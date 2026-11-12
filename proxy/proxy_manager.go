package proxy

import (
    "fmt"
    "net"
    "strconv"
    "time"
    "strings"
)

type ProxyManager struct {
    proxies        []*Proxy
    trafficManager *TrafficManager
    webConfig      WebConfig
    webServer      *WebServer
    inbounds       []InboundConfig
    accounts       []AccountConfig
    dnsResolver    *DNSResolver
    outboundDialer *OutboundDialer
}

// 校验密码是否合理
func ValidateAESKey(key string) error {
    switch len([]byte(key)) {
    case 16, 24, 32:
        return nil
    default:
        return fmt.Errorf("AES key must be 16, 24, or 32 bytes; got %d", len([]byte(key)))
    }
}

// 校验监听地址
func ValidateListenAddr(tag, listen string, seen map[string]struct{}) error {
    if listen == "" {
        return fmt.Errorf("inbound %q listen address is empty", tag)
    }
    if _, exists := seen[listen]; exists {
        return fmt.Errorf("duplicate inbound listen address: %s", listen)
    }
    if _, err := net.ResolveTCPAddr("tcp", listen); err != nil {
        return fmt.Errorf("inbound %q invalid listen address: %w", tag, err)
    }
    seen[listen] = struct{}{}
    return nil
}

func ValidateOutboundAddr(tag, address string) error {
    host, port, err := net.SplitHostPort(address)
    if err != nil {
        return fmt.Errorf(
            "outbound %q invalid server address %q: %w",
            tag,
            address,
            err,
        )
    }

    if host == "" {
        return fmt.Errorf(
            "outbound %q server host is empty",
            tag,
        )
    }

    portNumber, err := strconv.Atoi(port)
    if err != nil || portNumber < 1 || portNumber > 65535 {
        return fmt.Errorf(
            "outbound %q invalid server port %q",
            tag,
            port,
        )
    }

    return nil
}

// 验证 inbounds 配置文件
func ValidateInbounds(inbounds []InboundConfig) error {
    if len(inbounds) == 0 {
        return fmt.Errorf("no inbound configured")
    }
    inboundTags := make(map[string]struct{}, len(inbounds))
    listenSet := make(map[string]struct{}, len(inbounds))
    for i := range inbounds {
        inbound := &inbounds[i]
        if inbound.Tag == "" {
            return fmt.Errorf("inbound tag is empty")
        }
        if _, exists := inboundTags[inbound.Tag]; exists {
            return fmt.Errorf("duplicate inbound tag: %s", inbound.Tag)
        }
        inboundTags[inbound.Tag] = struct{}{}
        if err := ValidateListenAddr(inbound.Tag, inbound.Listen, listenSet); err != nil {
            return err
        }
        if inbound.Crypto.Enabled {
            if err := ValidateAESKey(inbound.Crypto.Key); err != nil {
                return fmt.Errorf("inbound %q crypto: %w", inbound.Tag, err)
            }
        }
    }
    return nil
}

// 验证 Outbound 配置文件
func ValidateOutbounds(outbounds []OutboundConfig) error {
    if len(outbounds) == 0 {
        return fmt.Errorf("no outbound configured")
    }
    outboundTags := make(map[string]struct{}, len(outbounds))
    for i := range outbounds {
        outbound := &outbounds[i]
        if outbound.Tag == "" {
            return fmt.Errorf("outbound tag is empty")
        }
        if _, exists := outboundTags[outbound.Tag]; exists {
            return fmt.Errorf("duplicate outbound tag: %s", outbound.Tag)
        }
        outboundTags[outbound.Tag] = struct{}{}
        if outbound.Protocol == "" {
            outbound.Protocol = "tcp"
        }
        if outbound.Protocol != "tcp" {
            return fmt.Errorf("outbound %q unsupported protocol %q", outbound.Tag, outbound.Protocol)
        }
        if outbound.Server == "" {
            return fmt.Errorf("outbound %q server address is empty", outbound.Tag)
        }
        if outbound.Crypto.Enabled {
            if err := ValidateAESKey(outbound.Crypto.Key); err != nil {
                return fmt.Errorf("outbound %q crypto: %w", outbound.Tag, err)
            }
        }
        if err := ValidateOutboundAddr(outbound.Tag, outbound.Server); err != nil {
            return err
        }
    }
    return nil;
}

func (c *DNSConfig) Validate() error {
    if c == nil {
        return nil
    }

    if strings.TrimSpace(c.CacheTTL) == "" {
        return fmt.Errorf("dns.cache_ttl is required")
    }

    ttl, err := time.ParseDuration(c.CacheTTL)
    if err != nil {
        return fmt.Errorf(
            "invalid dns.cache_ttl %q: %w",
            c.CacheTTL,
            err,
        )
    }

    if ttl <= 0 {
        return fmt.Errorf("dns.cache_ttl must be greater than zero")
    }

    return nil
}

// 代理总管理程序
func NewProxyManager(cfg Config, trafficManager *TrafficManager) (*ProxyManager, error) {
    if trafficManager == nil {
        return nil, fmt.Errorf("traffic manager is nil")
    }

    dnsResolver, err := NewDNSResolver(cfg.DNS)
    if err != nil {
        return nil, fmt.Errorf("initialize DNS resolver: %w", err)
    }
    outboundDialer := NewOutboundDialer(dnsResolver)
    if err := ValidateInbounds(cfg.Inbounds); err != nil {
        return nil, err
    }
    if err := ValidateOutbounds(cfg.Outbounds); err != nil {
        return nil, err
    }

    accounts, err := NewAccountManager(cfg.Accounts)
    if err != nil {
        return nil, err
    }
    for _, inbound := range cfg.Inbounds {
        if inbound.AuthRequired && accounts.Count() == 0 {
            return nil, fmt.Errorf("inbound %q requires authentication but no accounts are configured", inbound.Tag)
        }
    }
    routes, err := NewRouteManager(cfg.Route, cfg.Inbounds, cfg.Outbounds)
    if err != nil {
        return nil, err
    }
    manager := &ProxyManager{
        proxies: make([]*Proxy, 0, len(cfg.Inbounds)),
        trafficManager: trafficManager,
        webConfig: cfg.Web,
        inbounds: append([]InboundConfig(nil), cfg.Inbounds...),
        accounts: append([]AccountConfig(nil), cfg.Accounts...),
        dnsResolver:    dnsResolver,
        outboundDialer: outboundDialer,
    }
    for _, in := range cfg.Inbounds {
        manager.proxies = append(manager.proxies, NewProxy(in, accounts, routes, trafficManager, outboundDialer))
    }
    if cfg.Web.Enabled && cfg.Web.Listen == "" {
        return nil, fmt.Errorf("web.listen is empty while web.enabled is true")
    }
    return manager, nil
}

func (manager *ProxyManager) Start() {
    for _, p := range manager.proxies {
        proxy := p
        go proxy.Run()
    }
    if manager.webConfig.Enabled {
        manager.webServer = NewWebServer(manager.webConfig.Listen, manager.trafficManager, manager.inbounds, manager.accounts)
        go manager.webServer.Run()
    }
}
func (manager *ProxyManager) Count() int { return len(manager.proxies) }
