package option

import "github.com/sagernet/sing-box/option"

type ALGenevaInboundOptions struct {
	option.HTTPMixedInboundOptions
}

type ALGenevaOutboundOptions struct {
	option.HTTPOutboundOptions
	Strategy string `json:"strategy,omitempty"`
}
