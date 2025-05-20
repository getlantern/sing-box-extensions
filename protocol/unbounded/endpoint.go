package unbounded

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"

	"github.com/getlantern/broflake/clientcore"
	"github.com/getlantern/broflake/egress"
	C "github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterEndpoint(registry *endpoint.Registry) {
	endpoint.Register[option.UnboundedEndpointOptions](registry, C.TypeUnbounded, NewEndpoint)
}

type Endpoint struct {
	endpoint.Adapter
	ctx    context.Context
	router adapter.Router
	logger log.ContextLogger

	// client
	ql          *clientcore.QUICLayer
	uClientConn *clientcore.BroflakeConn
	// server
	uPeerConn *clientcore.BroflakeConn
	listener  net.Listener
}

func NewEndpoint(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.UnboundedEndpointOptions) (adapter.Endpoint, error) {
	outboundDialer, err := dialer.New(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}

	// unbounded client
	bfOptClient := clientcore.NewDefaultBroflakeOptions()
	bfOptClient.Netstated = options.Netstated
	bfOptClient.ClientType = "desktop"

	// unbounded peer
	bfOptPeer := clientcore.NewDefaultBroflakeOptions()
	bfOptPeer.Netstated = options.Netstated
	logger.Info("Running as unbounded peer")
	// unbounded peer (proxy), this ClientType is only used here, which creates WebRTC data tunnels, and routes the traffic to BroflakeConn
	bfOptPeer.ClientType = "singbox-inbound"
	// TODO: find out why setting these to 5, 5 doesn't work
	bfOptPeer.CTableSize = 1
	bfOptPeer.PTableSize = 1

	// common RTC options
	rtcOpt := clientcore.NewDefaultWebRTCOptions()
	rtcOpt.Tag = options.WebRTCTag
	rtcOpt.DiscoverySrv = options.Freddie
	rtcOpt.HttpClient = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				//logger.Debug("RTC http client dialing:", network, address) // TODO: remove
				return outboundDialer.DialContext(ctx, network, M.ParseSocksaddr(address))
			},
			TLSClientConfig: &tls.Config{
				// TODO
				//RootCAs: adapter.RootPoolFromContext(ctx),
			},
		},
	}

	// egOpt not being used so passing nil
	bfClientConn, _, err := clientcore.NewBroflake(bfOptClient, rtcOpt, nil)
	if err != nil {
		logger.Error("failed to create unbounded client connection: ", err)
		return nil, err
	}
	bfPeerConn, _, err := clientcore.NewBroflake(bfOptPeer, rtcOpt, nil)
	if err != nil {
		logger.Error("failed to create unbounded peer connection: ", err)
		return nil, err
	}

	ep := &Endpoint{
		Adapter:     endpoint.NewAdapterWithDialerOptions(C.TypeUnbounded, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		ctx:         ctx,
		router:      router,
		logger:      logger,
		uClientConn: bfClientConn,
		uPeerConn:   bfPeerConn,
	}

	// peer
	// this creates a net.Listener that accepts QUIC connections over the bfconn, which reads/writes from/to the WebRTC data tunnel
	l, err := egress.NewListenerFromPacketConn(ctx, bfPeerConn, string(options.TLSCert), string(options.TLSKey))
	if err != nil {
		return nil, err
	}
	ep.listener = l

	// TODO: not using our listener so this won't work
	// ep.listener = listener.New(listener.Options{
	// 	Context: ctx,
	// 	Logger:  logger,
	// 	Network: []string{N.NetworkTCP, N.NetworkUDP},
	// 	Listen:  options.ListenOptions,
	// 	//ConnectionHandler: ep,
	// 	PacketHandler: ep,
	// })

	// create a QUIC layer (client)
	certPool := x509.NewCertPool()
	insecureSkipVerify := false
	if options.TLSCert != nil {
		certPool.AppendCertsFromPEM([]byte(options.TLSCert))
		insecureSkipVerify = true
	}

	// TODO: maybe use sing-quic instead, or use tuic/packet.go implementation
	ql, err := clientcore.NewQUICLayer(
		bfClientConn,
		&clientcore.QUICLayerOptions{ServerName: options.ServerName, InsecureSkipVerify: insecureSkipVerify, CA: certPool},
	)
	if err != nil {
		logger.Error("failed to create QUIC layer: %v", err)
		return nil, err
	}
	go ql.DialAndMaintainQUICConnection()
	ep.ql = ql

	return ep, nil
}

func (u *Endpoint) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	u.logger.Info("Starting:", stage)

	// start listening on QUIC for the peer connection
	go func() {
		for {
			conn, err := u.listener.Accept()
			if err != nil {
				// Check for shutdown
				select {
				case <-u.ctx.Done():
					return
				default:
					u.logger.Error("accept error: %v", err)
					continue
				}
			}

			go func() {
				destination, err := M.SocksaddrSerializer.ReadAddrPort(conn)
				if err != nil {
					u.logger.Error("read destination error: ", err)
					return
				}
				u.logger.Info("Listener conn LocalAddr: ", conn.LocalAddr().String(), ", Remote:", destination)
				u.newConnectionEx(u.ctx, conn, M.ParseSocksaddr("1.1.1.1:1111"), destination, nil)
			}()
		}
	}()

	// TODO: find a way to listen on packets here and call newPacketConnectionEx
	// go func() {
	// 	for {
	// 		u.listener
	// 	}
	// }

	return nil
}

// var _ N.PacketConn = (*packetConnWrapper)(nil)

// type packetConnWrapper struct {
// 	net.PacketConn
// }

// func (c *packetConnWrapper) ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error) {
// 	return p.PacketConn.ReadFrom(b)
// }

// func (c *packetConnWrapper) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
// }

// func (u *Endpoint) DatagramHandler(pconn net.PacketConn) {
// 	buf := make([]byte, 65535)
// 	for {
// 		_, addr, err := pconn.ReadFrom(buf)
// 		if err != nil {
// 			break
// 		}
// 		source := M.SocksaddrFromNet(pconn.LocalAddr())
// 		destination := M.SocksaddrFromNet(addr)

// 		go u.newPacketConnectionEx(u.ctx, &packetConnWrapper{pconn}, source, destination, nil)
// 	}
// }

func (u *Endpoint) Close() error {
	u.logger.Info("Close()")
	// TODO
	return nil
}

func (u *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch network {
	case N.NetworkTCP:
		u.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		// TODO: find out how this can work
		u.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	//ctx, metadata := adapter.ExtendContext(ctx)
	// metadata.Outbound = u.Tag()
	// metadata.Destination = destination
	conn, err := u.ql.DialContext(ctx)
	if err != nil {
		u.logger.ErrorContext(ctx, "failed to dial QUIC connection: %v", err)
		return nil, err
	}
	M.SocksaddrSerializer.WriteAddrPort(conn, destination)
	return conn, nil
}

func (u *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	u.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	pconn, err := u.ql.DialUDP(ctx)
	if err != nil {
		u.logger.ErrorContext(ctx, "failed to obtain QUIC PacketConn: %v", err)
		return nil, err
	}
	return pconn, nil
}

func (u *Endpoint) NewPacketEx(buffer *buf.Buffer, source M.Socksaddr) {
	u.logger.InfoContext(u.ctx, "NewPacketEx from ", source)
	// TODO
}

// func (u *Endpoint) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
// 	metadata.Inbound = u.Tag()
// 	metadata.InboundType = u.Type()
// 	metadata.Destination = metadata.OriginDestination
// 	u.logger.InfoContext(ctx, "inbound connection from ", metadata.Source, " to ", metadata.Destination)
// 	// u.router.RouteConnectionEx(ctx, conn, metadata, onClose)
// }

// func (u *Endpoint) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
// 	metadata.Inbound = u.Tag()
// 	metadata.InboundType = u.Type()
// 	metadata.InboundType = u.Type()
// 	metadata.Destination = metadata.OriginDestination
// 	conn = metrics.NewPacketConn(conn, &metadata)
// 	u.logger.InfoContext(ctx, "inbound packet connection from ", metadata.Source, " to ", metadata.Destination)
// 	// u.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
// }

func (u *Endpoint) newConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = u.Tag()
	metadata.InboundType = u.Type()
	metadata.Source = source
	metadata.Destination = destination
	u.logger.InfoContext(ctx, "inbound connection from ", source, " to ", destination)
	u.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (u *Endpoint) newPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = u.Tag()
	metadata.InboundType = u.Type()
	metadata.Source = source
	metadata.Destination = destination
	u.logger.InfoContext(ctx, "inbound packet connection from ", source, " to ", destination)
	u.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}
