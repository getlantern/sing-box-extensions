package water

import (
	"net"
	"sync"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
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

func (c *WATERConn) handshaked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handshake
}

func (c *WATERConn) Write(b []byte) (n int, err error) {
	if c.handshaked() {
		return c.Conn.Write(b)
	}
	err = M.SocksaddrSerializer.WriteAddrPort(c.Conn, c.destination)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.handshake = true
	return c.Conn.Write(b)
}

func NewWATERPacketConn(conn net.Conn) *waterPacketConn {
	return &waterPacketConn{Conn: conn}
}

type waterPacketConn struct {
	net.Conn
}

func (c *waterPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	_, err := buffer.ReadOnceFrom(c)
	if err != nil {
		return M.Socksaddr{}, err
	}
	return M.SocksaddrSerializer.ReadAddrPort(buffer)
}

func (c *waterPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	header := buf.With(buffer.ExtendHeader(M.SocksaddrSerializer.AddrPortLen(destination)))
	err := M.SocksaddrSerializer.WriteAddrPort(header, destination)
	if err != nil {
		return err
	}
	return common.Error(buffer.WriteTo(c))
}

func (c *waterPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, err = c.Read(p)
	if err != nil {
		return
	}
	buffer := buf.With(p[:n])
	destination, err := M.SocksaddrSerializer.ReadAddrPort(buffer)
	if err != nil {
		return
	}
	if destination.IsFqdn() {
		addr = destination
	} else {
		addr = destination.UDPAddr()
	}
	n = copy(p, buffer.Bytes())
	return
}

func (c *waterPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	destination := M.SocksaddrFromNet(addr)
	buffer := buf.NewSize(M.SocksaddrSerializer.AddrPortLen(destination) + len(p))
	defer buffer.Release()
	err = M.SocksaddrSerializer.WriteAddrPort(buffer, destination)
	if err != nil {
		return
	}
	_, err = buffer.Write(p)
	if err != nil {
		return
	}
	return len(p), nil
}
