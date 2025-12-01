// Package metrics provides a metrics manager that uses OpenTelemetry to track
// various metrics related to the proxy server's performance. It includes
// tracking bytes sent and received, connection duration, and the number of
// connections. The metrics are recorded using OpenTelemetry's metric API and
// can be used for monitoring and observability purposes.
package metrics

import (
	"sync"
	"time"

	"github.com/getlantern/geo"
	"github.com/getlantern/http-proxy-lantern/v2/instrument/distinct"
	"github.com/sagernet/sing-box/adapter"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

var (
	initOnce                                                 sync.Once
	meter                                                    metric.Meter
	Blacklist                                                metric.Int64Counter
	ProxyIO                                                  metric.Int64Counter
	QuicPackets                                              metric.Int64Counter
	Mimicked                                                 metric.Int64Counter
	MultipathFrames                                          metric.Int64Counter
	MultipathIO                                              metric.Int64Counter
	XBQ                                                      metric.Int64Counter
	Throttling                                               metric.Int64Counter
	SuspectedProbing                                         metric.Int64Counter
	Connections                                              metric.Int64Counter
	DistinctClients1m, DistinctClients10m, DistinctClients1h *distinct.SlidingWindowDistinctCount
	distinctClients                                          metric.Int64ObservableGauge
)

var (
	geolite2CityURL = "https://lanterngeo.lantern.io/GeoLite2-City.mmdb.tar.gz"
	geoip2ISPURL    = "https://lanterngeo.lantern.io/GeoIP2-ISP.mmdb.tar.gz"
)

type metricsManager struct {
	meter   metric.Meter
	ProxyIO metric.Int64Counter
	// bytesSent     metric.Int64Counter
	// bytesReceived metric.Int64Counter
	Connections   metric.Int64Counter
	duration      metric.Int64Histogram
	conns         metric.Int64UpDownCounter
	countryLookup geo.CountryLookup
	ispLookup     geo.ISPLookup
}

var metrics = newMetricsManager()

func newMetricsManager() *metricsManager {
	meter := otel.GetMeterProvider().Meter("lantern-box")
	bytesSent, err := meter.Int64Counter("sing.bytes_sent", metric.WithDescription("Bytes sent"))
	if err != nil {
		bytesSent = &noop.Int64Counter{}
	}
	bytesReceived, err := meter.Int64Counter("sing.bytes_received", metric.WithDescription("Bytes received"))
	if err != nil {
		bytesReceived = &noop.Int64Counter{}
	}

	// Track connection duration.
	duration, err := meter.Int64Histogram("sing.connection_duration", metric.WithDescription("Connection duration"))
	if err != nil {
		duration = &noop.Int64Histogram{}
	}

	// Track the number of connections.
	conns, err := meter.Int64UpDownCounter("sing.connections", metric.WithDescription("Number of connections"))
	if err != nil {
		conns = &noop.Int64UpDownCounter{}
	}

	countryLookup := geo.FromWeb(geolite2CityURL, "GeoLite2-City.mmdb", 24*time.Hour, cityDBFile, geo.CountryCode)
	ispLookup := geo.FromWeb(geoip2ISPURL, "GeoIP2-ISP.mmdb", 24*time.Hour, *geoip2ISPDBFile, geo.ISP)

	return &metricsManager{
		meter: meter,
		// bytesSent:     bytesSent,
		// bytesReceived: bytesReceived,
		duration:      duration,
		conns:         conns,
		ispLookup:     ispLookup,
		countryLookup: countryLookup,
	}
}

func metadataToAttributes(metadata *adapter.InboundContext) []attribute.KeyValue {
	// Convert metadata to attributes
	fromCountry := metrics.countryLookup.CountryCode(metadata.Source.IPAddr().IP)
	return []attribute.KeyValue{
		attribute.String("country", fromCountry),
		attribute.String("proxy_ip", metadata.Destination.IPAddr().String()),
		attribute.String("protocol", metadata.Protocol),
		attribute.String("user", metadata.User),
		attribute.String("inbound", metadata.Inbound),
		attribute.String("inbound_type", metadata.InboundType),
		attribute.String("outbound", metadata.Outbound),
		attribute.String("client", metadata.Client),
		attribute.String("domain", metadata.Domain),
	}
}
