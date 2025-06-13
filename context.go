package sbx

import (
	"context"

	box "github.com/sagernet/sing-box"

	"github.com/getlantern/sing-box-extensions/protocol"
)

// BoxContext returns a box context with the registered inbound, outbound, and endpoint protocols.
func BoxContext() context.Context {
	in, out, ep := protocol.GetRegistries()
	return box.Context(context.Background(), in, out, ep)
}
