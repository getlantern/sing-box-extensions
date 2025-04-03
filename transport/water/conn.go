package water

import (
	"net"
	"sync"

	M "github.com/sagernet/sing/common/metadata"
)

type WATERConn struct {
	net.Conn
	destination M.Socksaddr
	handshake   bool
	mu          sync.Locker
}

func NewWATERConnection(conn net.Conn, destination M.Socksaddr) *WATERConn {
	return &WATERConn{
		Conn:        conn,
		destination: destination,
		handshake:   false,
		mu:          new(sync.Mutex),
	}
}

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
