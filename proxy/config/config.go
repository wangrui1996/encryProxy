package config

import (
	"fmt"
)

// Config 是 spider 的完整运行配置。
type Config struct {
	Log       LogConfig        `json:"log"`
	Inbounds  []InboundConfig  `json:"inbounds"`
	DNS       *DNSConfig       `json:"dns,omitempty"`
	Web       WebConfig        `json:"web"`
	Outbounds []OutboundConfig `json:"outbounds"`
	Accounts  []AccountConfig  `json:"accounts"`
	Route     []RouteConfig    `json:"route"`
}

type LogConfig struct {
	Level  string `json:"level"`
	Path   string `json:"path"`
	Access string `json:"access"`
}

type DNSConfig struct {
	CacheTTL      string `json:"cache_ttl"`
	CacheFile     string `json:"cache_file,omitempty"`
	CacheMaxBytes int64  `json:"cache_max_bytes,omitempty"`
}

type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
	User    string `json:"user"`
	Passwd  string `json:"passwd"`
}

func (cfg *Config) Validate() error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if err := cfg.Log.Validate(); err != nil {
		return fmt.Errorf("log: %w", err)
	}

	// DNS
	if err := cfg.DNS.Validate(); err != nil {
		return fmt.Errorf("dns: %w", err)
	}

	// Web
	if err := cfg.Web.Validate(); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	// Inbounds
	if err := validateInboundList(cfg.Inbounds); err != nil {
		return err
	}

	// Outbounds
	if err := validateOutboundList(cfg.Outbounds); err != nil {
		return err
	}

	// Accounts
	if err := validateAccountList(cfg.Accounts); err != nil {
		return err
	}

	// Route
	for i := range cfg.Route {
		if err := cfg.Route[i].Validate(); err != nil {
			return fmt.Errorf("route[%d]: %w", i, err)
		}
	}

	// 跨配置引用校验
	if err := cfg.validateReferences(); err != nil {
		return err
	}

	return nil
}

// 校验配置之间的引用关系。
func (cfg *Config) validateReferences() error {
	inboundTags := make(map[string]*InboundConfig, len(cfg.Inbounds))
	// 配置层只校验引用是否存在、协议是否兼容，不管理运行时 outbound。
	// 出站管理器构建运行时索引，route 模块只选择出站标签。
	outboundProtocols := make(map[string]string, len(cfg.Outbounds)+2)
	outboundProtocols["freedom"] = "freedom"
	outboundProtocols["blackhole"] = "blackhole"
	for i := range cfg.Outbounds {
		out := &cfg.Outbounds[i]
		outboundProtocols[out.Tag] = out.Protocol
	}

	for i := range cfg.Inbounds {
		inboundTags[cfg.Inbounds[i].Tag] = &cfg.Inbounds[i]
	}

	routed := make(map[string]bool, len(cfg.Inbounds))
	for i := range cfg.Route {
		route := &cfg.Route[i]
		for _, tag := range route.Inbound {
			inbound, exists := inboundTags[tag]
			if !exists {
				return fmt.Errorf("route[%d] references unknown inbound %q", i, tag)
			}
			if routed[tag] {
				return fmt.Errorf("inbound %q appears in more than one route", tag)
			}
			routed[tag] = true
			if (route.Type == "relay") != (inbound.Protocol == "tcp") {
				return fmt.Errorf("route[%d] type %q does not match inbound %q protocol %q", i, route.Type, tag, inbound.Protocol)
			}
		}

		var targets []string
		if route.Type == "relay" {
			targets = append(targets, route.Relay.Outbound)
		} else {
			targets = append(targets, route.Proxy.DefaultOutbound)
			for _, policy := range route.Proxy.Policies {
				targets = append(targets, policy.Outbound)
			}
		}
		for _, tag := range targets {
			protocol, exists := outboundProtocols[tag]
			if !exists {
				return fmt.Errorf("route[%d] references unknown outbound %q", i, tag)
			}
			// Relay 没有动态目标地址；TCP 出站也不传递代理目标地址。
			if route.Type == "relay" && protocol != "tcp" {
				return fmt.Errorf("relay route[%d] requires tcp outbound %q", i, tag)
			}
			if route.Type == "proxy" {
				switch protocol {
				case "freedom", "blackhole", "tcp", "http", "shadowsocks":
					// 支持所有可配置的出站协议；TCP 仅连接固定的 Server。
				default:
					return fmt.Errorf("proxy route[%d]: outbound %q protocol %q is not implemented for proxy routing", i, tag, protocol)
				}
			}
		}
	}

	hasCryptoAccount := false
	hasProxyAccount := false
	for i := range cfg.Accounts {
		hasCryptoAccount = hasCryptoAccount || cfg.Accounts[i].CryptoEnable
		hasProxyAccount = hasProxyAccount || cfg.Accounts[i].ProxyEnable
	}

	for i := range cfg.Inbounds {
		inbound := &cfg.Inbounds[i]
		if inbound.Crypto != nil && inbound.Crypto.AuthRequired && !hasCryptoAccount {
			return fmt.Errorf("inbound %q requires a crypto-enabled account", inbound.Tag)
		}
		if inbound.HTTP != nil && inbound.HTTP.AuthRequired && !hasProxyAccount {
			return fmt.Errorf("inbound %q requires a proxy-enabled account", inbound.Tag)
		}
	}

	return nil
}
