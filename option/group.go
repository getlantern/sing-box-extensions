package option

type FallBackOutboundOptions struct {
	// Primary and Fallback are the tags of the primary and fallback outbounds.
	Primary  string `json:"main,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}
