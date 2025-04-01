package water

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	waterDownloader "github.com/getlantern/lantern-water/downloader"
	waterVC "github.com/getlantern/lantern-water/version_control"
	"github.com/getlantern/sing-box-extensions/constant"
	L "github.com/getlantern/sing-box-extensions/log"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/refraction-networking/water"
	_ "github.com/refraction-networking/water/transport/v1"
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
	waterDialer    water.Dialer
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.WATEROutboundOptions) (adapter.Outbound, error) {
	timeout, err := time.ParseDuration(options.DownloadTimeout)
	if err != nil {
		return nil, err
	}

	slogLogger := slog.New(L.NewLogHandler(logger))
	vc := waterVC.NewWaterVersionControl(options.Dir, slogLogger)
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

	logger.DebugContext(ctx, "downloaded WASM with len: ", len(b), " bytes")

	outboundDialer, err := dialer.New(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}

	outbound := &Outbound{
		logger:         logger,
		outboundDialer: outboundDialer,
		serverAddr:     options.ServerOptions.Build(),
	}

	cfg := &water.Config{
		TransportModuleBin: b,
		OverrideLogger:     slogLogger,
		NetworkDialerFunc: func(network, address string) (net.Conn, error) {
			logger.Debug("received address ", address)
			addr, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, err
			}

			socksAddr := M.SocksaddrFromNetIP(addr)
			logger.Debug("dialing to address: ", address, " socks addr: ", socksAddr)
			serverConn, err := outboundDialer.DialContext(log.ContextWithNewID(ctx), network, outbound.serverAddr)
			if err != nil {
				return nil, err
			}

			return serverConn, nil
		},
	}

	outbound.waterDialer, err = water.NewDialerWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return outbound, nil
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination

	return o.waterDialer.DialContext(ctx, network, fmt.Sprintf("%s:%d", destination.TCPAddr().IP.String(), destination.Port))
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

func (o *Outbound) Network() []string {
	return []string{network.NetworkTCP}
}
