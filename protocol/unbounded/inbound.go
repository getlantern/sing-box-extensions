package unbounded

// Part two

// this will be the unbounded proxy (uncensored users) that bridges QUIC connections from the desktop to
// the egress server, and potentially we can skip egress server but just send the traffic to the destination instead

// This element builds WebRTC tunnels with the unbounded outbound in radiance (censored peers) and starts a QUIC server that accepts connections from the unbounded outbound
