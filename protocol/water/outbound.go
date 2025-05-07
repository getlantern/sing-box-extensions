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
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
)

// RegisterOutbound registers the WATER outbound adapter with the given registry.
func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.WATEROutboundOptions](registry, constant.TypeWATER, NewOutbound)
}

// Outbound represents a WATER outbound adapter.
type Outbound struct {
	outbound.Adapter
	logger      logger.ContextLogger
	waterDialer water.Dialer
	serverAddr  string
}

// NewOutbound creates a new WATER outbound adapter.
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

	if options.Config != nil {
		transportModuleConfig, err := json.MarshalContext(ctx, options.Config)
		if err != nil {
			return nil, err
		}

		cfg.TransportModuleConfig = water.TransportModuleConfigFromBytes(transportModuleConfig)
	}

	waterDialer, err := water.NewDialerWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	serverAddr := options.ServerOptions.Build()

	outbound := &Outbound{
		Adapter:     outbound.NewAdapterWithDialerOptions(constant.TypeWATER, tag, []string{network.NetworkTCP}, options.DialerOptions),
		logger:      logger,
		waterDialer: waterDialer,
		serverAddr:  fmt.Sprintf("%s:%d", serverAddr.TCPAddr().IP.String(), serverAddr.Port),
	}

	return outbound, nil
}

// DialContext dials a connection to the specified network and destination.
func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination

	conn, err := o.waterDialer.DialContext(ctx, network, o.serverAddr)
	if err != nil {
		return nil, err
	}

	return waterTransport.NewWATERConnection(conn, destination), nil
}

// ListenPacket is not implemented
func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("not implemented")
}

// Network returns the supported network types for this outbound adapter.
// In this case, it supports only TCP.
func (o *Outbound) Network() []string {
	return []string{network.NetworkTCP}
}
