package option

import "github.com/sagernet/sing-box/option"

type UnboundedOptions struct {
	// AsPeer determines if this endpoint should also run as a unbounded peer (as a proxy) in addition to being a client
	AsPeer     bool   `json:"as_peer"` //TODO: maybe a better name?
	Freddie    string `json:"freddie"`
	Netstated  string `json:"netstated"`
	WebRTCTag  string `json:"webrtc_tag"`
	ServerName string `json:"server_name"`
	TLSCert    []byte `json:"tls_cert"`
	TLSKey     []byte `json:"tls_key"`
}

type UnboundedInboundOptions struct {
	option.ListenOptions
	UnboundedOptions
}

type UnboundedOutboundOptions struct {
	option.ServerOptions
	option.DialerOptions
	UnboundedOptions
}

type UnboundedEndpointOptions struct {
	option.DialerOptions
	option.ListenOptions
	UnboundedOptions
}
