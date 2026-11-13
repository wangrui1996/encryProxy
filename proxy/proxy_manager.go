package proxy

import (
	"context"
	"fmt"
	"time"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/dns"
	"wangjui.com/enproxy/proxy/route"
	"wangjui.com/enproxy/proxy/stat"
	"wangjui.com/enproxy/proxy/web"
)

type ProxyManager struct {
	proxies []*core.Proxy

	accountManager *AccountManager
	routeManager   *route.Manager
	trafficManager *stat.Manager
	dnsResolver    *dns.Resolver

	webConfig config.WebConfig
	webServer *web.WebServer

	inbounds []config.InboundConfig
	accounts []config.AccountConfig
}

// NewProxyManager 创建并组装所有运行时 Manager。
func NewProxyManager(
	cfg config.Config,
	trafficManager *stat.Manager,
) (*ProxyManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf(
			"invalid configuration: %w",
			err,
		)
	}

	if trafficManager == nil {
		return nil, fmt.Errorf("traffic manager is nil")
	}

	// 1. 创建账号管理器。
	accountManager, err := NewAccountManager(cfg.Accounts)
	if err != nil {
		return nil, fmt.Errorf(
			"initialize account manager: %w",
			err,
		)
	}

	// 2. 创建路由管理器。
	routeManager, err := route.NewManager(
		cfg.Route,
		cfg.Inbounds,
		cfg.Outbounds,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"initialize route manager: %w",
			err,
		)
	}

	// 3. 创建 DNS Resolver。
	dnsResolver, err := dns.NewResolver(cfg.DNS)
	if err != nil {
		return nil, fmt.Errorf(
			"initialize DNS resolver: %w",
			err,
		)
	}

	// 4. 创建 core 所需的上游拨号器。
	outboundDialer := core.NewOutboundDialer(dnsResolver)

	manager := &ProxyManager{
		proxies:        make([]*core.Proxy, 0, len(cfg.Inbounds)),
		accountManager: accountManager,
		routeManager:   routeManager,
		trafficManager: trafficManager,
		dnsResolver:    dnsResolver,
		webConfig:      cfg.Web,
		inbounds:       append([]config.InboundConfig(nil), cfg.Inbounds...),
		accounts:       append([]config.AccountConfig(nil), cfg.Accounts...),
	}

	// 5. 组装 core.Proxy。
	deps := core.Dependencies{
		Authenticator: accountManager,
		RouteResolver: routeManager,
		Dialer:        outboundDialer,
		Traffic:       trafficManager,
	}

	for _, inbound := range cfg.Inbounds {
		proxyInstance, err := core.NewProxy(inbound, deps)
		if err != nil {
			dnsResolver.Close()
			return nil, fmt.Errorf(
				"initialize inbound %q: %w",
				inbound.Tag,
				err,
			)
		}

		manager.proxies = append(
			manager.proxies,
			proxyInstance,
		)
	}

	return manager, nil
}

// Start 启动统计、所有入站代理以及 Web 服务。
func (manager *ProxyManager) Start() {
	if manager == nil {
		return
	}

	if manager.trafficManager != nil {
		manager.trafficManager.Start()
	}

	for _, p := range manager.proxies {
		proxyInstance := p
		go proxyInstance.Run()
	}

	if manager.webConfig.Enabled {
		manager.webServer = web.NewWebServer(
			manager.webConfig.Listen,
			manager.trafficManager,
			manager.inbounds,
			manager.accounts,
		)

		go manager.webServer.Run()
	}
}

// Close 释放由 ProxyManager 创建和管理的资源。
func (manager *ProxyManager) Close() {
	if manager == nil {
		return
	}

	if manager.webServer != nil {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		if err := manager.webServer.Shutdown(ctx); err != nil {
			Logger.Errorf(
				"shutdown web server failed: %v",
				err,
			)
		}
	}

	if manager.dnsResolver != nil {
		manager.dnsResolver.Close()
	}

	if manager.trafficManager != nil {
		manager.trafficManager.Close()
	}
}

func (manager *ProxyManager) Count() int {
	if manager == nil {
		return 0
	}
	return len(manager.proxies)
}