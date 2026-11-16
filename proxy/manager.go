package proxy

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/dns"
	"wangjui.com/enproxy/proxy/route"
	"wangjui.com/enproxy/proxy/stat"
	"wangjui.com/enproxy/proxy/web"
)

type ProxyManager struct {
	proxies []*Proxy

	accountManager *AccountManager
	routeManager   *route.Manager
	trafficManager *stat.Manager
	dnsResolver    *dns.Resolver

	webConfig config.WebConfig
	webServer *web.WebServer

	inbounds []config.InboundConfig
	accounts []config.AccountConfig
}

// NewProxyManager 从 JSON 配置文件创建整个代理运行环境。
// AccountManager、route.Manager、dns.Resolver、stat.Manager 和入站代理
// 都由 ProxyManager 创建和持有；调用方只负责 Start 和 Close。
func NewProxyManager(configPath string) (*ProxyManager, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path is empty")
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path %q: %w", configPath, err)
	}

	Logger.Infof("Loading config: %s", absPath)
	cfg, err := config.Load(absPath)
	if err != nil {
		return nil, fmt.Errorf("load config failed: %w", err)
	}

	manager, err := NewProxyManagerFromConfig(*cfg)
	if err != nil {
		return nil, err
	}

	Logger.Infof(
		"config loaded: %d inbounds, %d custom outbounds, %d accounts, %d routes; builtin outbounds: freedom, blackhole",
		len(cfg.Inbounds), len(cfg.Outbounds), len(cfg.Accounts), len(cfg.Route),
	)
	return manager, nil
}

// NewProxyManagerFromConfig 允许测试或其他代码直接使用已解析的配置。
// 与从文件创建一样，TrafficManager 也在这里自动创建和托管。
func NewProxyManagerFromConfig(cfg config.Config) (*ProxyManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	manager := &ProxyManager{
		proxies:        make([]*Proxy, 0, len(cfg.Inbounds)),
		trafficManager: stat.NewManager(),
		webConfig:      cfg.Web,
		inbounds:       append([]config.InboundConfig(nil), cfg.Inbounds...),
		accounts:       append([]config.AccountConfig(nil), cfg.Accounts...),
	}

	// 任意一个组件构造失败，都释放已经创建的 DNS/统计资源。
	initialized := false
	defer func() {
		if !initialized {
			manager.Close()
		}
	}()

	var err error
	manager.accountManager, err = NewAccountManager(cfg.Accounts)
	if err != nil {
		return nil, fmt.Errorf("initialize account manager: %w", err)
	}

	manager.routeManager, err = route.NewManager(cfg.Route, cfg.Inbounds, cfg.Outbounds)
	if err != nil {
		return nil, fmt.Errorf("initialize route manager: %w", err)
	}

	manager.dnsResolver, err = dns.NewResolver(cfg.DNS)
	if err != nil {
		return nil, fmt.Errorf("initialize DNS resolver: %w", err)
	}

	outboundDialer := NewOutboundDialer(manager.dnsResolver)
	for _, inbound := range cfg.Inbounds {
		proxyInstance, err := NewProxy(
			inbound,
			manager.accountManager,
			manager.routeManager,
			outboundDialer,
			manager.trafficManager,
		)
		if err != nil {
			return nil, fmt.Errorf("initialize inbound %q: %w", inbound.Tag, err)
		}
		manager.proxies = append(manager.proxies, proxyInstance)
	}

	initialized = true
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

// Close 释放 ProxyManager 持有的全部运行时资源。
func (manager *ProxyManager) Close() {
	if manager == nil {
		return
	}

	if manager.webServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.webServer.Shutdown(ctx); err != nil {
			Logger.Errorf("shutdown web server failed: %v", err)
		}
	}

	// 先停止监听与连接，再关闭它们依赖的 DNS 和流量统计资源。
	for _, p := range manager.proxies {
		p.Close()
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
