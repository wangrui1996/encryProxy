// Package protocol contains application-layer proxy protocols and their outbound transports.
// It does not depend on core, stat, route or proxy, preventing import cycles.
package protocol

import (
	"context"
	"io"
	"net"

	"wangjui.com/enproxy/proxy/config"
)

// Resolver selects an outbound after the client protocol has provided a target hostname.
type Resolver interface {
	ResolveProxy(inboundTag, hostname string) (*config.OutboundConfig, error)
}

// DialFunc opens an outbound stream. core owns the underlying dial, optional AES-GCM
// framing and optional ENPA authentication. For freedom, address is the target.
// For an HTTP outbound, address is the HTTP upstream's server.
type DialFunc func(ctx context.Context, outbound *config.OutboundConfig, address string) (net.Conn, error)

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
