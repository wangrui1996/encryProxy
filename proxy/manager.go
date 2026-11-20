package proxy

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/dns"
	"wangjui.com/enproxy/proxy/route"
	"wangjui.com/enproxy/proxy/stat"
	"wangjui.com/enproxy/proxy/web"
)

type ProxyManager struct {
	proxies   []*Proxy
	mu        sync.Mutex
	started   bool
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
	runErr    error

	accountManager *AccountManager
	routeManager   *route.Manager
	trafficManager *stat.Manager
	dnsResolver    *dns.Resolver

	webServer *web.WebServer
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

	manager, err := newProxyManagerFromConfig(*cfg, filepath.Dir(absPath))
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
	return newProxyManagerFromConfig(cfg, ".")
}

func newProxyManagerFromConfig(cfg config.Config, ruleBaseDir string) (*ProxyManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	manager := &ProxyManager{
		proxies:        make([]*Proxy, 0, len(cfg.Inbounds)),
		done:           make(chan struct{}),
		trafficManager: stat.NewManager(),
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

	manager.routeManager, err = route.NewManager(routeRules(cfg.Route), ruleBaseDir)
	if err != nil {
		return nil, fmt.Errorf("initialize route manager: %w", err)
	}

	var ttl time.Duration
	if cfg.DNS != nil {
		ttl, err = time.ParseDuration(cfg.DNS.CacheTTL)
		if err != nil {
			return nil, fmt.Errorf("parse DNS cache TTL: %w", err)
		}
	}
	var cacheFile string
	var cacheMaxBytes int64
	if cfg.DNS != nil {
		cacheFile = cfg.DNS.CacheFile
		cacheMaxBytes = cfg.DNS.CacheMaxBytes
		if cacheFile != "" && !filepath.IsAbs(cacheFile) {
			cacheFile = filepath.Join(ruleBaseDir, cacheFile)
		}
	}
	manager.dnsResolver, err = dns.NewResolverWithCache(ttl, cacheFile, cacheMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("initialize DNS resolver: %w", err)
	}

	outbounds, err := NewOutboundManager(cfg.Outbounds, NewOutboundDialer(manager.dnsResolver))
	if err != nil {
		return nil, fmt.Errorf("initialize outbound manager: %w", err)
	}
	Logger.Infof("built-in outbounds initialized: freedom, blackhole")
	for _, outbound := range cfg.Outbounds {
		method := ""
		if outbound.Shadowsocks != nil {
			method = outbound.Shadowsocks.Method
		}
		Logger.Infof("outbound initialized: tag=%q protocol=%q server=%q crypto=%t crypto_auth_required=%t http_auth_required=%t ss_method=%q", outbound.Tag, outbound.Protocol, outbound.Server, outbound.Crypto != nil, outbound.Crypto != nil && outbound.Crypto.AuthRequired, outbound.HTTP != nil && outbound.HTTP.AuthRequired, method)
	}
	for _, inbound := range cfg.Inbounds {
		proxyInstance, err := NewProxy(inbound, manager.accountManager, manager.routeManager, outbounds, manager.trafficManager)
		if err != nil {
			return nil, fmt.Errorf("initialize inbound %q: %w", inbound.Tag, err)
		}
		manager.proxies = append(manager.proxies, proxyInstance)
		method := ""
		if inbound.Shadowsocks != nil {
			method = inbound.Shadowsocks.Method
		}
		Logger.Infof("inbound initialized: tag=%q listen=%q protocol=%q crypto=%t crypto_auth_required=%t http_auth_required=%t http_sniff=%t ss_method=%q upload_kbps=%d download_kbps=%d", inbound.Tag, inbound.Listen, inbound.Protocol, inbound.Crypto != nil, inbound.Crypto != nil && inbound.Crypto.AuthRequired, inbound.HTTP != nil && inbound.HTTP.AuthRequired, inbound.HTTP != nil && inbound.HTTP.Sniff, method, inbound.RateLimit.UploadKbps, inbound.RateLimit.DownloadKbps)
	}

	// 仅按路由引用判断是否使用；未引用的入站仍监听、未引用的出站仍可用。
	usedInbounds := make(map[string]bool, len(cfg.Inbounds))
	usedOutbounds := make(map[string]bool, len(cfg.Outbounds))
	for i, rule := range cfg.Route {
		for _, tag := range rule.Inbound {
			usedInbounds[tag] = true
		}
		if rule.Type == "relay" {
			usedOutbounds[rule.Relay.Outbound] = true
			Logger.Infof("route[%d] initialized: type=relay inbound=%v outbound=%q", i, rule.Inbound, rule.Relay.Outbound)
		} else {
			usedOutbounds[rule.Proxy.DefaultOutbound] = true
			Logger.Infof("route[%d] initialized: type=proxy inbound=%v default_outbound=%q policies=%d", i, rule.Inbound, rule.Proxy.DefaultOutbound, len(rule.Proxy.Policies))
			for j, policy := range rule.Proxy.Policies {
				usedOutbounds[policy.Outbound] = true
				Logger.Infof("route[%d] policy[%d]: domain=%v outbound=%q", i, j, policy.Domain, policy.Outbound)
			}
		}
	}
	for _, inbound := range cfg.Inbounds {
		if !usedInbounds[inbound.Tag] {
			Logger.Warnf("inbound %q has no route (listening, but cannot forward connections)", inbound.Tag)
		}
	}
	for _, outbound := range cfg.Outbounds {
		if !usedOutbounds[outbound.Tag] {
			Logger.Warnf("outbound %q is not referenced by any route", outbound.Tag)
		}
	}

	// WebServer 在构造阶段创建，Start 仅负责启动。
	// 未启用 Web 时保持 nil，不创建 HTTP 服务。
	if cfg.Web.Enabled {
		manager.webServer = web.NewWebServer(cfg.Web, manager.trafficManager, append([]config.InboundConfig(nil), cfg.Inbounds...), append([]config.AccountConfig(nil), cfg.Accounts...))
	}

	initialized = true
	return manager, nil
}

// Start binds every required listener before reporting success. A failed start
// rolls back all resources; construct a new manager to retry a failed/closed one.
func (manager *ProxyManager) Start() error {
	if manager == nil {
		return fmt.Errorf("proxy manager is nil")
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return fmt.Errorf("proxy manager is closed")
	}
	if manager.started {
		manager.mu.Unlock()
		return nil
	}
	var startErr error
	for _, p := range manager.proxies {
		if err := p.Start(); err != nil {
			startErr = err
			break
		}
	}
	if startErr == nil && manager.webServer != nil {
		startErr = manager.webServer.Start()
	}
	if startErr != nil {
		manager.closed = true
		manager.runErr = startErr
		manager.mu.Unlock()
		manager.Close()
		return startErr
	}
	manager.trafficManager.Start()
	manager.started = true
	manager.mu.Unlock()
	// Waiters do not participate in Close's worker join, avoiding self-deadlock.
	for _, p := range manager.proxies {
		p := p
		go func() { manager.serviceExited(p.Wait()) }()
	}
	if manager.webServer != nil {
		go func() { manager.serviceExited(manager.webServer.Wait()) }()
	}
	return nil
}

func (manager *ProxyManager) serviceExited(err error) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	if err == nil {
		err = fmt.Errorf("service stopped unexpectedly")
	}
	if manager.runErr == nil {
		manager.runErr = err
	}
	manager.mu.Unlock()
	manager.Close()
}

// Wait returns after resource cleanup, including when a runtime service fails.
func (manager *ProxyManager) Wait() error {
	if manager == nil {
		return fmt.Errorf("proxy manager is nil")
	}
	manager.mu.Lock()
	ready := manager.started || manager.closed
	manager.mu.Unlock()
	if !ready {
		return fmt.Errorf("proxy manager has not started")
	}
	<-manager.done
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.runErr
}

// Close is idempotent; concurrent callers wait for the same cleanup operation.
func (manager *ProxyManager) Close() {
	if manager == nil {
		return
	}
	manager.closeOnce.Do(func() {
		manager.mu.Lock()
		manager.closed = true
		manager.mu.Unlock()
		// Cancel all inbounds first so a slow session cannot delay cancellation of others.
		for _, p := range manager.proxies {
			p.cancel()
		}
		var stopped sync.WaitGroup
		for _, p := range manager.proxies {
			stopped.Add(1)
			go func(p *Proxy) { defer stopped.Done(); p.Close() }(p)
		}
		stopped.Wait()
		if manager.webServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := manager.webServer.Shutdown(ctx)
			cancel()
			if err != nil {
				manager.mu.Lock()
				if manager.runErr == nil {
					manager.runErr = fmt.Errorf("shutdown web: %w", err)
				}
				manager.mu.Unlock()
			}
		}
		if manager.dnsResolver != nil {
			manager.dnsResolver.Close()
		}
		if manager.trafficManager != nil {
			manager.trafficManager.Close()
		}
		close(manager.done)
	})
}

func (manager *ProxyManager) Count() int {
	if manager == nil {
		return 0
	}
	return len(manager.proxies)
}

// routeRules 在配置完成引用和协议兼容性校验后，提取运行时匹配所需字段。
func routeRules(configs []config.RouteConfig) []route.Rule {
	rules := make([]route.Rule, 0, len(configs))
	for _, cfg := range configs {
		rule := route.Rule{Type: cfg.Type, Inbounds: cfg.Inbound}
		if cfg.Type == "relay" {
			rule.Outbound = cfg.Relay.Outbound
		} else {
			rule.Outbound = cfg.Proxy.DefaultOutbound
			for _, p := range cfg.Proxy.Policies {
				rule.Policies = append(rule.Policies, route.Policy{Domains: p.Domain, Outbound: p.Outbound})
			}
		}
		rules = append(rules, rule)
	}
	return rules
}
