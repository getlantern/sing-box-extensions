package option

import "github.com/sagernet/sing-box/option"

type WATERInboundOptions struct {
	option.ListenOptions
	Transport       string   `json:"transport"`
	WASMAvailableAt []string `json:"wasm_available_at"`
}
