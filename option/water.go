package option

import "github.com/sagernet/sing-box/option"

type WATERInboundOptions struct {
	option.ListenOptions
	Multiplex       *option.InboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport       string                          `json:"transport"`
	WASMAvailableAt []string                        `json:"wasm_available_at"`
}

type WATEROutboundOptions struct {
	option.ServerOptions
	option.DialerOptions
	Transport       string                           `json:"transport"`
	WASMAvailableAt []string                         `json:"wasm_available_at"`
	DownloadTimeout string                           `json:"download_timeout"`
	Dir             string                           `json:"water_dir"`
	Multiplex       *option.OutboundMultiplexOptions `json:"multiplex,omitempty"`
}
