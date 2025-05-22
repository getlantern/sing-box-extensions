package unbounded

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// TODO: add webt.DatagramChunker to fragment large payload
// QUICDatagramConn wraps QUIC datagram APIs into a net.PacketConn
type QUICDatagramConn struct {
	conn   quic.Connection
	closed bool
	mu     sync.Mutex

	readDeadline   time.Time
	writeDeadline  time.Time
	readBufferSize int
}

var _ N.PacketConn = (*QUICDatagramConn)(nil)
var _ net.PacketConn = (*QUICDatagramConn)(nil)

// ReadFrom implements net.PacketConn ReadFrom method
func (c *QUICDatagramConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	if c.closed {
		return 0, nil, net.ErrClosed
	}

	ctx := context.Background()
	if !c.readDeadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.readDeadline)
		defer cancel()
	}

	// read datagram
	datagram, err := c.conn.ReceiveDatagram(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, nil, errors.New("read deadline exceeded")
		}
		return 0, nil, err
	}

	// read addr and port
	bufferReader := bytes.NewReader(datagram)
	destination, err := M.SocksaddrSerializer.ReadAddrPort(bufferReader)
	if err != nil {
		return 0, nil, err
	}
	// copy the rest to p
	dataStart := len(datagram) - bufferReader.Len()
	n = copy(p, datagram[dataStart:])

	// create a UDP address from the parsed address and port
	remoteAddr := &net.UDPAddr{
		IP:   destination.Addr.AsSlice(),
		Port: int(destination.Port),
	}

	return n, remoteAddr, nil
}

// WriteTo implements net.PacketConn WriteTo method
func (c *QUICDatagramConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if c.closed {
		return 0, net.ErrClosed
	}
	ctx := context.Background()
	if !c.writeDeadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.writeDeadline)
		defer cancel()
	}

	// write addr and port
	destination := M.SocksaddrFromNet(addr)
	bufferWriter := bytes.NewBuffer(nil)
	if err := M.SocksaddrSerializer.WriteAddrPort(bufferWriter, destination); err != nil {
		return 0, err
	}
	// and data
	bufferWriter.Write(p)

	if err := c.conn.SendDatagram(bufferWriter.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close implements net.PacketConn Close method
func (c *QUICDatagramConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return nil
}

// LocalAddr implements net.PacketConn LocalAddr method
func (c *QUICDatagramConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// SetDeadline implements net.PacketConn SetDeadline method
func (c *QUICDatagramConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

// SetReadDeadline implements net.PacketConn SetReadDeadline method
func (c *QUICDatagramConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.readDeadline = t
	return nil
}

// SetWriteDeadline implements net.PacketConn SetWriteDeadline method
func (c *QUICDatagramConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writeDeadline = t
	return nil
}

// ReadPacket implements sing's N.PacketConn ReadPacket method
func (c *QUICDatagramConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	ctx := context.Background()
	if !c.readDeadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.readDeadline)
		defer cancel()
	}

	datagram, err := c.conn.ReceiveDatagram(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return M.Socksaddr{}, errors.New("read deadline exceeded")
		}
		return M.Socksaddr{}, err
	}

	bufferReader := bytes.NewReader(datagram)
	destination, err := M.SocksaddrSerializer.ReadAddrPort(bufferReader)
	if err != nil {
		return M.Socksaddr{}, err
	}

	// write the data to buffer
	dataStart := len(datagram) - bufferReader.Len()
	_, err = buffer.Write(datagram[dataStart:])
	if err != nil {
		return M.Socksaddr{}, err
	}

	return destination, nil
}

// WritePacket implements sing's N.PacketConn WritePacket method
func (c *QUICDatagramConn) WritePacket(buffer *buf.Buffer, addr M.Socksaddr) error {
	ctx := context.Background()
	if !c.writeDeadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.writeDeadline)
		defer cancel()
	}

	bufferWriter := bytes.NewBuffer(nil)
	if err := M.SocksaddrSerializer.WriteAddrPort(bufferWriter, addr); err != nil {
		return err
	}
	bufferWriter.Write(buffer.Bytes())

	if err := c.conn.SendDatagram(bufferWriter.Bytes()); err != nil {
		return err
	}

	return nil
}

// // NewQUICDatagramConn creates a new QUICDatagramConn from a QUIC connection
func NewQUICDatagramConn(conn quic.Connection) *QUICDatagramConn {
	return &QUICDatagramConn{
		conn:           conn,
		readBufferSize: 1500, // default MTU size, can be adjusted
	}
}

// BytePacketConn implements N.PacketConn interface for raw byte data
type BytePacketConn struct {
	buffer       []byte
	qdconn       *QUICDatagramConn
	localAddr    net.Addr
	readDeadline time.Time
	closed       bool
	closeMutex   sync.Mutex
	readCh       chan packetData
	closeCh      chan struct{}
	logger       log.ContextLogger
}

var _ N.PacketConn = (*BytePacketConn)(nil)

type packetData struct {
	data []byte
	addr net.Addr
}

// NewBytePacketConn creates a new PacketConn from raw byte data
func NewBytePacketConn(initialData []byte, qdconn *QUICDatagramConn, destination net.Addr, logger log.ContextLogger) *BytePacketConn {
	conn := &BytePacketConn{
		buffer:    initialData,
		qdconn:    qdconn,
		localAddr: qdconn.LocalAddr(),
		readCh:    make(chan packetData, 1),
		closeCh:   make(chan struct{}),
		logger:    logger,
	}

	// Put initial data in the channel if provided
	if len(initialData) > 0 {
		conn.readCh <- packetData{
			data: initialData,
			addr: destination,
		}
	}

	return conn
}

// ReadFrom implements N.PacketConn
func (c *BytePacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	select {
	case <-c.closeCh:
		return 0, nil, net.ErrClosed
	case packet := <-c.readCh:
		n = copy(p, packet.data)
		return n, packet.addr, nil
	case <-func() <-chan time.Time {
		if c.readDeadline.IsZero() {
			return make(chan time.Time)
		}
		now := time.Now()
		if now.After(c.readDeadline) {
			return nil // Return immediately if past deadline
		}
		timer := time.NewTimer(c.readDeadline.Sub(now))
		return timer.C
	}():
		return 0, nil, context.DeadlineExceeded
	}
}

// WriteTo implements N.PacketConn
func (c *BytePacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// Implementation depends on how you want to handle outgoing data
	// For example, you might want to callback to some handler
	c.logger.Info("WriteTo called for BytePacketConn with addr:", addr, "and data:", string(p))
	return c.qdconn.WriteTo(p, addr)
}

func (c *BytePacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	select {
	case <-c.closeCh:
		return M.Socksaddr{}, net.ErrClosed
	case packet := <-c.readCh:
		buffer.Write(packet.data)
		return M.SocksaddrFromNet(packet.addr), nil
	case <-func() <-chan time.Time {
		if c.readDeadline.IsZero() {
			return make(chan time.Time)
		}
		now := time.Now()
		if now.After(c.readDeadline) {
			return nil // Return immediately if past deadline
		}
		timer := time.NewTimer(c.readDeadline.Sub(now))
		return timer.C
	}():
		return M.Socksaddr{}, context.DeadlineExceeded
	}
}

func (c *BytePacketConn) WritePacket(buffer *buf.Buffer, addr M.Socksaddr) error {
	c.logger.Info("WritePacket called for BytePacketConn with addr:", addr, "and data:", string(buffer.Bytes()))
	return c.qdconn.WritePacket(buffer, addr)
}

// Close implements N.PacketConn
func (c *BytePacketConn) Close() error {
	c.closeMutex.Lock()
	defer c.closeMutex.Unlock()

	if c.closed {
		return net.ErrClosed
	}
	c.closed = true
	close(c.closeCh)
	return nil
}

// LocalAddr implements N.PacketConn
func (c *BytePacketConn) LocalAddr() net.Addr {
	return c.localAddr
}

// SetDeadline implements N.PacketConn
func (c *BytePacketConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return nil
}

// SetReadDeadline implements N.PacketConn
func (c *BytePacketConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

// SetWriteDeadline implements N.PacketConn
func (c *BytePacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}
