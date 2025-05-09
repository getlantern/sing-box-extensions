package option

import "github.com/sagernet/sing-box/option"

type UnboundedInboundOptions struct {
	option.ListenOptions
	//Transport string `json:"transport"`
	Freddie   string `json:"freddie"`
	Netstated string `json:"netstated"`
	WebRTCTag string `json:"webrtc_tag"`
	TLSCert   []byte `json:"tls_cert"`
	TLSKey    []byte `json:"tls_key"`
	// TODO
}

type UnboundedOutboundOptions struct {
	option.ServerOptions
	option.DialerOptions
	Freddie    string `json:"freddie"`
	Netstated  string `json:"netstated"`
	WebRTCTag  string `json:"webrtc_tag"`
	ServerName string `json:"server_name"`
	TLSCert    []byte `json:"tls_cert"`
	// TODO
}
