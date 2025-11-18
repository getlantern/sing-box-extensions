package datacap

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

type InboundWrapper struct {
	inbound.Adapter
	ctx                  context.Context
	logger               log.ContextLogger
	listener             *listener.Listener
	router               adapter.Router
	datacapWrapper       *Wrapper
	deviceIDExtractor    DeviceIDExtractorFunc
	countryCodeExtractor CountryCodeExtractorFunc
}

type DeviceIDExtractorFunc func(ctx context.Context, metadata adapter.InboundContext) string

type CountryCodeExtractorFunc func(ctx context.Context, metadata adapter.InboundContext) string

type InboundWrapperConfig struct {
	Tag                  string
	Ctx                  context.Context
	Logger               log.ContextLogger
	Router               adapter.Router
	ListenerOptions      listener.Options
	SidecarURL           string
	HTTPTimeout          time.Duration
	ReportInterval       time.Duration
	EnableThrottling     bool
	StatusCheckInterval  time.Duration
	ThrottleSpeed        int64
	DeviceIDExtractor    DeviceIDExtractorFunc
	CountryCodeExtractor CountryCodeExtractorFunc
}

func NewInboundWrapper(config InboundWrapperConfig) (adapter.Inbound, error) {
	if config.Tag == "" {
		return nil, E.New("tag is required")
	}

	if config.DeviceIDExtractor == nil {
		config.DeviceIDExtractor = defaultDeviceIDExtractor
	}
	if config.CountryCodeExtractor == nil {
		config.CountryCodeExtractor = defaultCountryCodeExtractor
	}

	if config.HTTPTimeout == 0 {
		config.HTTPTimeout = 10 * time.Second
	}
	if config.ReportInterval == 0 {
		config.ReportInterval = 30 * time.Second
	}

	wrapper := &InboundWrapper{
		Adapter:              inbound.NewAdapter("datacap", config.Tag),
		ctx:                  config.Ctx,
		logger:               config.Logger,
		router:               config.Router,
		deviceIDExtractor:    config.DeviceIDExtractor,
		countryCodeExtractor: config.CountryCodeExtractor,
	}

	wrapper.datacapWrapper = NewWrapper(WrapperConfig{
		SidecarURL:          config.SidecarURL,
		HTTPTimeout:         config.HTTPTimeout,
		ReportInterval:      config.ReportInterval,
		Logger:              config.Logger,
		EnableThrottling:    config.EnableThrottling,
		StatusCheckInterval: config.StatusCheckInterval,
		ThrottleSpeed:       config.ThrottleSpeed,
	})

	config.ListenerOptions.ConnectionHandler = wrapper
	wrapper.listener = listener.New(config.ListenerOptions)

	return wrapper, nil
}

func (w *InboundWrapper) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	metadata.Inbound = w.Tag()
	metadata.InboundType = w.Type()

	if !w.datacapWrapper.IsEnabled() {
		w.router.RouteConnectionEx(ctx, conn, metadata, onClose)
		return
	}

	deviceID := w.deviceIDExtractor(ctx, metadata)
	countryCode := w.countryCodeExtractor(ctx, metadata)

	if deviceID != "" {
		w.logger.DebugContext(ctx, "[datacap] tracking connection from device: ", deviceID, " (", countryCode, ") to ", metadata.Destination)
	} else {
		w.logger.DebugContext(ctx, "[datacap] connection to ", metadata.Destination, " (no device ID)")
	}

	wrappedConn := w.datacapWrapper.WrapConn(conn, deviceID, countryCode)
	w.router.RouteConnectionEx(ctx, wrappedConn, metadata, onClose)
}

func (w *InboundWrapper) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	metadata.Inbound = w.Tag()
	metadata.InboundType = w.Type()

	if !w.datacapWrapper.IsEnabled() {
		w.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
		return
	}

	deviceID := w.deviceIDExtractor(ctx, metadata)
	countryCode := w.countryCodeExtractor(ctx, metadata)

	if deviceID != "" {
		w.logger.DebugContext(ctx, "[datacap] tracking packet connection from device: ", deviceID, " (", countryCode, ") to ", metadata.Destination)
	} else {
		w.logger.DebugContext(ctx, "[datacap] packet connection to ", metadata.Destination, " (no device ID)")
	}

	wrappedConn := w.datacapWrapper.WrapPacketConn(conn, deviceID, countryCode)
	w.router.RoutePacketConnectionEx(ctx, wrappedConn, metadata, onClose)
}

func (w *InboundWrapper) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return w.listener.Start()
}

func (w *InboundWrapper) Close() error {
	return w.listener.Close()
}

func defaultDeviceIDExtractor(ctx context.Context, metadata adapter.InboundContext) string {
	return ""
}

func defaultCountryCodeExtractor(ctx context.Context, metadata adapter.InboundContext) string {
	return ""
}
