package group

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func RegisterSelector(registry *outbound.Registry) {
	outbound.Register[option.SelectorOutboundOptions](registry, constant.TypeSelector, NewSelector)
}

const tracerName = "github.com/getlantern/sing-box-extensions/protocol/group"

type Selector struct {
	*group.Selector
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	outbound, err := group.NewSelector(ctx, router, logger, tag, options)
	if err != nil {
		return nil, err
	}

	return &Selector{outbound.(*group.Selector)}, nil
}

func (s *Selector) DialContext(ctx context.Context, network string, destination metadata.Socksaddr) (net.Conn, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "Selector.DialContext", trace.WithAttributes(
		attribute.String("network", network),
		attribute.StringSlice("supported_network_options", s.Network()),
		attribute.String("outbound", s.Now()),
		attribute.String("tag", s.Tag()),
		attribute.String("type", s.Type()),
	))
	defer span.End()
	conn, err := s.Selector.DialContext(ctx, network, destination)
	span.RecordError(err)
	return conn, err
}

func (s *Selector) ListenPacket(ctx context.Context, destination metadata.Socksaddr) (net.PacketConn, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "Selector.ListenPacket", trace.WithAttributes(
		attribute.StringSlice("supported_network_options", s.Network()),
		attribute.String("outbound", s.Now()),
		attribute.String("tag", s.Tag()),
		attribute.String("type", s.Type()),
	))
	defer span.End()
	conn, err := s.Selector.ListenPacket(ctx, destination)
	span.RecordError(err)
	return conn, err
}

func (s *Selector) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "Selector.NewConnectionEx", trace.WithAttributes(
		attribute.String("inbound", s.Now()),
		attribute.String("outbound", metadata.Outbound),
		attribute.String("protocol", metadata.Protocol),
		attribute.String("tag", s.Tag()),
		attribute.String("type", s.Type()),
	))
	defer span.End()

	s.Selector.NewConnectionEx(ctx, conn, metadata, func(it error) {
		span.RecordError(it)
		onClose(it)
	})
}
func (s *Selector) NewPacketConnectionEx(ctx context.Context, conn network.PacketConn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "Selector.NewPacketConnectionEx", trace.WithAttributes(
		attribute.String("inbound", s.Now()),
		attribute.String("outbound", metadata.Outbound),
		attribute.String("protocol", metadata.Protocol),
		attribute.String("tag", s.Tag()),
		attribute.String("type", s.Type()),
	))
	defer span.End()

	s.Selector.NewPacketConnectionEx(ctx, conn, metadata, func(it error) {
		span.RecordError(it)
		if onClose != nil {
			onClose(it)
		}
	})
}
