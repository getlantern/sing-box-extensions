package option

import (
	O "github.com/sagernet/sing-box/option"
)

/*
AmneziaOptions provides advanced security options for WireGuard required to activate Amnezia.

In Amnezia, random bytes are appended to every auth packet to alter their size.

Thus, "init and response handshake packets" have added "junk" at the beginning of their data, the size of which
is determined by the values S1 and S2.

By default, the initiating handshake packet has a fixed size (148 bytes). After adding the junk, its size becomes 148 bytes + S1.
Amnezia also incorporates another trick for more reliable masking. Before initiating a session, Amnezia sends a

certain number of "junk" packets to thoroughly confuse DPI systems. The number of these packets and their
minimum and maximum byte sizes can also be adjusted in the settings, using parameters Jc, Jmin, and Jmax.

*/

type AmneziaOptions struct {
	JunkPacketCount            int    `json:"junk_packet_count,omitempty"`             // jc
	JunkPacketMinSize          int    `json:"junk_packet_min_size,omitempty"`          // jmin
	JunkPacketMaxSize          int    `json:"junk_packet_max_size,omitempty"`          // jmax
	InitPacketJunkSize         int    `json:"init_packet_junk_size,omitempty"`         // s1
	ResponsePacketJunkSize     int    `json:"response_packet_junk_size,omitempty"`     // s2
	InitPacketMagicHeader      uint32 `json:"init_packet_magic_header,omitempty"`      // h1
	ResponsePacketMagicHeader  uint32 `json:"response_packet_magic_header,omitempty"`  // h2
	UnderloadPacketMagicHeader uint32 `json:"underload_packet_magic_header,omitempty"` // h3
	TransportPacketMagicHeader uint32 `json:"transport_packet_magic_header,omitempty"` // h4
}

type AmneziaEndpointOptions struct {
	O.WireGuardEndpointOptions
	AmneziaOptions
}
