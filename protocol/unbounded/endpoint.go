package unbounded

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"github.com/getlantern/broflake/clientcore"
	"github.com/getlantern/broflake/egress"
	C "github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/google/uuid"
	"github.com/quic-go/quic-go"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
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
	// TODO: find out if we can create just one http client, or we have to create multiple different ones
	outboundHttpClient := &http.Client{
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

	// unbounded client
	bfOptClient := clientcore.NewDefaultBroflakeOptions()
	bfOptClient.Netstated = options.Netstated
	bfOptClient.ClientType = "desktop"
	bfOptClient.NetstateHttpClient = outboundHttpClient
	//bfOptClient.CTableSize = 1 //TODO: revert to default. This is just for testing.
	//bfOptClient.PTableSize = 1

	rtcOptClient := clientcore.NewDefaultWebRTCOptions()
	rtcOptClient.Tag = uuid.New().String()
	rtcOptClient.DiscoverySrv = options.Freddie
	rtcOptClient.Patience = time.Minute
	rtcOptClient.NATFailTimeout = 10 * time.Second
	rtcOptClient.HttpClient = outboundHttpClient

	// unbounded peer
	bfOptPeer := clientcore.NewDefaultBroflakeOptions()
	bfOptPeer.Netstated = options.Netstated
	logger.Info("Running as unbounded peer")
	// unbounded peer (proxy), this ClientType is only used here, which creates WebRTC data tunnels, and routes the traffic to BroflakeConn
	bfOptPeer.ClientType = "singbox-inbound"
	bfOptPeer.NetstateHttpClient = outboundHttpClient
	// TODO: find out why setting these to 5, 5 doesn't work
	bfOptPeer.CTableSize = 1
	bfOptPeer.PTableSize = 1

	rtcOptPeer := clientcore.NewDefaultWebRTCOptions()
	rtcOptPeer.Tag = uuid.New().String()
	rtcOptPeer.DiscoverySrv = options.Freddie
	rtcOptPeer.Patience = time.Minute
	rtcOptPeer.NATFailTimeout = 10 * time.Second
	rtcOptPeer.HttpClient = outboundHttpClient

	// udpConn, err := outboundDialer.ListenPacket(ctx, M.Socksaddr{Addr: netip.IPv4Unspecified()})
	// if err != nil {
	// 	logger.Error("failed to create a UDP conn: ", err)
	// 	return nil, err
	// }
	// //rtcOpt.UDPConn = udpConn

	// egOpt not being used so passing nil
	bfClientConn, _, err := clientcore.NewBroflake(bfOptClient, rtcOptClient, nil)
	if err != nil {
		logger.Error("failed to create unbounded client connection: ", err)
		return nil, err
	}
	bfPeerConn, _, err := clientcore.NewBroflake(bfOptPeer, rtcOptPeer, nil)
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
	l, err := egress.NewListenerFromPacketConn(ctx, bfPeerConn, string(options.TLSCert), string(options.TLSKey), ep.datagramHandler)
	if err != nil {
		return nil, err
	}
	ep.listener = l

	//adapter.NewUpstreamHandlerEx(adapter.InboundContext{}, ep.NewConnectionEx, ep.NewPacketConnectionEx)

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
				u.logger.Info("Listener conn LocalAddr: ", conn.LocalAddr().String(), ", RemoteAddr:", conn.RemoteAddr().String(), ", Destination: ", destination)
				u.newConnectionEx(u.ctx, conn, M.ParseSocksaddr("1.1.1.1:1111"), destination, nil)
			}()
		}
	}()

	return nil
}

func (u *Endpoint) datagramHandler(qconn quic.Connection) {
	// create a new UDP handler that handles incoming packets (as a peer)
	u.logger.Info("datagramHandler running:", qconn.LocalAddr().String())
	handler := NewUDPOverQUICHandler(qconn, u.router, u.logger, u.Tag(), u.Type())
	defer handler.cancel()
	<-qconn.Context().Done()
	u.logger.Info("datagramHandler exited:", qconn.LocalAddr().String())

	// conn := NewQUICDatagramConn(qconn)
	// _ = conn
	// u.logger.Info("datagramHandler running:", conn.LocalAddr().String())
	// for {
	// 	buf := make([]byte, 1500)
	// 	n, addr, err := conn.ReadFrom(buf)
	// 	if err != nil {
	// 		u.logger.Error("ReadFrom error: ", err)
	// 		return
	// 	}
	// 	u.logger.Info("datagramHandler ReadFrom(): ", n, ", addr: ", addr, ", buf: ", string(buf))
	// 	// wrap the []byte to a N.PacketConn TODO: isn't this silly? This must be wrong. Find out a way
	// 	bconn := NewBytePacketConn(buf[:n], conn, addr, u.logger)
	// 	u.newPacketConnectionEx(u.ctx, bconn, M.ParseSocksaddr("1.1.1.1:1111"), M.SocksaddrFromNet(addr), nil)
	// }

	//go u.newPacketConnectionEx(u.ctx, conn, source, destination, nil)
}

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
		u.logger.InfoContext(ctx, "DialContext(): outbound packet connection to ", destination)
	}
	//ctx, metadata := adapter.ExtendContext(ctx)
	// metadata.Outbound = u.Tag()
	// metadata.Destination = destination
	conn, err := u.ql.DialContext(ctx)
	if err != nil {
		u.logger.ErrorContext(ctx, "failed to dial QUIC connection: ", err)
		return nil, err
	}
	M.SocksaddrSerializer.WriteAddrPort(conn, destination)
	return conn, nil
}

func (u *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	u.logger.InfoContext(ctx, "ListenPacket(): outbound packet connection to ", destination)
	qconn, err := u.ql.QUICConn(ctx)
	if err != nil {
		u.logger.ErrorContext(ctx, "failed to obtain QUIC connection: ", err)
		return nil, err
	}
	// Create a local address for this connection
	localAddr := &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	}
	handler := NewUDPOverQUICHandler(qconn, nil, u.logger, u.Tag(), u.Type()) // router specifically set to nil to indicate we are the client
	return handler.NewClientUDPConn(localAddr, destination), nil
}

// func (u *Endpoint) NewPacketEx(buffer *buf.Buffer, source M.Socksaddr) {
// 	u.logger.InfoContext(u.ctx, "NewPacketEx from ", source)
// 	// TODO
// }

// TODO: if we have defined these 2 functions, socks-in will be routed here, so those requests will be treated as inbound connection but we want them to be outbound
/*
INFO[0060] [1166225048 0ms] inbound/socks[socks-in]: inbound connection from 127.0.0.1:50987
INFO[0060] [1166225048 0ms] inbound/socks[socks-in]: inbound connection to incoming.telemetry.mozilla.org:443

DEBUG[0060] [1166225048 1ms] router: match[0] inbound=socks-in => route(unbounded-ep)
INFO[0060] [1166225048 1ms] endpoint/unbounded[unbounded-ep]: inbound connection from 127.0.0.1:50987 to 127.0.0.1:1081
DEBUG[0060] [1166225048 1ms] router: match[1] inbound=unbounded-ep => route(direct)
INFO[0060] [1166225048 1ms] outbound/direct[direct]: outbound connection to 127.0.0.1:1081
*/
// func (u *Endpoint) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
// 	metadata.Inbound = u.Tag()
// 	metadata.InboundType = u.Type()
// 	//metadata.Destination = metadata.OriginDestination
// 	u.logger.InfoContext(ctx, "inbound connection from ", metadata.Source, " to ", metadata.Destination, " original destination:", metadata.OriginDestination)
// 	u.router.RouteConnectionEx(ctx, conn, metadata, onClose)
// }

// func (u *Endpoint) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
// 	metadata.Inbound = u.Tag()
// 	metadata.InboundType = u.Type()
// 	//metadata.Destination = metadata.OriginDestination
// 	conn = metrics.NewPacketConn(conn, &metadata)
// 	u.logger.InfoContext(ctx, "inbound packet connection from ", metadata.Source, " to ", metadata.Destination, " original destination:", metadata.OriginDestination)
// 	u.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
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
