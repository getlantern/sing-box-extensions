//go:build integration

package water

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/getlantern/sing-box-extensions/option"
	_ "github.com/refraction-networking/water/transport/v1"
	"github.com/sagernet/sing-box/log"
	O "github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/metadata"
)

func TestOutboundWASM(t *testing.T) {
	t.Parallel()
	transportConfig := map[string]any{
		"remote_addr":          "104.18.29.242",
		"remote_port":          "443",
		"password":             "8JCsPssfgS8tiRwiMlhARg==",
		"method":               "chacha20-ietf-poly1305",
		"internal_buffer_size": 16383,
	}

	options := option.WATEROutboundOptions{
		DownloadTimeout:           "10s",
		WASMStorageDir:            "build",
		WazeroCompilationCacheDir: "build",
		WASMAvailableAt:           []string{"https://github.com/getlantern/tiny-shadowsocks/releases/download/v1.0.2/shadowsocks_client_debug.wasm"},
		Transport:                 "shadowsocks",
		ServerOptions:             O.ServerOptions{Server: "192.168.50.37", ServerPort: 8388},
		DialerOptions:             O.DialerOptions{},
		Config:                    transportConfig,
		SkipHandshake:             true,
	}

	ctx := context.Background()
	out, err := NewOutbound(ctx, nil, log.NewNOPFactory().Logger(), "test", options)
	if err != nil {
		t.Fatalf("failed to create outbound: %v", err)
	}

	var tests = []struct {
		name            string
		givenDomain     string
		givenHost       string
		givenRemoteAddr string
		givenRemotePort uint16
	}{
		{
			name:            "lantern.io should succeed",
			givenDomain:     "https://lantern.io",
			givenHost:       "lantern.io",
			givenRemoteAddr: "104.18.29.242",
			givenRemotePort: 443,
		},
		{
			name:            "google.com should succeed",
			givenDomain:     "https://google.com",
			givenHost:       "google.com",
			givenRemoteAddr: "172.217.29.78",
			givenRemotePort: 443,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tt.givenDomain, http.NoBody)
			if err != nil {
				t.Fatalf("failed to create HTTP request: %v", err)
			}
			req.Header.Set("Host", tt.givenHost)
			req.Header.Set("Accept", "*/*")

			client := &http.Client{
				Transport: &http.Transport{
					DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
						return out.DialContext(ctx, network, metadata.ParseSocksaddrHostPort(tt.givenRemoteAddr, tt.givenRemotePort))
					},
				},
				Timeout: 10 * time.Second,
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("failed to do request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected status code 200, got %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}
			t.Logf("response: %s", body)
		})
	}
}
