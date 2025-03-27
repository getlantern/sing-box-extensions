package water

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	waterDownloader "github.com/getlantern/lantern-water/downloader"
	waterVC "github.com/getlantern/lantern-water/version_control"
	"github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/refraction-networking/water"
	transport "github.com/refraction-networking/water/transport/v1"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.WATEROutboundOptions](registry, constant.TypeWATER, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	logger         logger.ContextLogger
	serverAddr     M.Socksaddr
	outboundDialer network.Dialer
	core           water.Core
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.WATEROutboundOptions) (adapter.Outbound, error) {
	timeout, err := time.ParseDuration(options.DownloadTimeout)
	if err != nil {
		return nil, err
	}

	// TODO: create slog.Logger that uses the sing-box logger
	vc := waterVC.NewWaterVersionControl(options.Dir, nil)
	d, err := waterDownloader.NewWASMDownloader(options.WASMAvailableAt, &http.Client{Timeout: timeout})
	if err != nil {
		return nil, E.New("failed to create WASM downloader", err)
	}

	rc, err := vc.GetWASM(ctx, options.Transport, d)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	cfg := &water.Config{
		TransportModuleBin: b,
		// TODO: create slog.Logger that uses the sing-box logger
		// OverrideLogger: ,
	}

	core, err := water.NewCoreWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}

	outboundDialer, err := dialer.New(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}

	outbound := &Outbound{
		logger:         logger,
		outboundDialer: outboundDialer,
		core:           core,
	}

	return outbound, nil
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination

	conn, err := o.outboundDialer.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}

	transportModule := transport.UpgradeCore(o.core)
	dstConn, err := transportModule.DialFrom(conn)
	if err != nil {
		o.logger.ErrorContext(ctx, E.Cause(err, "accepting connection from ", metadata.Source))
		return nil, err
	}

	if err := transportModule.StartWorker(); err != nil {
		o.logger.ErrorContext(ctx, E.Cause(err, "failed to start WATER worker", metadata.Source))
		return nil, err
	}
	return dstConn, nil
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination
	return nil, E.New("not implemented")
}

func (o *Outbound) Close() error {
	return nil
}
