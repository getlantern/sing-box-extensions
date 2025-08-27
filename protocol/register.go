package protocol

import (
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/include"

	"github.com/getlantern/sing-box-extensions/protocol/amnezia"
	"github.com/getlantern/sing-box-extensions/protocol/group"
	"github.com/getlantern/sing-box-extensions/protocol/water"

	"github.com/getlantern/sing-box-extensions/protocol/algeneva"
	"github.com/getlantern/sing-box-extensions/protocol/outline"
)

var supportedProtocols = []string{
	// custom protocols
	"algeneva",
	"amnezia",
	"outline",
	"selector",

	// sing-box built-in protocols
	"http",
	"hysteria",
	"hysteria2",
	"shadowsocks",
	"shadowtls",
	"socks",
	"ssh",
	"tor",
	"trojan",
	"tuic",
	"vless",
	"vmess",
	"wireguard",
}

func GetRegistries() (*inbound.Registry, *outbound.Registry, *endpoint.Registry) {
	outboundRegistry := include.OutboundRegistry()
	inboundRegistry := include.InboundRegistry()
	endpointRegistry := include.EndpointRegistry()

	registerInbounds(inboundRegistry)
	registerOutbounds(outboundRegistry)
	registerEndpoints(endpointRegistry)

	return inboundRegistry, outboundRegistry, endpointRegistry
}

// ***** REGISTER NEW PROTOCOLS HERE ***** //

func registerInbounds(registry *inbound.Registry) {
	algeneva.RegisterInbound(registry)
	water.RegisterInbound(registry)
}

func registerOutbounds(registry *outbound.Registry) {
	// custom protocol outbounds
	algeneva.RegisterOutbound(registry)
	outline.RegisterOutbound(registry)
	amnezia.RegisterOutbound(registry)
	water.RegisterOutbound(registry)

	// utility outbounds
	group.RegisterFallback(registry)
	group.RegisterSelector(registry)
}

func registerEndpoints(registry *endpoint.Registry) {
	amnezia.RegisterEndpoint(registry)
}

func SupportedProtocols() []string {
	return supportedProtocols
}
