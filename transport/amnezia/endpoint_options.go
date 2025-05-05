package amnezia

import (
	"github.com/getlantern/sing-box-extensions/option"

	"github.com/sagernet/sing-box/transport/wireguard"
)

type EndpointOptions struct {
	// Context                                 context.Context
	// Logger                                  logger.ContextLogger
	// System                                  bool
	// Handler                                 tun.Handler
	// UDPTimeout                              time.Duration
	// Dialer                                  N.Dialer
	// CreateDialer                            func(interfaceName string) N.Dialer
	// Name                                    string
	// MTU                                     uint32
	// Address                                 []netip.Prefix
	// PrivateKey                              string
	// ListenPort                              uint16
	// ResolvePeer                             func(domain string) (netip.Addr, error)
	// Peers                                   []PeerOptions
	// Workers                                 int
	wireguard.EndpointOptions
	option.WireGuardAdvancedSecurityOptions /**  ADDED FOR AMNEZIA  **/
}

// type PeerOptions struct {
// 	Endpoint                    M.Socksaddr
// 	PublicKey                   string
// 	PreSharedKey                string
// 	AllowedIPs                  []netip.Prefix
// 	PersistentKeepaliveInterval uint16
// 	Reserved                    []uint8
// }
