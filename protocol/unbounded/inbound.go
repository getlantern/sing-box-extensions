package unbounded

import (
	"context"
	"errors"
	"net"

	"github.com/getlantern/broflake/clientcore"
	"github.com/getlantern/broflake/egress"
	C "github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
)

// this will be the unbounded proxy (uncensored users) that bridges QUIC connections from the desktop to
// the egress server, and potentially we can skip egress server but just send the traffic to the destination instead

// This element builds WebRTC tunnels with the unbounded outbound in radiance (censored peers) and starts a QUIC server that accepts connections from the unbounded outbound

// RegisterInbound registers the unbounded inbound to the registry
// An unbounded inbound starts listening on peer connections (from the unbounded outbound, or from the unbounded desktop for the censored users),
// creating WebRTC data tunnels with just one peer, and then starts a QUIC server that accepts http proxy connections from the peer.
//
// Note that this inbound receives http proxy requests from the peer, so it must be used with "detour" Listen option and route those requests to the sing-box
// http inbound. And then the http inbound can be routed to the direct outbound to proxy http/https traffic.
//
// Proxying flow is like:
// Unbounded desktop (or unbounded outbound) -[http over QUIC over WebRTC data tunnel]-> this inbound -[sing-box detour]-> http inbound (proxy server) ->[sing-box route]-> direct outbound
func RegisterInbound(registry *inbound.Registry) {
	inbound.Register(registry, C.TypeUnbounded, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx      context.Context
	router   adapter.Router
	listener net.Listener
	logger   log.ContextLogger
	cancel   context.CancelFunc
	detour   string
}

// NewInbound creates a new unbounded inbound adapter
func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.UnboundedInboundOptions) (adapter.Inbound, error) {
	// TODO: better way to enforce this?
	if options.Detour == "" {
		logger.Error("detour must be set")
		return nil, errors.New("detour must be set")
	}

	bfOpt := clientcore.NewDefaultBroflakeOptions()
	bfOpt.Netstated = options.Netstated
	// this ClientType is only used here, which creates WebRTC data tunnels, and routes the traffic to BroflakeConn
	bfOpt.ClientType = "singbox-inbound"
	// TODO: find out why setting these to 5, 5 doesn't work
	bfOpt.CTableSize = 1
	bfOpt.PTableSize = 1

	rtcOpt := clientcore.NewDefaultWebRTCOptions()
	rtcOpt.Tag = options.WebRTCTag
	rtcOpt.DiscoverySrv = options.Freddie
	//rtcOpt.HttpClient = //TODO: maybe use kindling

	// egOpt not being used so passing nil. ClientType=="proxy" won't connect to the egress server
	bfconn, _, err := clientcore.NewBroflake(bfOpt, rtcOpt, nil)
	if err != nil {
		return nil, err
	}

	// this creates a net.Listener that accepts QUIC connections over the bfconn, which reads/writes from/to the WebRTC data tunnel
	l, err := egress.NewListenerFromPacketConn(ctx, bfconn, string(options.TLSCert), string(options.TLSKey))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	return &Inbound{
		Adapter:  inbound.NewAdapter(C.TypeUnbounded, tag),
		ctx:      ctx,
		router:   router,
		listener: l,
		logger:   logger,
		cancel:   cancel,
		detour:   options.Detour,
	}, nil
}

func (i *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}

	// start listening on QUIC for the peer connection
	go func() {
		for {
			conn, err := i.listener.Accept()
			if err != nil {
				// Check for shutdown
				select {
				case <-i.ctx.Done():
					return
				default:
					i.logger.Error("accept error: %v", err)
					continue
				}
			}

			// let's detour the traffic to the detour inbound
			go func() {
				var metadata adapter.InboundContext
				metadata.Source = M.SocksaddrFromNet(conn.RemoteAddr()).Unwrap()
				metadata.OriginDestination = M.SocksaddrFromNet(conn.LocalAddr()).Unwrap()
				ctx := log.ContextWithNewID(i.ctx)
				i.detourConnection(ctx, conn, metadata, nil)
			}()
		}
	}()

	return nil
}

func (i *Inbound) detourConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose network.CloseHandlerFunc) {
	i.logger.TraceContext(ctx, "proxy connection. Detouring to ", i.detour)
	// TODO: this is deprecated so find the correct way to do this
	metadata.InboundDetour = i.detour
	// when the above InBoundDetour is set, these are overridden so there is no need to set those fields
	// metadata.Inbound = i.Tag()
	// metadata.InboundType = i.Type()
	i.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (i *Inbound) Close() error {
	// TODO: never tested
	i.cancel()
	return nil
}
