package water

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"time"

	waterDownloader "github.com/getlantern/lantern-water/downloader"
	"github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/refraction-networking/water"
	transport "github.com/refraction-networking/water/transport/v1"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/network"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.WATERInboundOptions](registry, constant.TypeWATER, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx      context.Context
	logger   log.ContextLogger
	core     water.Core
	listener *listener.Listener
	router   adapter.Router
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
		return nil, E.New("failed to create WASM downloader")
	}

	wasmBuffer := new(bytes.Buffer)
	// thil will lock the inbound until it finishes to download
	if err = d.DownloadWASM(ctx, wasmBuffer); err != nil {
		return nil, E.New("unable to download water wasm: %w", err)
	}

	cfg := &water.Config{
		// TODO: create slog.Logger that uses the sing-box logger
		// OverrideLogger: ,
		TransportModuleBin: wasmBuffer.Bytes(),
	}
	core, err := water.NewCoreWithContext(ctx, cfg)
	if err != nil {
		return nil, E.New("failed to create water listener: %w", err)
	}
	inbound := &Inbound{
		Adapter: inbound.NewAdapter(constant.TypeWATER, tag),
		ctx:     ctx,
		logger:  logger,
		router:  router,
		core:    core,
	}

	inbound.listener = listener.New(listener.Options{
		Context:           ctx,
		Logger:            logger,
		Listen:            options.ListenOptions,
		ConnectionHandler: inbound,
	})

	return inbound, nil
}

func (i *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	src, err := transport.UpgradeCore(i.core).AcceptFor(conn)
	if err != nil {
		i.logger.ErrorContext(ctx, E.Cause(err, "accepting connection from ", metadata.Source))
		return
	}

	err = i.newConnection(ctx, src, metadata)
	if err != nil {
		if E.IsClosedOrCanceled(err) {
			i.logger.DebugContext(ctx, "connection closed: ", err)
		} else {
			i.logger.ErrorContext(ctx, E.Cause(err, "process connection from ", metadata.Source))
		}
	}
}

func (i *Inbound) newConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	i.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	return i.router.RouteConnection(ctx, conn, metadata)
}

func (i *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return i.listener.Start()
}

func (i *Inbound) Close() error {
	return i.listener.Close()
}
