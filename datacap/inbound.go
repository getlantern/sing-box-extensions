package datacap

import (
	"context"
	"net"
	"time"

	"github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// RegisterInbound registers the datacap inbound with the given registry.
func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.DataCapInboundOptions](registry, constant.TypeDataCap, NewInbound)
}

// Inbound represents a datacap wrapper inbound that tracks data consumption.
type Inbound struct {
	inbound.Adapter
	ctx                 context.Context
	logger              log.ContextLogger
	listener            *listener.Listener
	router              adapter.Router
	datacapClient       *Client
	reportInterval      time.Duration
	deviceIDHeader      string
	countryCodeHeader   string
	platformHeader      string
	enableThrottling    bool
	statusCheckInterval time.Duration
	extractor           *DeviceIDExtractor
}

// NewInbound creates a new datacap wrapper inbound adapter.
func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DataCapInboundOptions) (adapter.Inbound, error) {
	if options.SidecarURL == "" {
		return nil, E.New("sidecar_url not defined")
	}

	// Parse intervals with defaults
	reportInterval := 30 * time.Second
	if options.ReportInterval != "" {
		interval, err := time.ParseDuration(options.ReportInterval)
		if err != nil {
			return nil, E.New("invalid report_interval: ", err)
		}
		reportInterval = interval
	}

	httpTimeout := 10 * time.Second
	if options.HTTPTimeout != "" {
		timeout, err := time.ParseDuration(options.HTTPTimeout)
		if err != nil {
			return nil, E.New("invalid http_timeout: ", err)
		}
		httpTimeout = timeout
	}

	statusCheckInterval := 60 * time.Second
	if options.StatusCheckInterval != "" {
		interval, err := time.ParseDuration(options.StatusCheckInterval)
		if err != nil {
			return nil, E.New("invalid status_check_interval: ", err)
		}
		statusCheckInterval = interval
	}

	// Set default header names
	deviceIDHeader := options.DeviceIDHeader
	if deviceIDHeader == "" {
		deviceIDHeader = "X-Device-ID"
	}

	countryCodeHeader := options.CountryCodeHeader
	if countryCodeHeader == "" {
		countryCodeHeader = "X-Country-Code"
	}

	platformHeader := options.PlatformHeader
	if platformHeader == "" {
		platformHeader = "X-Platform"
	}

	dcInbound := &Inbound{
		Adapter:             inbound.NewAdapter(constant.TypeDataCap, tag),
		ctx:                 ctx,
		logger:              logger,
		router:              router,
		datacapClient:       NewClient(options.SidecarURL, httpTimeout),
		reportInterval:      reportInterval,
		deviceIDHeader:      deviceIDHeader,
		countryCodeHeader:   countryCodeHeader,
		platformHeader:      platformHeader,
		enableThrottling:    options.EnableThrottling,
		statusCheckInterval: statusCheckInterval,
		extractor:           NewDeviceIDExtractor(deviceIDHeader, countryCodeHeader, platformHeader, logger),
	}

	dcInbound.listener = listener.New(listener.Options{
		Context:           ctx,
		Logger:            logger,
		Network:           []string{N.NetworkTCP},
		Listen:            options.ListenOptions,
		ConnectionHandler: dcInbound,
	})

	return dcInbound, nil
}

// NewConnectionEx handles new connections with datacap tracking.
func (i *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	// Extract device ID, country code, and platform from metadata or connection
	deviceID := i.extractDeviceID(ctx, metadata)
	countryCode := i.extractCountryCode(ctx, metadata)
	platform := i.extractPlatform(ctx, metadata)

	if deviceID == "" {
		i.logger.Warn("no device ID found, datacap tracking disabled for this connection")
		// Route without datacap tracking
		i.routeConnection(ctx, conn, metadata, onClose)
		return
	}

	i.logger.InfoContext(ctx, "datacap tracking enabled for device: ", deviceID, " (country: ", countryCode, ", platform: ", platform, ")")

	// Wrap connection with datacap tracking
	datacapConn := NewConn(ConnConfig{
		Conn:                conn,
		Client:              i.datacapClient,
		DeviceID:            deviceID,
		CountryCode:         countryCode,
		Platform:            platform,
		Logger:              i.logger,
		ReportInterval:      i.reportInterval,
		EnableThrottling:    i.enableThrottling,
		StatusCheckInterval: i.statusCheckInterval,
	})

	// Route the wrapped connection
	i.routeConnection(ctx, datacapConn, metadata, onClose)
}

// routeConnection routes the connection through the router.
func (i *Inbound) routeConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	i.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	i.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (i *Inbound) extractDeviceID(ctx context.Context, metadata adapter.InboundContext) string {
	deviceID, _, _ := i.extractor.ExtractFromMetadata(metadata)
	return deviceID
}

func (i *Inbound) extractCountryCode(ctx context.Context, metadata adapter.InboundContext) string {
	_, countryCode, _ := i.extractor.ExtractFromMetadata(metadata)
	return countryCode
}

func (i *Inbound) extractPlatform(ctx context.Context, metadata adapter.InboundContext) string {
	_, _, platform := i.extractor.ExtractFromMetadata(metadata)
	return platform
}

// Start starts the datacap inbound adapter.
func (i *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return i.listener.Start()
}

// Close stops the datacap inbound listener.
func (i *Inbound) Close() error {
	return i.listener.Close()
}
