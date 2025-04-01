package water

import (
	"net"

	M "github.com/sagernet/sing/common/metadata"
)

type WATERConn struct {
	net.Conn
	destination M.Socksaddr
	handshake   bool
}

func NewWATERConnection(conn net.Conn, destination M.Socksaddr) *WATERConn {
	return &WATERConn{
		Conn:        conn,
		destination: destination,
		handshake:   false,
	}
}

func (c *WATERConn) Write(b []byte) (n int, err error) {
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
