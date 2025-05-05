package amnezia

import (
	"github.com/getlantern/sing-box-extensions/option"

	"github.com/sagernet/sing-box/transport/wireguard"
)

type EndpointOptions struct {
	wireguard.EndpointOptions
	option.AmneziaOptions
}
