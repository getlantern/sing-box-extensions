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
	waterTransport "github.com/getlantern/sing-box-extensions/transport/water"
	"github.com/refraction-networking/water"
	_ "github.com/refraction-networking/water/transport/v1"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/mux"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.WATEROutboundOptions](registry, constant.TypeWATER, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	logger          logger.ContextLogger
	waterDialer     water.Dialer
	serverAddr      M.Socksaddr
	multiplexDialer *mux.Client
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

	serverAddr := options.ServerOptions.Build()

	cfg := &water.Config{
		TransportModuleBin: b,
		OverrideLogger:     slogLogger,
		NetworkDialerFunc: func(network, address string) (net.Conn, error) {
			addr, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, err
			}

			return outboundDialer.DialContext(log.ContextWithNewID(ctx), network, M.SocksaddrFromNetIP(addr))
		},
	}
	waterDialer, err := water.NewDialerWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}

	outbound := &Outbound{
		logger:      logger,
		waterDialer: waterDialer,
		serverAddr:  serverAddr,
	}

	outbound.multiplexDialer, err = mux.NewClientWithOptions((*wDialer)(outbound), logger, common.PtrValueOrDefault(options.Multiplex))
	if err != nil {
		return nil, err
	}

	return outbound, nil
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination

	if o.multiplexDialer != nil {
		return o.multiplexDialer.DialContext(ctx, network, destination)
	}

	return (*wDialer)(o).DialContext(ctx, network, destination)
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.logger.ErrorContext(ctx, "received listen packet but UDP not supported")
	if o.multiplexDialer != nil {
		return o.multiplexDialer.ListenPacket(ctx, destination)
	}
	return (*wDialer)(o).ListenPacket(ctx, destination)
}

func (o *Outbound) Close() error {
	return nil
}

func (o *Outbound) Network() []string {
	return []string{N.NetworkTCP}
}

type wDialer Outbound

func (d *wDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = d.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		addr := fmt.Sprintf("%s:%d", d.serverAddr.TCPAddr().IP.String(), d.serverAddr.Port)
		waterConn, err := d.waterDialer.DialContext(ctx, N.NetworkTCP, addr)
		if err != nil {
			return nil, err
		}
		return waterTransport.NewWATERConnection(waterConn, destination), nil
	case N.NetworkUDP:
		d.logger.ErrorContext(ctx, "received listen packet but UDP not supported")
		addr := fmt.Sprintf("%s:%d", d.serverAddr.TCPAddr().IP.String(), d.serverAddr.Port)
		waterConn, err := d.waterDialer.DialContext(ctx, N.NetworkTCP, addr)
		if err != nil {
			return nil, err
		}
		return bufio.NewBindPacketConn(waterTransport.NewWATERPacketConn(waterConn), destination), nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (d *wDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = d.Tag()
	metadata.Destination = destination
	addr := fmt.Sprintf("%s:%d", d.serverAddr.TCPAddr().IP.String(), d.serverAddr.Port)
	waterConn, err := d.waterDialer.DialContext(ctx, N.NetworkTCP, addr)
	if err != nil {
		return nil, err
	}
	return waterTransport.NewWATERPacketConn(waterConn), nil
}
