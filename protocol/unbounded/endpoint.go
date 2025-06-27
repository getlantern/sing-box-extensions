package unbounded

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
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

	// for WebRTC
	outboundPacketConn net.PacketConn
	// for signaling server and netstatd
	outboundHttpClient *http.Client

	// client
	clientTag        string
	clientBf         *clientcore.BroflakeEngine
	ql               *clientcore.QUICLayer
	udpClientHandler *UDPOverQUICHandler
	udpMutex         sync.RWMutex // for udpClientHandler

	// peer/server
	peerTag          string
	peerBf           *clientcore.BroflakeEngine
	listener         net.Listener
	udpServerHandler *UDPOverQUICHandler
}

func (u *Endpoint) createClientOptions(options option.UnboundedEndpointOptions) (*clientcore.BroflakeOptions, *clientcore.WebRTCOptions, *clientcore.QUICLayerOptions) {
	// unbounded client
	bfOptClient := clientcore.NewDefaultBroflakeOptions()
	bfOptClient.Netstated = options.Netstated
	bfOptClient.ClientType = "desktop"
	bfOptClient.NetstateHttpClient = u.outboundHttpClient
	bfOptClient.CTableSize = 1 // not used, always 1
	bfOptClient.PTableSize = 1 // WebRTC channel

	rtcOptClient := clientcore.NewDefaultWebRTCOptions()
	rtcOptClient.Tag = uuid.New().String()
	rtcOptClient.DiscoverySrv = options.Freddie
	rtcOptClient.Patience = time.Minute
	rtcOptClient.NATFailTimeout = 10 * time.Second
	rtcOptClient.UDPConn = u.outboundPacketConn
	rtcOptClient.HttpClient = u.outboundHttpClient

	// create a QUIC layer options for the client
	certPool := x509.NewCertPool()
	insecureSkipVerify := false
	if options.TLSCert != nil {
		certPool.AppendCertsFromPEM([]byte(options.TLSCert))
		insecureSkipVerify = true
	}

	qlOptions := &clientcore.QUICLayerOptions{
		ServerName:         options.ServerName,
		InsecureSkipVerify: insecureSkipVerify,
		CA:                 certPool,
	}

	return bfOptClient, rtcOptClient, qlOptions
}

func (u *Endpoint) createPeerOptions(options option.UnboundedEndpointOptions) (*clientcore.BroflakeOptions, *clientcore.WebRTCOptions) {
	bfOptPeer := clientcore.NewDefaultBroflakeOptions()
	bfOptPeer.Netstated = options.Netstated
	// unbounded peer (proxy), this ClientType is only used here, which creates WebRTC data tunnels, and routes the traffic to BroflakeConn
	bfOptPeer.ClientType = "singbox-inbound"
	bfOptPeer.NetstateHttpClient = u.outboundHttpClient
	// TODO: setting these otherwise won't work
	bfOptPeer.CTableSize = 1 // WebRTC channel
	bfOptPeer.PTableSize = 1 // not used, always 1

	rtcOptPeer := clientcore.NewDefaultWebRTCOptions()
	rtcOptPeer.Tag = uuid.New().String()
	rtcOptPeer.DiscoverySrv = options.Freddie
	rtcOptPeer.Patience = time.Minute
	rtcOptPeer.NATFailTimeout = 10 * time.Second
	rtcOptPeer.UDPConn = u.outboundPacketConn
	rtcOptPeer.HttpClient = u.outboundHttpClient

	return bfOptPeer, rtcOptPeer
}

func (u *Endpoint) createClient(options option.UnboundedEndpointOptions) error {
	bfOptClient, rtcOptClient, qlOpt := u.createClientOptions(options)
	u.clientTag = rtcOptClient.Tag

	// egOpt not being used so passing nil
	bfClientConn, ui, err := clientcore.NewBroflake(bfOptClient, rtcOptClient, nil)
	if err != nil {
		u.logger.Error("failed to create unbounded client connection: ", err)
		return err
	}
	// TODO: maybe use sing-quic instead, or use tuic/packet.go implementation
	ql, err := clientcore.NewQUICLayer(bfClientConn, qlOpt)
	if err != nil {
		u.logger.Error("failed to create QUIC layer: ", err)
		return err
	}
	connReady := make(chan quic.Connection)
	go ql.DialAndMaintainQUICConnection(connReady)
	u.clientBf = ui.BroflakeEngine
	u.ql = ql
	u.logger.Info("Client created with tag:", u.clientTag)

	// setup UDP over QUIC handler
	go func() {
		select {
		case <-u.ctx.Done():
			return
		case qconn := <-connReady:
			u.udpMutex.RLock()
			if u.udpClientHandler != nil {
				u.udpClientHandler.cancel()
			}
			u.udpMutex.RUnlock()
			// router specifically set to nil to indicate we are the client
			u.udpMutex.Lock()
			u.udpClientHandler = NewUDPOverQUICHandler(qconn, nil, u.logger, u.Tag(), u.Type())
			u.udpMutex.Unlock()
			u.logger.Info("UDP Handler ready")
		}
	}()
	return nil
}

func (u *Endpoint) createPeer(options option.UnboundedEndpointOptions) error {
	bfOptPeer, rtcOptPeer := u.createPeerOptions(options)
	u.peerTag = rtcOptPeer.Tag

	bfPeerConn, ui, err := clientcore.NewBroflake(bfOptPeer, rtcOptPeer, nil)
	if err != nil {
		u.logger.Error("failed to create unbounded peer connection: ", err)
		return err
	}
	// this creates a net.Listener that accepts QUIC connections over the bfconn, which reads/writes from/to the WebRTC data tunnel
	l, err := egress.NewListenerFromPacketConn(u.ctx, bfPeerConn, string(options.TLSCert), string(options.TLSKey), u.datagramHandler)
	if err != nil {
		return err
	}
	u.peerBf = ui.BroflakeEngine
	u.listener = l
	u.logger.Info("Peer created with tag:", u.peerTag)

	return nil
}

func NewEndpoint(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.UnboundedEndpointOptions) (adapter.Endpoint, error) {
	outboundDialer, err := dialer.New(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}
	pconn, err := outboundDialer.ListenPacket(ctx, M.Socksaddr{})
	if err != nil {
		return nil, err
	}

	ep := &Endpoint{
		Adapter: endpoint.NewAdapterWithDialerOptions(C.TypeUnbounded, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		ctx:     ctx,
		router:  router,
		logger:  logger,

		outboundPacketConn: pconn,
		outboundHttpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return outboundDialer.DialContext(ctx, network, M.ParseSocksaddr(address))
				},
				TLSClientConfig: &tls.Config{
					// TODO: to be added for real world use
					//RootCAs: adapter.RootPoolFromContext(ctx),
				},
			},
		},
	}

	switch options.Role {
	case "client":
		if err := ep.createClient(options); err != nil {
			logger.Error("failed to create endpoint as a client: ", err)
			return nil, err
		}
	case "peer":
		if err := ep.createPeer(options); err != nil {
			logger.Error("failed to create endpoint as a peer: ", err)
			return nil, err
		}
	default:
		if err := ep.createClient(options); err != nil {
			logger.Error("failed to create endpoint as a client (Role: both): ", err)
			return nil, err
		}
		if err := ep.createPeer(options); err != nil {
			logger.Error("failed to create Role as a peer (Role: both): ", err)
			return nil, err
		}
	}
	return ep, nil
}

func (u *Endpoint) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart || u.listener == nil {
		return nil
	}
	u.logger.Info("Starting unbounded listener")

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
					u.logger.Error("accept error: ", err)
					return
				}
			}

			go func() {
				destination, err := M.SocksaddrSerializer.ReadAddrPort(conn)
				if err != nil {
					u.logger.Error("read destination error: ", err)
					return
				}
				u.logger.Info("Listener conn LocalAddr: ", conn.LocalAddr().String(), ", RemoteAddr:", conn.RemoteAddr().String(), ", Destination: ", destination)
				// the source address doesn't matter so just put a random one here
				u.newConnectionEx(u.ctx, conn, M.ParseSocksaddr("1.1.1.1:1111"), destination, func(err error) {
					u.logger.Info("Listener conn closed: ", conn.LocalAddr().String(), ", RemoteAddr:", conn.RemoteAddr().String(), ", Destination: ", destination, ", Error: ", err)
					conn.Close()
				})
			}()
		}
	}()

	return nil
}

func (u *Endpoint) datagramHandler(qconn quic.Connection) {
	// create a new UDP handler that handles incoming packets (as a peer)
	u.logger.Debug("datagramHandler running:", qconn.LocalAddr().String())
	// client will only re-dial when the original quic.Connection is broken, so let's just create a new handler
	u.udpServerHandler = NewUDPOverQUICHandler(qconn, u.router, u.logger, u.Tag(), u.Type())
	defer u.udpServerHandler.cancel()
	// block until the QUIC connection is closed or the context is done
	select {
	case <-u.ctx.Done():
	case <-qconn.Context().Done():
	}
	u.logger.Debug("datagramHandler exited:", qconn.LocalAddr().String())
}

func (u *Endpoint) Close() error {
	u.logger.Debug("Closing...")
	// stop server/client UDP handler
	if u.udpClientHandler != nil {
		u.udpClientHandler.cancel()
	}
	if u.udpServerHandler != nil {
		u.udpServerHandler.cancel()
	}
	// stop peer QUIC listener
	if u.listener != nil {
		u.listener.Close()
	}
	// stop client QUIC dialer
	if u.ql != nil {
		u.ql.Close()
	}
	// signal closure of broflake engines
	var clientClosed <-chan struct{}
	if u.clientBf != nil {
		clientClosed = u.clientBf.Close()
	}
	var peerClosed <-chan struct{}
	if u.peerBf != nil {
		peerClosed = u.peerBf.Close()
	}

	// wait for engines to close
	if u.clientBf != nil {
		<-clientClosed
		u.logger.Info("Unbounded client closed")
	}
	if u.peerBf != nil {
		<-peerClosed
		u.logger.Info("Unbounded peer closed")
	}

	return nil
}

func (u *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if u.ql == nil {
		return nil, errors.New("endpoint acts as an unbounded peer, cannot dial outbound")
	}
	switch network {
	case N.NetworkTCP:
		u.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		u.logger.InfoContext(ctx, "DialContext(): outbound packet connection to ", destination)
	}

	conn, err := u.ql.DialContext(ctx)
	if err != nil {
		u.logger.ErrorContext(ctx, "failed to dial QUIC connection: ", err)
		return nil, err
	}
	// TODO: this agressive cleanup (timeout) for stuck streams are currently needed.
	// It looks like the conn being returned here isn't closed properly in singbox
	// which leads to quic streams being stuck maybe due to flow control or resource limits
	// try with speed test site. It quickly gets stuck and TCP can go through
	// (it goes to the peer, but never comes back)
	// It's worth noting that while the TCP is dead, UDP still works, meaning the underlying quic.Connection
	// and webRTC all works fine so this suggests a stream-level resource exhaustion issue.
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	M.SocksaddrSerializer.WriteAddrPort(conn, destination)
	return conn, nil
}

func (u *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if u.ql == nil {
		return nil, errors.New("endpoint acts as an unbounded peer, cannot dial packet outbound")
	}
	u.logger.InfoContext(ctx, "ListenPacket(): outbound packet connection to ", destination)

	// Create a local address for this connection
	localAddr := &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	}
	u.udpMutex.RLock()
	handler := u.udpClientHandler
	u.udpMutex.RUnlock()
	if handler == nil {
		return nil, errors.New("unable to create outbound packet connection: no UDP handler")
	}
	return handler.NewClientUDPConn(localAddr, destination), nil
}

func (u *Endpoint) newConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = u.Tag()
	metadata.InboundType = u.Type()
	metadata.Source = source
	metadata.Destination = destination
	u.logger.InfoContext(ctx, "inbound connection from ", source, " to ", destination)
	u.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}
