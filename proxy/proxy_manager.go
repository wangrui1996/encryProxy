package proxy

import (
    "fmt"
    "time"
    "context"

    "wangjui.com/enproxy/proxy/config"
)

type ProxyManager struct {
    proxies        []*Proxy
    trafficManager *TrafficManager
    webConfig      config.WebConfig
    webServer      *WebServer
    inbounds       []config.InboundConfig
    accounts       []config.AccountConfig
    dnsResolver    *DNSResolver
    outboundDialer *OutboundDialer
}


// 代理总管理程序
func NewProxyManager(cfg config.Config, trafficManager *TrafficManager) (*ProxyManager, error) {
    if trafficManager == nil {
        return nil, fmt.Errorf("traffic manager is nil")
    }

    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("invalid configuration: %w", err)
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

    if cfg.Web.Enabled && cfg.Web.Listen == "" {
        return nil, fmt.Errorf("web.listen is empty while web.enabled is true")
    }

    dnsResolver, err := NewDNSResolver(cfg.DNS)
    if err != nil {
        return nil, fmt.Errorf("initialize DNS resolver: %w", err)
    }
    outboundDialer := NewOutboundDialer(dnsResolver)
    manager := &ProxyManager{
        proxies: make([]*Proxy, 0, len(cfg.Inbounds)),
        trafficManager: trafficManager,
        webConfig: cfg.Web,
        inbounds: append([]config.InboundConfig(nil), cfg.Inbounds...),
        accounts: append([]config.AccountConfig(nil), cfg.Accounts...),
        dnsResolver:    dnsResolver,
        outboundDialer: outboundDialer,
    }
    for _, in := range cfg.Inbounds {
        manager.proxies = append(manager.proxies, NewProxy(in, accounts, routes, trafficManager, outboundDialer))
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

func (manager *ProxyManager) Close() {
    if manager == nil {
        return
    }

    if manager.dnsResolver != nil {
        manager.dnsResolver.Close()
    }

    if manager.webServer != nil {
        ctx, cancel := context.WithTimeout(
            context.Background(),
            5*time.Second,
        )
        defer cancel()

        if err := manager.webServer.Shutdown(ctx); err != nil {
            Logger.Errorf("shutdown web server failed: %v", err)
        }
    }
}

func (manager *ProxyManager) Count() int { return len(manager.proxies) }
