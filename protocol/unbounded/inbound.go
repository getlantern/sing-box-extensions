package unbounded

import (
	"context"

	C "github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/log"
)

// TODO: there is nothing here yet.

// this will be the unbounded proxy (uncensored users) that bridges QUIC connections from the desktop to
// the egress server, and potentially we can skip egress server but just send the traffic to the destination instead

// This element builds WebRTC tunnels with the unbounded outbound in radiance (censored peers) and starts a QUIC server that accepts connections from the unbounded outbound

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register(registry, C.TypeUnbounded, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx    context.Context
	router adapter.Router
	// TODO
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.UnboundedInboundOptions) (adapter.Inbound, error) {

	// TODO: this is different. We now want to combine widget/egress together here
	// and avoid websocket/webtransport connections between widget <-> egress server

	// Essentially this will
	// 1. start a QUIC listener that accepts connections solely from the WebRTC data tunnel
	// 2. builds that WebRTC tunnels with outbound.go

	// as a first step maybe consider:
	// - start a normal widget, with websocket connection to localhost egress server(for simplicity)
	// - start a normal egress server at localhost

	inbound := &Inbound{
		Adapter: inbound.NewAdapter(C.TypeUnbounded, tag),
		ctx:     ctx,
		router:  router,
	}

	return inbound, nil
}

func (i *Inbound) Start(stage adapter.StartStage) error {
	// TODO
	return nil
}

func (i *Inbound) Close() error {
	// TODO
	return nil
}
