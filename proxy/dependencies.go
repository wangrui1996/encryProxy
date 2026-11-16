package proxy

import (
    "context"
    "net"

    "wangjui.com/enproxy/proxy/config"
    "wangjui.com/enproxy/proxy/stat"
)

type DNSResolver interface {
    Lookup(ctx context.Context, host string) ([]net.IPAddr, error)
}

type Authenticator interface {
    AuthenticateCrypto(username, password string) (*Account, error)
    AuthenticateProxy(username, password string) (*Account, error)
}

type RouteResolver interface {
    ResolveRelay(inboundTag string) (*config.OutboundConfig, error)
    ResolveProxy(inboundTag, hostname string) (*config.OutboundConfig, error)
}

// 上游 TCP 拨号接口
type Dialer interface {
    DialContext(ctx context.Context, address string) (net.Conn, error)
}

type Dependencies struct {
    Authenticator Authenticator
    RouteResolver RouteResolver
    Dialer        Dialer
    Traffic       *stat.Manager
}