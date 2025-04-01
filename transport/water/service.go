package water

import (
	"context"
	"net"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type Service struct {
	handler N.TCPConnectionHandlerEx
	logger  logger.ContextLogger
}

func NewService(logger logger.ContextLogger, handler N.TCPConnectionHandlerEx) *Service {
	return &Service{
		handler: handler,
		logger:  logger,
	}
}

func (s *Service) NewConnection(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	destination, err := M.SocksaddrSerializer.ReadAddrPort(conn)
	if err != nil {
		return E.Cause(err, "read destination")
	}

	s.handler.NewConnectionEx(ctx, conn, source, destination, onClose)
	return nil
}
