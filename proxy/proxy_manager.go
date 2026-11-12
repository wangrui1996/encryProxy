package proxy

import (
	"fmt"
	"net"
)

// ProxyManager 管理所有 Inbound、账号、路由和 Web Dashboard。
type ProxyManager struct {
	proxies        []*Proxy
	trafficManager *TrafficManager
	webConfig      WebConfig
	webServer      *WebServer
}

func NewProxyManager(cfg Config, trafficManager *TrafficManager) (*ProxyManager, error) {
	if trafficManager == nil {
		return nil, fmt.Errorf("traffic manager is nil")
	}
	if len(cfg.Inbounds) == 0 {
		return nil, fmt.Errorf("no inbound configured")
	}
	if len(cfg.Outbounds) == 0 {
		return nil, fmt.Errorf("no outbound configured")
	}

	inboundTags := make(map[string]struct{}, len(cfg.Inbounds))
	listenSet := make(map[string]struct{}, len(cfg.Inbounds))
	for i := range cfg.Inbounds {
		inbound := &cfg.Inbounds[i]
		if inbound.Tag == "" {
			return nil, fmt.Errorf("inbound tag is empty")
		}
		if _, exists := inboundTags[inbound.Tag]; exists {
			return nil, fmt.Errorf("duplicate inbound tag: %s", inbound.Tag)
		}
		inboundTags[inbound.Tag] = struct{}{}

		if inbound.Listen == "" {
			return nil, fmt.Errorf("inbound %q listen address is empty", inbound.Tag)
		}
		if _, exists := listenSet[inbound.Listen]; exists {
			return nil, fmt.Errorf("duplicate inbound listen address: %s", inbound.Listen)
		}
		if _, err := net.ResolveTCPAddr("tcp", inbound.Listen); err != nil {
			return nil, fmt.Errorf("inbound %q invalid listen address: %w", inbound.Tag, err)
		}
		listenSet[inbound.Listen] = struct{}{}

		if inbound.Crypto.Enabled {
			if err := validateAESKey(inbound.Crypto.Key); err != nil {
				return nil, fmt.Errorf("inbound %q crypto: %w", inbound.Tag, err)
			}
		}
	}

	outboundTags := make(map[string]struct{}, len(cfg.Outbounds))
	for i := range cfg.Outbounds {
		outbound := &cfg.Outbounds[i]
		if outbound.Tag == "" {
			return nil, fmt.Errorf("outbound tag is empty")
		}
		if _, exists := outboundTags[outbound.Tag]; exists {
			return nil, fmt.Errorf("duplicate outbound tag: %s", outbound.Tag)
		}
		outboundTags[outbound.Tag] = struct{}{}

		if outbound.Protocol == "" {
			outbound.Protocol = "tcp"
		}
		if outbound.Protocol != "tcp" {
			return nil, fmt.Errorf("outbound %q unsupported protocol %q", outbound.Tag, outbound.Protocol)
		}
		if outbound.Server == "" {
			return nil, fmt.Errorf("outbound %q server address is empty", outbound.Tag)
		}
		if _, err := net.ResolveTCPAddr("tcp", outbound.Server); err != nil {
			return nil, fmt.Errorf("outbound %q invalid server address: %w", outbound.Tag, err)
		}
		if outbound.Crypto.Enabled {
			if err := validateAESKey(outbound.Crypto.Key); err != nil {
				return nil, fmt.Errorf("outbound %q crypto: %w", outbound.Tag, err)
			}
		}
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
		proxies:        make([]*Proxy, 0, len(cfg.Inbounds)),
		trafficManager: trafficManager,
		webConfig:      cfg.Web,
	}
	for _, inbound := range cfg.Inbounds {
		manager.proxies = append(manager.proxies, NewProxy(inbound, accounts, routes, trafficManager))
	}

	if cfg.Web.Enabled && cfg.Web.Listen == "" {
		return nil, fmt.Errorf("web.listen is empty while web.enabled is true")
	}
	return manager, nil
}

func validateAESKey(key string) error {
	switch len([]byte(key)) {
	case 16, 24, 32:
		return nil
	default:
		return fmt.Errorf("AES key must be 16, 24, or 32 bytes; got %d", len([]byte(key)))
	}
}

// Start 启动所有入口监听和可选 Web Dashboard。
func (m *ProxyManager) Start() {
	for _, proxy := range m.proxies {
		p := proxy
		go p.Run()
	}

	if m.webConfig.Enabled {
		m.webServer = NewWebServer(m.webConfig.Listen, m.trafficManager)
		go m.webServer.Run()
	}
}

func (m *ProxyManager) Count() int {
	return len(m.proxies)
}
