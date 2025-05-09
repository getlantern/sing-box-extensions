package unbounded

import (
	"context"
	"crypto/x509"
	"net"
	"os"

	"github.com/getlantern/broflake/clientcore"
	C "github.com/getlantern/sing-box-extensions/constant"
	"github.com/getlantern/sing-box-extensions/option"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"
)

// Outbound implements the unbounded outbound that initiate QUIC connection to the
// QUIC server in inbound.go through WebRTC data tunnels. It also can be used to dial
// to the existing ubounded network to proxy http/https traffic.
type Outbound struct {
	outbound.Adapter
	logger log.ContextLogger
	bfConn *clientcore.BroflakeConn
	ql     *clientcore.QUICLayer
}

// RegisterOutbound registers the unbounded outbound to the registry
func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register(registry, C.TypeUnbounded, NewOutbound)
}

// NewOutbound creates a unbounded outbound that uses the unbounded network
func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.UnboundedOutboundOptions) (adapter.Outbound, error) {
	bfOpt := clientcore.NewDefaultBroflakeOptions()
	bfOpt.Netstated = options.Netstated
	bfOpt.ClientType = "desktop"

	rtcOpt := clientcore.NewDefaultWebRTCOptions()
	rtcOpt.Tag = options.WebRTCTag
	rtcOpt.DiscoverySrv = options.Freddie
	//rtcOpt.HttpClient = //TODO: maybe use kindling

	// egOpt not being used so passing nil
	bfconn, _, err := clientcore.NewBroflake(bfOpt, rtcOpt, nil)
	if err != nil {
		logger.Error("failed to create unbounded connection: %v", err)
		return nil, err
	}

	// create a QUIC layer
	certPool := x509.NewCertPool()
	insecureSkipVerify := false
	if options.TLSCert != nil {
		certPool.AppendCertsFromPEM([]byte(options.TLSCert))
		insecureSkipVerify = true
	}
	ql, err := clientcore.NewQUICLayer(
		bfconn,
		&clientcore.QUICLayerOptions{ServerName: options.ServerName, InsecureSkipVerify: insecureSkipVerify, CA: certPool},
	)
	if err != nil {
		logger.Error("failed to create QUIC layer: %v", err)
		return nil, err
	}
	go ql.DialAndMaintainQUICConnection()

	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeUnbounded, tag, []string{network.NetworkTCP}, options.DialerOptions),
		logger:  logger,
		bfConn:  bfconn,
		ql:      ql,
	}, nil
}

// DialContext calls the underlying QUIC layer and dial through the QUIC connection to the unbounded network
func (o *Outbound) DialContext(ctx context.Context, network string, destination metadata.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination
	conn, err := o.ql.DialContext(ctx)
	if err != nil {
		o.logger.ErrorContext(ctx, "failed to dial QUIC connection: %v", err)
		return nil, err
	}
	return conn, nil
}

// ListenPacket isn't implemented
func (o *Outbound) ListenPacket(ctx context.Context, destination metadata.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}
