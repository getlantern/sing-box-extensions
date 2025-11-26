// Package metrics provides a metrics manager that uses OpenTelemetry to track
// various metrics related to the proxy server's performance. It includes
// tracking bytes sent and received, connection duration, and the number of
// connections. The metrics are recorded using OpenTelemetry's metric API and
// can be used for monitoring and observability purposes.
package metrics

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/getlantern/lantern-box/constant"
	"github.com/sagernet/sing-box/adapter"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/sagernet/sing-box/log"
)

type metricsManager struct {
	meter            metric.Meter
	duration         metric.Int64Histogram
	proxyIO          metric.Int64Counter
	proxyConnections metric.Int64Counter
}

const (
	defaultTelemetryHost = "telemetry.iantem.io:443"
)

// getTelemetryEndpoint returns the OTEL endpoint to use for telemetry.
// It checks the CUSTOM_OTLP_ENDPOINT environment variable first,
// falling back to the default if not set.
func getTelemetryEndpoint() string {
	if endpoint := os.Getenv("CUSTOM_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return defaultTelemetryHost
}

var metrics = NewMetricsManager()

func NewMetricsManager() *metricsManager {
	meter := otel.GetMeterProvider().Meter("")

	// Track connection duration.
	duration, err := meter.Int64Histogram("sing.connection_duration", metric.WithDescription("Connection duration"))
	if err != nil {
		duration = &noop.Int64Histogram{}
	}

	// Track bytes sent and received
	proxyIO, err := meter.Int64Counter("proxy.io", metric.WithUnit("bytes"))
	if err != nil {
		proxyIO = &noop.Int64Counter{}
	}
	// Track number of proxy connections
	proxyConnections, err := meter.Int64Counter("proxy.connections")
	if err != nil {
		proxyConnections = &noop.Int64Counter{}
	}
	buildTracerProvider(getTelemetryEndpoint())
	buildMeterProvider(getTelemetryEndpoint())
	return &metricsManager{
		meter:            meter,
		duration:         duration,
		proxyIO:          proxyIO,
		proxyConnections: proxyConnections,
	}
}

func metadataToAttributes(metadata *adapter.InboundContext) []attribute.KeyValue {
	// Convert metadata to attributes
	return []attribute.KeyValue{
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

const (
	batchTimeout = 1 * time.Minute
	maxQueueSize = 10000
)

func buildTracerProvider(endpoint string) {
	// Create HTTP client to talk to OTEL collector
	clientOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
	}

	// If endpoint doesn't use port 443, assume insecure (HTTP not HTTPS)
	if !strings.Contains(endpoint, ":443") {
		log.Debug("Using insecure connection for OTEL endpoint %v", endpoint)
		clientOpts = append(clientOpts, otlptracehttp.WithInsecure())
	}

	client := otlptracehttp.NewClient(clientOpts...)

	// Create an exporter that exports to the OTEL collector
	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		log.Error("Unable to initialize OpenTelemetry, will not report traces to %v", endpoint)
		return
	}
	log.Debug("Will report traces to OpenTelemetry at %v", endpoint)

	// Create a TracerProvider that uses the above exporter
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(
			exporter,
			sdktrace.WithBatchTimeout(batchTimeout),
			sdktrace.WithMaxQueueSize(maxQueueSize),
		),
		sdktrace.WithResource(buildResource()),
	)

	// Set the TracerProvider as the global provider
	otel.SetTracerProvider(tp)
}

func buildMeterProvider(endpoint string) {
	metricOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithTemporalitySelector(func(kind sdkmetric.InstrumentKind) metricdata.Temporality {
			switch kind {
			case
				sdkmetric.InstrumentKindCounter,
				sdkmetric.InstrumentKindUpDownCounter,
				sdkmetric.InstrumentKindObservableCounter,
				sdkmetric.InstrumentKindObservableUpDownCounter:
				return metricdata.DeltaTemporality
			default:
				return metricdata.CumulativeTemporality
			}
		}),
	}

	// If endpoint doesn't use port 443, assume insecure (HTTP not HTTPS)
	if !strings.Contains(endpoint, ":443") {
		log.Debug("Using insecure connection for OTEL metrics endpoint %v", endpoint)
		metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
	}

	exp, err := otlpmetrichttp.New(context.Background(), metricOpts...)
	if err != nil {
		return
	}

	// Create a new meter provider
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
		sdkmetric.WithResource(buildResource()),
	)

	// Set the meter provider as global
	otel.SetMeterProvider(mp)
}

func buildResource() *resource.Resource {
	return resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceNameKey.String("sing-box"),
		semconv.ServiceVersionKey.String(constant.Version),
	)
}
