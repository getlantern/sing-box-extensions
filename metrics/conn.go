package metrics

import (
	"context"
	"net"
	"slices"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Conn wraps a net.Conn and tracks metrics such as bytes sent and received.
type Conn struct {
	net.Conn
	attributes metric.MeasurementOption
	readAttrs  metric.MeasurementOption
	writeAttrs metric.MeasurementOption
	startTime  time.Time
}

// NewConn creates a new Conn instance.
func NewConn(conn net.Conn, metadata *adapter.InboundContext) net.Conn {
	attributes := metadataToAttributes(metadata)
	attrOpt := metric.WithAttributes(attributes...)
	// prealloc attr slices to avoid doing that on each read/write
	readAttrs := metric.WithAttributes(append(slices.Clone(attributes), attribute.String("direction", "receive"))...)
	writeAttrs := metric.WithAttributes(append(attributes, attribute.String("direction", "transmit"))...)
	metrics.proxyConnections.Add(context.Background(), 1, attrOpt)
	return &Conn{
		Conn:       conn,
		readAttrs:  readAttrs,
		writeAttrs: writeAttrs,
		attributes: attrOpt,
		startTime:  time.Now(),
	}
}

// Read overrides net.Conn's Read method to track received bytes.
func (c *Conn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		metrics.proxyIO.Add(context.Background(), int64(n), c.readAttrs)
	}
	return
}

// Write overrides net.Conn's Write method to track sent bytes.
func (c *Conn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		metrics.proxyIO.Add(context.Background(), int64(n), c.writeAttrs)
	}
	return
}

// Close overrides net.Conn's Close method to track connection duration.
func (c *Conn) Close() error {
	duration := time.Since(c.startTime).Milliseconds()
	metrics.duration.Record(context.Background(), duration, c.attributes)
	return c.Conn.Close()
}
