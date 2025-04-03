package water

import (
	"context"
	"net"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Service provide a way to create a new connection, extract the destination
// and handle it.
type Service struct {
	handler N.TCPConnectionHandlerEx
	logger  logger.ContextLogger
}

// NewService creates a new Service instance with the given logger and
// TCPConnectionHandlerEx.
func NewService(logger logger.ContextLogger, handler N.TCPConnectionHandlerEx) *Service {
	return &Service{
		handler: handler,
		logger:  logger,
	}
}

// NewConnection extracts the destination address from the connection and
// handle the connection.
func (s *Service) NewConnection(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	destination, err := M.SocksaddrSerializer.ReadAddrPort(conn)
	if err != nil {
		return E.Cause(err, "read destination")
	}

	s.handler.NewConnectionEx(ctx, conn, source, destination, onClose)
	return nil
}
