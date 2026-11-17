// Package protocol contains application-layer proxy protocols and their outbound transports.
// It uses core transport primitives but does not depend on config, stat, route or proxy.
package protocol

import (
	"context"
	"io"
	"net"
)

// Resolver selects an outbound after the client protocol has provided a target hostname.
type Resolver interface {
	ResolveProxy(inboundTag, hostname string) (*Outbound, error)
}

// DialFunc opens an outbound stream. The assembly layer owns dialing, optional
// AES-GCM framing and ENPA authentication. For freedom, address is the target.
// For an HTTP outbound, address is the HTTP upstream's server.
type DialFunc func(ctx context.Context, outbound *Outbound, address string) (net.Conn, error)

// RelayFunc forwards a full-duplex stream; core supplies the implementation.
// Readers may be buffered (for example, after HTTP CONNECT negotiation).
type RelayFunc func(downstream net.Conn, downReader io.Reader, upstream net.Conn, upReader io.Reader) error

// AuthenticateFunc verifies an HTTP proxy username/password and updates the
// current connection's account rate limiter and traffic attribution.
type AuthenticateFunc func(username, password string) error

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

// Outbound is a runtime route result. DialContext is bound by the assembly layer
// to the selected outbound's outer encryption/authentication settings.
// Treat this descriptor as immutable once returned by a resolver.
type Outbound struct {
	Tag         string
	Protocol    string
	Server      string
	HTTP        *HTTPOptions
	Shadowsocks *ShadowsocksOptions
	DialContext func(ctx context.Context, address string) (net.Conn, error)
}
