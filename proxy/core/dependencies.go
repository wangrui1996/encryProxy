package core

import (
	"context"
	"net"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/stat"
)

// DNSResolver 定义 DNS 查询能力，由 proxy/dns 实现。
type DNSResolver interface {
	Lookup(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Authenticator 只依赖认证能力，不依赖具体的账号管理实现。
// HTTP 使用 AuthenticateProxy，TCP 加密通道使用 AuthenticateCrypto。
type Authenticator interface {
	AuthenticateCrypto(username, password string) (*Account, error)
	AuthenticateProxy(username, password string) (*Account, error)
}

// RouteResolver 同时暴露固定 relay 与基于目标域名的 proxy 路由。
type RouteResolver interface {
	ResolveRelay(inboundTag string) (*config.OutboundConfig, error)
	ResolveProxy(inboundTag, hostname string) (*config.OutboundConfig, error)
}

// Dialer 只负责连接指定地址；DNS、超时等由实现方处理。
type Dialer interface {
	DialContext(ctx context.Context, address string) (net.Conn, error)
}

// Dependencies 是核心层的外部依赖，统一在 ProxyManager 注入。
type Dependencies struct {
	Authenticator Authenticator
	RouteResolver RouteResolver
	Dialer        Dialer
	Traffic       *stat.Manager
}
