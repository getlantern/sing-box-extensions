package water

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	waterDownloader "github.com/getlantern/lantern-water/downloader"
	"github.com/getlantern/sing-box-extensions/constant"
	L "github.com/getlantern/sing-box-extensions/log"
	"github.com/getlantern/sing-box-extensions/option"
	waterTransport "github.com/getlantern/sing-box-extensions/transport/water"
	"github.com/refraction-networking/water"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/mux"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"

	_ "github.com/refraction-networking/water/transport/v1"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.WATERInboundOptions](registry, constant.TypeWATER, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx           context.Context
	logger        log.ContextLogger
	core          water.Core
	waterListener water.Listener
	listener      *listener.Listener
	router        adapter.ConnectionRouterEx
	service       *waterTransport.Service
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.WATERInboundOptions) (adapter.Inbound, error) {
	if options.Transport == "" {
		return nil, E.New("transport not defined")
	}
	if len(options.WASMAvailableAt) == 0 {
		return nil, E.New("no WASM URLs available")
	}

	d, err := waterDownloader.NewWASMDownloader(options.WASMAvailableAt, &http.Client{Timeout: 1 * time.Minute})
	if err != nil {
		return nil, E.New("failed to create WASM downloader", err)
	}

	wasmBuffer := new(bytes.Buffer)
	// this will lock the inbound until it finishes to download
	if err = d.DownloadWASM(ctx, wasmBuffer); err != nil {
		return nil, E.New("unable to download water wasm", err)
	}

	inbound := &Inbound{
		Adapter: inbound.NewAdapter(constant.TypeWATER, tag),
		ctx:     ctx,
		logger:  logger,
		router:  router,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Listen:  options.ListenOptions,
		}),
	}

	inbound.router, err = mux.NewRouterWithOptions(router, logger, common.PtrValueOrDefault(options.Multiplex))
	if err != nil {
		return nil, err
	}

	inbound.service = waterTransport.NewService(logger, adapter.NewUpstreamHandlerEx(adapter.InboundContext{}, inbound.newConnection, inbound.newPacketConnection))
	tcpListener, err := inbound.listener.ListenTCP()
	if err != nil {
		return nil, err
	}

	cfg := &water.Config{
		OverrideLogger:     slog.New(L.NewLogHandler(logger)),
		TransportModuleBin: wasmBuffer.Bytes(),
		NetworkListener:    tcpListener,
	}

	inbound.waterListener, err = water.NewListenerWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return inbound, nil
}

func (i *Inbound) newConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	i.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	i.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (i *Inbound) newPacketConnection(ctx context.Context, conn network.PacketConn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	i.logger.ErrorContext(ctx, "packet connection not implemented")
}

func (i *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}

	go func() {
		for {
			conn, err := i.waterListener.AcceptWATER()
			if err != nil {
				i.logger.Error(err)
				return
			}

			var metadata adapter.InboundContext
			metadata.Source = M.SocksaddrFromNet(conn.RemoteAddr()).Unwrap()
			metadata.OriginDestination = M.SocksaddrFromNet(conn.LocalAddr()).Unwrap()
			ctx := log.ContextWithNewID(i.ctx)
			go i.service.NewConnection(ctx, conn, metadata.Source, func(it error) {
				if it != nil {
					i.logger.ErrorContext(ctx, it)
				}
			})
		}
	}()
	return nil
}

func (i *Inbound) Close() error {
	return i.listener.Close()
}
