package datacap

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/log"
)

// Conn wraps a net.Conn and tracks data consumption for datacap reporting.
type Conn struct {
	net.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	client      *Client
	deviceID    string
	countryCode string
	platform    string
	logger      log.ContextLogger

	// Atomic counters for thread-safe tracking
	bytesSent     atomic.Int64
	bytesReceived atomic.Int64

	// Reporting control
	reportTicker *time.Ticker
	reportMutex  sync.Mutex
	closed       atomic.Bool

	// Throttling
	throttler         *Throttler
	statusCheckTicker *time.Ticker
	throttlingEnabled bool
}

// ConnConfig holds configuration for creating a datacap-tracked connection.
type ConnConfig struct {
	Conn                net.Conn
	Client              *Client
	DeviceID            string
	CountryCode         string
	Platform            string
	Logger              log.ContextLogger
	ReportInterval      time.Duration
	EnableThrottling    bool
	StatusCheckInterval time.Duration
	ThrottleSpeed       int64
}

// NewConn creates a new datacap-tracked connection wrapper.
func NewConn(config ConnConfig) *Conn {
	ctx, cancel := context.WithCancel(context.Background())

	// Default report interval to 30 seconds if not specified
	if config.ReportInterval == 0 {
		config.ReportInterval = 30 * time.Second
	}

	// Default status check interval to 60 seconds if not specified
	if config.StatusCheckInterval == 0 {
		config.StatusCheckInterval = 60 * time.Second
	}

	conn := &Conn{
		Conn:              config.Conn,
		ctx:               ctx,
		cancel:            cancel,
		client:            config.Client,
		deviceID:          config.DeviceID,
		countryCode:       config.CountryCode,
		platform:          config.Platform,
		logger:            config.Logger,
		reportTicker:      time.NewTicker(config.ReportInterval),
		throttler:         NewThrottler(config.ThrottleSpeed),
		throttlingEnabled: config.EnableThrottling,
	}

	// Start periodic reporting goroutine
	go conn.periodicReport()

	// Start periodic status checking if throttling is enabled
	if conn.throttlingEnabled {
		conn.statusCheckTicker = time.NewTicker(config.StatusCheckInterval)
		go conn.periodicStatusCheck()
	}

	return conn
}

// Read tracks bytes received and applies throttling if enabled.
func (c *Conn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		c.bytesReceived.Add(int64(n))

		// Apply throttling after read (token bucket wait)
		if c.throttler.IsEnabled() {
			if waitErr := c.throttler.WaitRead(c.ctx, n); waitErr != nil {
				// Context cancelled, but we already read the data
				// Return the data and the error
				return n, waitErr
			}
		}
	}
	return
}

// Write tracks bytes sent and applies throttling if enabled.
func (c *Conn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		c.bytesSent.Add(int64(n))

		// Apply throttling after write (token bucket wait)
		if c.throttler.IsEnabled() {
			if waitErr := c.throttler.WaitWrite(c.ctx, n); waitErr != nil {
				// Context cancelled, but we already wrote the data
				// Return the bytes written and the error
				return n, waitErr
			}
		}
	}
	return
} // Close stops reporting and closes the underlying connection.
func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil // Already closed
	}

	// Stop the reporting ticker
	c.reportTicker.Stop()

	// Stop the status check ticker if enabled
	if c.statusCheckTicker != nil {
		c.statusCheckTicker.Stop()
	}

	// Cancel context to stop reporting goroutine
	c.cancel()

	// Send final report
	c.sendReport()

	return c.Conn.Close()
}

// periodicReport runs in a goroutine and periodically reports data consumption.
func (c *Conn) periodicReport() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.reportTicker.C:
			c.sendReport()
		}
	}
}

// periodicStatusCheck runs in a goroutine and periodically checks datacap status for throttling.
func (c *Conn) periodicStatusCheck() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.statusCheckTicker.C:
			c.checkAndApplyThrottling()
		}
	}
}

// checkAndApplyThrottling checks the datacap status and applies throttling if needed.
func (c *Conn) checkAndApplyThrottling() {
	status, err := c.GetStatus()
	if err != nil {
		c.logger.Debug("[datacap] failed to get status for throttling: ", err)
		return
	}

	if status.Throttle {
		// Calculate throttle speed based on remaining data
		// Default: throttle to 128 KB/s (conservative browsing speed)
		throttleSpeed := int64(128 * 1024) // 128 KB/s

		// Calculate percentage remaining
		if status.CapLimit > 0 && status.RemainingBytes >= 0 {
			percentRemaining := float64(status.RemainingBytes) / float64(status.CapLimit)

			// Scale throttle speed: 50% remaining = 512 KB/s, 10% = 128 KB/s, etc.
			maxSpeed := int64(1024 * 1024) // 1 MB/s max when throttled
			minSpeed := int64(64 * 1024)   // 64 KB/s minimum
			throttleSpeed = int64(float64(maxSpeed-minSpeed)*percentRemaining) + minSpeed
		}

		// Following http-proxy-lantern approach: asymmetric throttling
		// When capped, only throttle downloads (reads), not uploads (writes)
		// This allows users to upload content even when capped
		defaultRate := int64(5 * 1024 * 1024) // 5 Mbps default for uploads
		c.throttler.EnableWithRates(throttleSpeed, defaultRate)
		c.logger.Info("[datacap] throttling enabled for device ", c.deviceID,
			" - downloads: ", throttleSpeed/1024, " KB/s, uploads: ", defaultRate/1024/1024, " Mbps (remaining: ", status.RemainingBytes, " bytes)")
	} else {
		if c.throttler.IsEnabled() {
			c.throttler.Disable()
			c.logger.Info("[datacap] throttling disabled for device ", c.deviceID)
		}
	}
}

// sendReport sends the current consumption data to the sidecar.
func (c *Conn) sendReport() {
	c.reportMutex.Lock()
	defer c.reportMutex.Unlock()

	// Skip if client is nil (datacap disabled)
	if c.client == nil {
		return
	}

	sent := c.bytesSent.Load()
	received := c.bytesReceived.Load()
	totalConsumed := sent + received

	// Only report if there's data to report
	if totalConsumed == 0 {
		return
	}

	report := &DataCapReport{
		DeviceID:    c.deviceID,
		CountryCode: c.countryCode,
		Platform:    c.platform,
		BytesUsed:   totalConsumed,
	}

	// Use a timeout context for the report
	reportCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.client.ReportDataCapConsumption(reportCtx, report); err != nil {
		// Just log the error, don't fail the connection
		c.logger.Debug("failed to report datacap consumption (non-fatal): ", err)
	} else {
		c.logger.Debug("reported datacap consumption: ", totalConsumed, " bytes (sent: ", sent, ", received: ", received, ") for device ", c.deviceID)
	}
}

// GetStatus queries the sidecar for current data cap status.
func (c *Conn) GetStatus() (*DataCapStatus, error) {
	// Skip if client is nil (datacap disabled)
	if c.client == nil {
		return nil, nil
	}

	statusCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return c.client.GetDataCapStatus(statusCtx, c.deviceID)
}

// GetBytesConsumed returns the total bytes consumed by this connection.
func (c *Conn) GetBytesConsumed() int64 {
	return c.bytesSent.Load() + c.bytesReceived.Load()
}
