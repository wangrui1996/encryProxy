package protocol

import (
	"context"
	"io"
	"net"
	"fmt"
)

// Resolver selects an outbound after the client protocol has provided a target hostname.
type Resolver interface {
	ResolveProxy(inboundTag, hostname string) (*Outbound, error)
}

// DialFunc opens one outbound stream. The assembly layer owns network dialing,
// optional AES-GCM framing and ENPA authentication. For freedom, address is
// the target; for HTTP and Shadowsocks outbounds, it is the upstream server.
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

// CryptoOptions describes the optional outer transport encryption and
// authentication performed by the assembly-layer DialFunc.
type CryptoOptions struct {
	Key          string
	AuthRequired bool
	Account      string
	Passwd       string
}

// Outbound is immutable runtime route data. It contains no dial callback;
// protocol handlers receive the single DialFunc dependency separately.
type Outbound struct {
	Tag         string
	Protocol    string
	Server      string
	Crypto      *CryptoOptions
	HTTP        *HTTPOptions
	Shadowsocks *ShadowsocksOptions
}

// 默认黑洞，毁灭一切请求
type Blackhole struct{}

func (Blackhole) Handle(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// 默认自由访问，接受一切请求
type Freedom struct{ Dial DialFunc }

func (f Freedom) Connect(ctx context.Context, cfg *Outbound, target string) (net.Conn, error) {
	if f.Dial == nil {
		return nil, fmt.Errorf("freedom dialer is nil")
	}
	return f.Dial(ctx, cfg, target)
}
