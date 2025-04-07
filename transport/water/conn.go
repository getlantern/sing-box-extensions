package water

import (
	"net"
	"sync"

	M "github.com/sagernet/sing/common/metadata"
)

// WATERConn is a connection type that wraps a net.Conn and handles the
// handshake process for the Water protocol. It implements the net.Conn
type WATERConn struct {
	net.Conn
	destination M.Socksaddr
	handshake   bool
	mu          sync.Locker
}

// NewWATERConnection creates a new WATERConn instance with the given
// net.Conn and destination address.
func NewWATERConnection(conn net.Conn, destination M.Socksaddr) *WATERConn {
	return &WATERConn{
		Conn:        conn,
		destination: destination,
		handshake:   false,
		mu:          new(sync.Mutex),
	}
}

// Write sends data to the connection. If the handshake has not
// been completed, it first sends the destination address and port
// before sending the actual data. It locks the connection to ensure
// thread safety during the handshake process.
func (c *WATERConn) Write(b []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handshake {
		return c.Conn.Write(b)
	}
	err = M.SocksaddrSerializer.WriteAddrPort(c.Conn, c.destination)
	if err != nil {
		return
	}

	c.handshake = true
	return c.Conn.Write(b)
}
