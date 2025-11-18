package datacap

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/log"
	N "github.com/sagernet/sing/common/network"
)

// Wrapper provides optional datacap tracking for connections.
// If datacap is not configured, it acts as a pass-through.
type Wrapper struct {
	client           *Client
	logger           log.ContextLogger
	reportInterval   time.Duration
	enabled          bool
	enableThrottling bool
	throttleSpeed    int64
}

// WrapperConfig holds configuration for the datacap wrapper.
type WrapperConfig struct {
	SidecarURL       string // Empty means datacap disabled
	HTTPTimeout      time.Duration
	ReportInterval   time.Duration
	Logger           log.ContextLogger
	EnableThrottling bool  // Enable automatic throttling based on datacap status
	ThrottleSpeed    int64 // Fixed throttle speed in bytes/sec (0 = auto-calculate)
}

// NewWrapper creates a new datacap wrapper.
// If SidecarURL is empty, datacap is disabled and connections flow normally.
func NewWrapper(config WrapperConfig) *Wrapper {
	w := &Wrapper{
		logger:           config.Logger,
		reportInterval:   config.ReportInterval,
		enabled:          config.SidecarURL != "",
		enableThrottling: config.EnableThrottling,
		throttleSpeed:    config.ThrottleSpeed,
	}

	// Only create client if datacap is enabled
	if w.enabled {
		w.client = NewClient(config.SidecarURL, config.HTTPTimeout)
		if w.enableThrottling {
			w.logger.Info("datacap wrapper enabled with sidecar at: ", config.SidecarURL, " (throttling enabled)")
		} else {
			w.logger.Info("datacap wrapper enabled with sidecar at: ", config.SidecarURL, " (throttling disabled)")
		}
	} else {
		w.logger.Info("datacap wrapper disabled (no sidecar URL configured)")
	}

	return w
}

// WrapConn wraps a connection with datacap tracking if enabled.
// If datacap is disabled, returns the original connection unchanged.
func (w *Wrapper) WrapConn(conn net.Conn, deviceID, countryCode string) net.Conn {
	if !w.enabled || deviceID == "" {
		return conn
	}

	return NewConn(ConnConfig{
		Conn:             conn,
		Client:           w.client,
		DeviceID:         deviceID,
		CountryCode:      countryCode,
		Logger:           w.logger,
		ReportInterval:   w.reportInterval,
		EnableThrottling: w.enableThrottling,
		ThrottleSpeed:    w.throttleSpeed,
	})
}

// WrapPacketConn wraps a packet connection with datacap tracking if enabled.
// If datacap is disabled, returns the original connection unchanged.
func (w *Wrapper) WrapPacketConn(conn N.PacketConn, deviceID, countryCode string) N.PacketConn {
	if !w.enabled || deviceID == "" {
		return conn
	}

	return NewPacketConn(PacketConnConfig{
		Conn:             conn,
		Client:           w.client,
		DeviceID:         deviceID,
		CountryCode:      countryCode,
		Logger:           w.logger,
		ReportInterval:   w.reportInterval,
		EnableThrottling: w.enableThrottling,
		ThrottleSpeed:    w.throttleSpeed,
	})
}

// IsEnabled returns whether datacap tracking is enabled.
func (w *Wrapper) IsEnabled() bool {
	return w.enabled
}

// GetStatus queries datacap status for a device if enabled.
// Returns nil if datacap is disabled or on error.
func (w *Wrapper) GetStatus(ctx context.Context, deviceID string) (*DataCapStatus, error) {
	if !w.enabled || w.client == nil {
		return nil, nil
	}

	return w.client.GetDataCapStatus(ctx, deviceID)
}
