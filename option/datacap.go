package option

import "github.com/sagernet/sing-box/option"

// DataCapInboundOptions specifies the configuration for datacap-wrapped inbound.
// This wraps another inbound and adds datacap tracking functionality.
type DataCapInboundOptions struct {
	option.ListenOptions

	// Tag is the tag for the datacap inbound
	Tag string `json:"tag"`

	// WrappedInbound specifies which inbound to wrap with datacap functionality
	WrappedInbound string `json:"wrapped_inbound"`

	// SidecarURL is the base URL of the datacap sidecar service
	// Example: "http://localhost:8080"
	SidecarURL string `json:"sidecar_url"`

	// ReportInterval specifies how often to report data consumption to the sidecar
	// Default: "30s"
	ReportInterval string `json:"report_interval,omitempty"`

	// HTTPTimeout specifies the timeout for HTTP requests to the sidecar
	// Default: "10s"
	HTTPTimeout string `json:"http_timeout,omitempty"`

	// DeviceIDHeader specifies the HTTP header name that contains the device ID
	// Default: "X-Device-ID"
	DeviceIDHeader string `json:"device_id_header,omitempty"`

	// CountryCodeHeader specifies the HTTP header name that contains the country code
	// Default: "X-Country-Code"
	CountryCodeHeader string `json:"country_code_header,omitempty"`

	// PlatformHeader specifies the HTTP header name that contains the device platform
	// Default: "X-Platform"
	PlatformHeader string `json:"platform_header,omitempty"`

	// EnableThrottling enables automatic throttling based on datacap status
	// Default: false
	EnableThrottling bool `json:"enable_throttling,omitempty"`

	// StatusCheckInterval specifies how often to check datacap status for throttling
	// Default: "60s"
	StatusCheckInterval string `json:"status_check_interval,omitempty"`
}
