package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Resolver selects an outbound after the client protocol has provided a target hostname.
type Resolver interface {
	ResolveProxy(inboundTag, hostname string) (string, error)
}

// OutboundConnector opens a routed stream and preserves bytes read ahead by a handshake.
type OutboundConnector interface {
	Connect(ctx context.Context, tag, target string) (net.Conn, io.Reader, error)
}

type HTTPOutbounds interface {
	OutboundConnector
	NewHTTPTransport(tag string) (*http.Transport, error)
}

// ErrBlackhole tells the inbound handler to close silently.
var ErrBlackhole = errors.New("blackhole outbound")

// DialFunc opens the raw or outer-encrypted stream used by a protocol client.
type DialFunc func(context.Context, string) (net.Conn, error)

// RelayFunc forwards a full-duplex stream; core supplies the implementation.
// Readers may be buffered (for example, after HTTP CONNECT negotiation).
type RelayFunc func(ctx context.Context, downstream net.Conn, downReader io.Reader, upstream net.Conn, upReader io.Reader) error

// AuthenticateFunc verifies HTTP proxy credentials and returns a callback that
// restores the connection's default account state after the request.
type AuthenticateFunc func(username, password string) (reset func(), err error)

// Handler represents a protocol-specific inbound connection handler.
type Handler interface {
	Handle(ctx context.Context, conn net.Conn, auth AuthenticateFunc) error
}

// InboundOptions contains only the settings consumed by protocol handlers.
// File parsing, defaults and validation remain in the assembly/config layer.
type InboundOptions struct {
	Tag         string
	Protocol    string
	HTTP        *HTTPOptions
	Shadowsocks *ShadowsocksOptions
}

// HTTPOptions describes protocol authentication, independent of file formats.
type HTTPOptions struct {
	AuthRequired bool
	Account      string
	Passwd       string
}

type ShadowsocksOptions struct {
	Method   string
	Password string
}

// Outbound contains the application protocol parameters for one upstream server.
type Outbound struct {
	Server      string
	HTTP        *HTTPOptions
	Shadowsocks *ShadowsocksOptions
}

type Router interface {
	Resolver
	RelayResolver
}

// NewHandler selects the inbound application protocol once when the listener is built.
func NewHandler(in InboundOptions, routes Router, outbounds HTTPOutbounds, relay RelayFunc) (Handler, error) {
	switch in.Protocol {
	case "tcp":
		return NewTCP(in, routes, outbounds, relay)
	case "http":
		return NewHTTP(in, routes, outbounds, relay)
	case "shadowsocks":
		return NewShadowsocks(in, routes, outbounds, relay)
	default:
		return nil, fmt.Errorf("inbound %q: unsupported protocol %q", in.Tag, in.Protocol)
	}
}
