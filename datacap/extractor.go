package datacap

import (
	"bufio"
	"net"
	"net/http"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

type DeviceIDExtractor struct {
	deviceIDHeader    string
	countryCodeHeader string
	platformHeader    string
	logger            log.ContextLogger
}

func NewDeviceIDExtractor(deviceIDHeader, countryCodeHeader, platformHeader string, logger log.ContextLogger) *DeviceIDExtractor {
	return &DeviceIDExtractor{
		deviceIDHeader:    deviceIDHeader,
		countryCodeHeader: countryCodeHeader,
		platformHeader:    platformHeader,
		logger:            logger,
	}
}

func (e *DeviceIDExtractor) ExtractFromHTTPHeaders(conn net.Conn) (deviceID, countryCode, platform string, wrappedConn net.Conn) {
	reader := bufio.NewReader(conn)

	peekBytes, err := reader.Peek(4096)
	if err != nil {
		e.logger.Debug("failed to peek connection data: ", err)
		return "", "", "", &peekConn{conn, reader}
	}

	// Validate that we have enough bytes for a minimal HTTP request
	// A minimal HTTP request is at least "GET / HTTP/1.0\r\n\r\n" (18 bytes)
	const minHTTPRequestSize = 18
	if len(peekBytes) < minHTTPRequestSize {
		e.logger.Debug("peeked bytes too short for HTTP request: ", len(peekBytes), " bytes")
		return "", "", "", &peekConn{conn, reader}
	}

	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(string(peekBytes))))
	if err != nil {
		e.logger.Debug("failed to parse HTTP request: ", err)
		return "", "", "", &peekConn{conn, reader}
	}

	deviceID = req.Header.Get(e.deviceIDHeader)
	countryCode = req.Header.Get(e.countryCodeHeader)
	platform = req.Header.Get(e.platformHeader)

	return deviceID, countryCode, platform, &peekConn{conn, reader}
}

func (e *DeviceIDExtractor) ExtractFromMetadata(metadata adapter.InboundContext) (deviceID, countryCode, platform string) {
	return "", "", ""
}

type peekConn struct {
	net.Conn
	reader *bufio.Reader
}

func (p *peekConn) Read(b []byte) (n int, err error) {
	return p.reader.Read(b)
}
