package option

type FallbackOutboundOptions struct {
	// Primary and Fallback are the tags of the primary and fallback outbounds.
	Primary  string `json:"primary,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}
