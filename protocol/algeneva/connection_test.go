package algeneva

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"

	alg "github.com/getlantern/algeneva"
)

func TestE2E(t *testing.T) {
	destination := metadata.ParseSocksaddr("google.com:80")
	client, server := net.Pipe()
	strategy, _ := alg.NewHTTPStrategy("[HTTP:method:*]-insert{%0A:end:value:2}-|")
	logger := log.StdLogger()
	d := aDialer{
		Dialer:   &mockDialer{conn: server},
		strategy: strategy,
		logger:   logger,
	}
	in := &Inbound{logger: logger}
	go func() {
		_, err := in.newConnectionEx(context.Background(), client)
		require.NoError(t, err)
	}()

	_, err := d.DialContext(context.Background(), "tcp", destination)
	require.NoError(t, err)
}

type mockDialer struct {
	conn net.Conn
}

func (m *mockDialer) DialContext(ctx context.Context, network string, destination metadata.Socksaddr) (net.Conn, error) {
	return m.conn, nil
}
func (d *mockDialer) ListenPacket(ctx context.Context, destination metadata.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}
