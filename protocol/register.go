package protocol

import (
	"github.com/getlantern/sing-box-extensions/protocol/amnezia"
	"github.com/getlantern/sing-box-extensions/protocol/water"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/include"

	"github.com/getlantern/sing-box-extensions/protocol/algeneva"
	"github.com/getlantern/sing-box-extensions/protocol/outline"
)

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
	algeneva.RegisterOutbound(registry)
	outline.RegisterOutbound(registry)
	amnezia.RegisterOutbound(registry)
	water.RegisterOutbound(registry)
}

func registerEndpoints(registry *endpoint.Registry) {
	amnezia.RegisterEndpoint(registry)
}
