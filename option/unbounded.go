package option

import "github.com/sagernet/sing-box/option"

type UnboundedOptions struct {
	WebRTCTag  string `json:"webrtc_tag"` // might need to separate this tag for inbound (peer) and outbound (client)
	Freddie    string `json:"freddie"`
	Netstated  string `json:"netstated"`
	ServerName string `json:"server_name"`
	TLSCert    []byte `json:"tls_cert"`
	TLSKey     []byte `json:"tls_key"`
}

type UnboundedEndpointOptions struct {
	option.DialerOptions
	UnboundedOptions

	// Role determines if this endpoint should run as a unbounded client (as outbound only), or peer (as inbound only), or both
	Role string `json:"role"` //values can only be one of these: client, peer, both
}
