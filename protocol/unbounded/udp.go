package unbounded

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// UDPOverQUICHandler handles UDP packets sent over QUIC datagrams
type UDPOverQUICHandler struct {
	quicConn       quic.Connection
	sessions       map[string]*UDPSession
	sessionsMux    sync.RWMutex
	clientConns    map[string]*ClientUDPConn // Map to route responses back to clients
	clientConnsMux sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc

	router      adapter.Router
	logger      logger.ContextLogger
	inboundTag  string
	inboundType string
}

// UDPSession represents a UDP flow between source and destination
type UDPSession struct {
	sessionKey string
	sourceAddr net.Addr
	destAddr   net.Addr
	conn       *UDPSessionConn
	lastActive time.Time
	cancel     context.CancelFunc
}

// UDPSessionConn implements N.PacketConn for a specific UDP session
type UDPSessionConn struct {
	session       *UDPSession
	handler       *UDPOverQUICHandler
	readCh        chan *UDPPacket
	writeCh       chan *UDPPacket
	closeOnce     sync.Once
	closed        chan struct{}
	readDeadline  time.Time
	writeDeadline time.Time
}

var _ N.PacketConn = (*UDPSessionConn)(nil)

// UDPPacket represents a UDP packet with destination addr
type UDPPacket struct {
	data []byte
	addr net.Addr
}

// NewUDPOverQUICHandler creates a new handler for UDP over QUIC. If router is nil, it's indended for a UDP client because it doesn't need to route UDP packets
func NewUDPOverQUICHandler(quicConn quic.Connection, router adapter.Router, logger logger.ContextLogger, inboundTag, inboundType string) *UDPOverQUICHandler {
	ctx, cancel := context.WithCancel(context.Background())

	handler := &UDPOverQUICHandler{
		quicConn:    quicConn,
		router:      router,
		logger:      logger,
		inboundTag:  inboundTag,
		inboundType: inboundType,
		sessions:    make(map[string]*UDPSession),
		clientConns: make(map[string]*ClientUDPConn),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Start the main datagram receive loop
	go handler.receiveLoop()

	return handler
}

// receiveLoop continuously receives QUIC datagrams and processes UDP packets
func (h *UDPOverQUICHandler) receiveLoop() {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			data, err := h.quicConn.ReceiveDatagram(h.ctx)
			if err != nil {
				if err == io.EOF || err == context.Canceled {
					return
				}
				continue
			}

			go h.handleUDPPacket(data)
		}
	}
}

// handleUDPPacket processes a single UDP packet received via QUIC datagram
func (h *UDPOverQUICHandler) handleUDPPacket(data []byte) {
	// Parse the packet to extract source, destination, and payload
	sourceAddr, destAddr, payload, err := h.parseUDPPacket(data)
	if err != nil {
		return
	}

	// Check if this is a response packet for a client connection
	if h.router == nil { // Client side (no router means we're the client)
		h.logger.Info("Client QuicConn reading response packet ", sourceAddr, " -> ", destAddr, " of bytes: ", len(payload))
		h.routeToClientConn(sourceAddr, destAddr, payload)
		return
	}
	h.logger.Info("Server QuicConn reading response packet ", sourceAddr, " -> ", destAddr, " of bytes: ", len(payload))

	// Create session key (you might want to include source addr too for bidirectional)
	sessionKey := fmt.Sprintf("%s->%s", sourceAddr.String(), destAddr.String())

	h.sessionsMux.Lock()
	session, ok := h.sessions[sessionKey]
	if !ok {
		// Create new session
		h.logger.Info("Creating new UDP session ", sessionKey)
		session = h.createUDPSession(sessionKey, sourceAddr, destAddr)
		h.sessions[sessionKey] = session
		// Start routing this session
		go h.routeUDPSession(session)
	}
	h.sessionsMux.Unlock()

	// Update last active time
	session.lastActive = time.Now()

	// Send packet to the session
	packet := &UDPPacket{
		data: payload,
		addr: destAddr,
	}

	select {
	case session.conn.readCh <- packet:
	case <-session.conn.closed:
	default:
		// Channel full, drop packet
	}
}

// routeToClientConn routes response packets back to client connections
func (h *UDPOverQUICHandler) routeToClientConn(sourceAddr, destAddr net.Addr, payload []byte) {
	// For client side, the destAddr is our local address, sourceAddr is the remote server
	// We need to find the client connection that sent to this sourceAddr
	clientKey := h.getClientKey(destAddr, sourceAddr)

	h.clientConnsMux.RLock()
	clientConn, ok := h.clientConns[clientKey]
	h.clientConnsMux.RUnlock()

	if !ok {
		// No client connection found for this response
		h.logger.Warn("no UDP client connection found for response from ", sourceAddr.String(), " to ", destAddr.String())
		return
	}

	packet := &UDPPacket{
		data: make([]byte, len(payload)),
		addr: sourceAddr, // Response comes from the server
	}
	copy(packet.data, payload)

	select {
	case clientConn.readCh <- packet:
	case <-clientConn.closed:
		// Client connection closed, clean it up
		h.logger.Info("client conn closed. Unregistering...")
		h.unregisterClientConn(clientConn)
	default:
		// Channel full, drop packet
	}
}

// parseUDPPacket extracts source, destination, and payload from the raw packet
func (h *UDPOverQUICHandler) parseUDPPacket(data []byte) (source, dest net.Addr, payload []byte, err error) {
	if len(data) < 1 {
		return nil, nil, nil, fmt.Errorf("packet too short")
	}

	reader := bytes.NewReader(data)

	// Read source address
	sourceAddrPort, err := M.SocksaddrSerializer.ReadAddrPort(reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read source address: %w", err)
	}

	// Read destination address
	destAddrPort, err := M.SocksaddrSerializer.ReadAddrPort(reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read destination address: %w", err)
	}

	// Remaining data is the UDP payload
	remainingLen := reader.Len()
	payload = make([]byte, remainingLen)
	_, err = reader.Read(payload)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read payload: %w", err)
	}
	h.logger.Debug("parseUDPPacket(): received packet from ", sourceAddrPort.UDPAddr(), " to ", destAddrPort.UDPAddr(), " payload size: ", len(payload))
	return sourceAddrPort.UDPAddr(), destAddrPort.UDPAddr(), payload, nil
}

// createUDPSession creates a new UDP session
func (h *UDPOverQUICHandler) createUDPSession(sessionKey string, sourceAddr, destAddr net.Addr) *UDPSession {
	ctx, cancel := context.WithCancel(h.ctx)

	session := &UDPSession{
		sessionKey: sessionKey,
		sourceAddr: sourceAddr,
		destAddr:   destAddr,
		lastActive: time.Now(),
		cancel:     cancel,
	}

	session.conn = &UDPSessionConn{
		session: session,
		handler: h,
		readCh:  make(chan *UDPPacket, 64),
		writeCh: make(chan *UDPPacket, 64),
		closed:  make(chan struct{}),
	}

	// Start write loop for this session
	go session.conn.writeLoop(ctx)

	return session
}

// routeUDPSession routes a UDP session through sing-box router
func (h *UDPOverQUICHandler) routeUDPSession(session *UDPSession) {
	defer h.cleanupSession(session.sessionKey)

	var metadata adapter.InboundContext
	metadata.Inbound = h.inboundTag
	metadata.InboundType = h.inboundType
	metadata.Source = M.SocksaddrFromNet(session.sourceAddr)
	metadata.Destination = M.SocksaddrFromNet(session.destAddr)

	onClose := func(err error) {
		h.logger.Info("ServerConn onClose with error ", err)
		//session.conn.Close() // do not call close here. Let singbox handle the lifecycle of this conn.
	}
	h.logger.Info("Routing UDP connection ", metadata.Source, " -> ", metadata.Destination)

	// Route the packet connection
	h.router.RoutePacketConnectionEx(h.ctx, session.conn, metadata, onClose)
}

// cleanupSession removes a session from the handler
// TODO: find a correct place to call this
func (h *UDPOverQUICHandler) cleanupSession(sessionKey string) {
	h.logger.Info("Session ", sessionKey, " deleted")
	h.sessionsMux.Lock()
	delete(h.sessions, sessionKey)
	h.sessionsMux.Unlock()
}

// UDPSessionConn methods implementing N.PacketConn

func (c *UDPSessionConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	c.handler.logger.Info("!!!!ServerConn ReadFrom")
	select {
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case packet := <-c.readCh:
		n = copy(p, packet.data)
		c.handler.logger.Info("ServerConn ReadFrom ", packet.addr.String(), " data:", string(p))
		return n, packet.addr, nil
	case <-c.getReadDeadlineChannel():
		return 0, nil, context.DeadlineExceeded
	}
}

func (c *UDPSessionConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	packet := &UDPPacket{
		data: make([]byte, len(p)),
		addr: addr,
	}
	copy(packet.data, p)
	c.handler.logger.Info("!!!!! ServerConn WriteTo ", addr.String(), " data:", string(p))
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	case c.writeCh <- packet:
		c.handler.logger.Info("ServerConn WriteTo ", addr.String(), " data:", string(p))
		return len(p), nil
	case <-c.getWriteDeadlineChannel():
		return 0, context.DeadlineExceeded
	}
}

func (c *UDPSessionConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	c.handler.logger.Info("!!!!ServerConn ReadPacket")
	select {
	case <-c.closed:
		return M.Socksaddr{}, net.ErrClosed
	case packet := <-c.readCh:
		_, err := buffer.Write(packet.data)
		if err != nil {
			return M.Socksaddr{}, err
		}
		c.handler.logger.Info("ServerConn ReadPacket ", packet.addr.String(), " data:", string(buffer.Bytes()))
		return M.SocksaddrFromNet(packet.addr), nil
	case <-c.getReadDeadlineChannel():
		return M.Socksaddr{}, context.DeadlineExceeded
	}
}

func (c *UDPSessionConn) WritePacket(buffer *buf.Buffer, addr M.Socksaddr) error {
	packet := &UDPPacket{
		data: make([]byte, buffer.Len()),
		addr: addr,
	}
	copy(packet.data, buffer.Bytes())
	c.handler.logger.Info("!!!!ServerConn WritePacket ", addr.String())
	select {
	case <-c.closed:
		return net.ErrClosed
	case c.writeCh <- packet:
		c.handler.logger.Info("ServerConn WritePacket ", addr.String())
		return nil
	case <-c.getWriteDeadlineChannel():
		return context.DeadlineExceeded
	}
}

func (c *UDPSessionConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case packet := <-c.writeCh:
			// Send packet back through QUIC datagram
			err := c.sendPacketOverQUIC(packet)
			if err != nil {
				// Handle error
				continue
			}
		}
	}
}

func (c *UDPSessionConn) sendPacketOverQUIC(packet *UDPPacket) error {
	// Create the packet with embedded addresses
	writer := buf.NewSize(1024)
	defer writer.Release()

	// Write destination address (this becomes source for the return packet)
	destAddrPort := M.SocksaddrFromNet(c.session.destAddr)
	err := M.SocksaddrSerializer.WriteAddrPort(writer, destAddrPort)
	if err != nil {
		return err
	}

	// Write source address (this becomes destination for the return packet)
	sourceAddrPort := M.SocksaddrFromNet(c.session.sourceAddr)
	err = M.SocksaddrSerializer.WriteAddrPort(writer, sourceAddrPort)
	if err != nil {
		return err
	}

	// Write payload
	_, err = writer.Write(packet.data)
	if err != nil {
		return err
	}

	// Send via QUIC datagram
	return c.handler.quicConn.SendDatagram(writer.Bytes())
}

func (c *UDPSessionConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.session.cancel()
	})
	return nil
}

func (c *UDPSessionConn) LocalAddr() net.Addr {
	return c.session.sourceAddr
}

func (c *UDPSessionConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

func (c *UDPSessionConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *UDPSessionConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *UDPSessionConn) getReadDeadlineChannel() <-chan time.Time {
	if c.readDeadline.IsZero() {
		return make(chan time.Time) // Never fires
	}
	return time.After(time.Until(c.readDeadline))
}

func (c *UDPSessionConn) getWriteDeadlineChannel() <-chan time.Time {
	if c.writeDeadline.IsZero() {
		return make(chan time.Time) // Never fires
	}
	return time.After(time.Until(c.writeDeadline))
}

// ClientUDPConn implements net.PacketConn for sending UDP packets over QUIC
type ClientUDPConn struct {
	handler       *UDPOverQUICHandler
	localAddr     net.Addr
	destination   M.Socksaddr
	readCh        chan *UDPPacket
	closeOnce     sync.Once
	closed        chan struct{}
	readDeadline  time.Time
	writeDeadline time.Time
}

var _ net.PacketConn = (*ClientUDPConn)(nil)

// NewClientUDPConn creates a net.PacketConn for sending UDP packets over QUIC
func (h *UDPOverQUICHandler) NewClientUDPConn(localAddr net.Addr, destination M.Socksaddr) *ClientUDPConn {
	conn := &ClientUDPConn{
		handler:     h,
		localAddr:   localAddr,
		destination: destination,
		readCh:      make(chan *UDPPacket, 64),
		closed:      make(chan struct{}),
	}

	// Register this connection to receive packets destined for it
	h.registerClientConn(conn)

	return conn
}

// registerClientConn registers a client connection to receive incoming packets
func (h *UDPOverQUICHandler) registerClientConn(conn *ClientUDPConn) {
	clientKey := h.getClientKey(conn.localAddr, conn.destination.UDPAddr())

	h.clientConnsMux.Lock()
	h.clientConns[clientKey] = conn
	h.clientConnsMux.Unlock()
}

// unregisterClientConn removes a client connection from the handler
func (h *UDPOverQUICHandler) unregisterClientConn(conn *ClientUDPConn) {
	clientKey := h.getClientKey(conn.localAddr, conn.destination.UDPAddr())

	h.clientConnsMux.Lock()
	delete(h.clientConns, clientKey)
	h.clientConnsMux.Unlock()
}

// getClientKey creates a unique key for client connections
func (h *UDPOverQUICHandler) getClientKey(localAddr, remoteAddr net.Addr) string {
	return fmt.Sprintf("%s<->%s", localAddr.String(), remoteAddr.String())
}

// ClientUDPConn methods implementing net.PacketConn

func (c *ClientUDPConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	c.handler.logger.Info("!!!!ClientConn ReadFrom")
	select {
	case <-c.closed:
		c.handler.logger.Info("ClientConn ReadFrom: closed")
		return 0, nil, net.ErrClosed
	case packet := <-c.readCh:
		n = copy(p, packet.data)
		c.handler.logger.Info("ClientConn ReadFrom ", packet.addr.String())
		return n, packet.addr, nil
	case <-c.getReadDeadlineChannel():
		c.handler.logger.Info("ClientConn ReadFrom: read deadline exceeded")
		return 0, nil, &net.OpError{Op: "read", Net: "udp", Err: context.DeadlineExceeded}
	}
}

func (c *ClientUDPConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	c.handler.logger.Info("!!!!ClientConn WriteTo ", addr.String())
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.handler.logger.Info("ClientConn WriteTo ", addr.String())

	// Create the packet with embedded addresses for sending over QUIC
	writer := buf.NewSize(len(p) + 64) // Extra space for addresses
	defer writer.Release()

	// Write source address (local address)
	sourceAddrPort := M.SocksaddrFromNet(c.localAddr)
	err = M.SocksaddrSerializer.WriteAddrPort(writer, sourceAddrPort)
	if err != nil {
		return 0, err
	}

	// Write destination address
	destAddrPort := M.SocksaddrFromNet(addr)
	err = M.SocksaddrSerializer.WriteAddrPort(writer, destAddrPort)
	if err != nil {
		return 0, err
	}

	// Write UDP payload
	_, err = writer.Write(p)
	if err != nil {
		return 0, err
	}

	// Send via QUIC datagram
	c.handler.logger.Info("Client QuicConn writing ", writer.Len(), "bytes to ", c.destination.String())
	err = c.handler.quicConn.SendDatagram(writer.Bytes())
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

func (c *ClientUDPConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.handler.logger.Info("ClientConn closed")
		c.handler.unregisterClientConn(c)
	})
	return nil
}

func (c *ClientUDPConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *ClientUDPConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

func (c *ClientUDPConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *ClientUDPConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *ClientUDPConn) getReadDeadlineChannel() <-chan time.Time {
	if c.readDeadline.IsZero() {
		return make(chan time.Time) // Never fires
	}
	return time.After(time.Until(c.readDeadline))
}
