package route

import (
	"fmt"
	"strings"

	"wangjui.com/enproxy/proxy/config"
)

// proxyPolicy 为启动时编译完成的规则；无需每次连接都通过 tag 查找 outbound。
type proxyPolicy struct {
	domains  []string
	outbound *config.OutboundConfig
}

type routeEntry struct {
	kind            string
	relay           *config.OutboundConfig
	defaultOutbound *config.OutboundConfig
	policies        []proxyPolicy
}

// Manager 负责路由匹配和选择 outbound，不负责 DNS、拨号或协议握手。
// 初始化后只读，可以被所有入站连接并发使用。
type Manager struct {
	entries map[string]routeEntry
}

func NewManager(routes []config.RouteConfig, inbounds []config.InboundConfig, outbounds []config.OutboundConfig) (*Manager, error) {
	inboundMap := make(map[string]config.InboundConfig, len(inbounds))
	for _, in := range inbounds {
		if _, exists := inboundMap[in.Tag]; exists {
			return nil, fmt.Errorf("duplicate inbound %q", in.Tag)
		}
		inboundMap[in.Tag] = in
	}

	// outbound registry 仅用于构建路由，运行时查询直接返回编译好的指针。
	index, err := buildOutboundIndex(outbounds)
	if err != nil {
		return nil, err
	}
	m := &Manager{entries: make(map[string]routeEntry, len(inbounds))}

	for i := range routes {
		r := &routes[i]
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("route[%d]: %w", i, err)
		}
		entry := routeEntry{kind: r.Type}
		switch r.Type {
		case "relay":
			out, err := findOutbound(index, r.Relay.Outbound)
			if err != nil {
				return nil, fmt.Errorf("route[%d]: relay: %w", i, err)
			}
			if out.Protocol != "tcp" {
				return nil, fmt.Errorf("route[%d]: relay needs tcp outbound %q", i, out.Tag)
			}
			entry.relay = out

		case "proxy":
			defaultOut, err := findOutbound(index, r.Proxy.DefaultOutbound)
			if err != nil {
				return nil, fmt.Errorf("route[%d]: default: %w", i, err)
			}
			if defaultOut.Protocol != "freedom" && defaultOut.Protocol != "blackhole" {
				return nil, fmt.Errorf("route[%d]: proxy default outbound %q must be freedom or blackhole", i, defaultOut.Tag)
			}
			entry.defaultOutbound = defaultOut

			entry.policies = make([]proxyPolicy, 0, len(r.Proxy.Policies))
			for j, rule := range r.Proxy.Policies {
				out, err := findOutbound(index, rule.Outbound)
				if err != nil {
					return nil, fmt.Errorf("route[%d] policies[%d]: %w", i, j, err)
				}
				switch out.Protocol {
				case "freedom", "blackhole", "http", "shadowsocks":
				default:
					return nil, fmt.Errorf("route[%d]: proxy outbound %q protocol %q unsupported", i, out.Tag, out.Protocol)
				}
				policy := proxyPolicy{outbound: out, domains: make([]string, len(rule.Domain))}
				for k, domain := range rule.Domain {
					policy.domains[k] = normalizeDomain(domain)
				}
				entry.policies = append(entry.policies, policy)
			}
		default:
			return nil, fmt.Errorf("route[%d]: unsupported route type %q", i, r.Type)
		}

		for _, tag := range r.Inbound {
			in, ok := inboundMap[tag]
			if !ok {
				return nil, fmt.Errorf("route[%d]: unknown inbound %q", i, tag)
			}
			if _, exists := m.entries[tag]; exists {
				return nil, fmt.Errorf("duplicate route for inbound %q", tag)
			}
			if (r.Type == "relay") != (in.Protocol == "tcp") {
				return nil, fmt.Errorf("route[%d]: incompatible inbound %s protocol %q", i, tag, in.Protocol)
			}
			m.entries[tag] = entry
		}
	}
	for _, in := range inbounds {
		if _, ok := m.entries[in.Tag]; !ok {
			return nil, fmt.Errorf("inbound %q has no route", in.Tag)
		}
	}
	return m, nil
}

func (m *Manager) ResolveRelay(inboundTag string) (*config.OutboundConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.kind != "relay" {
		return nil, fmt.Errorf("no relay route for inbound %q", inboundTag)
	}
	return entry.relay, nil
}

func normalizeDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// ResolveProxy 按策略顺序匹配，未命中时使用必填的 freedom/blackhole 默认出站。
// example.com 可命中 a.example.com，不会命中 fakeexample.com。
func (m *Manager) ResolveProxy(inboundTag, hostname string) (*config.OutboundConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.kind != "proxy" {
		return nil, fmt.Errorf("no proxy route for inbound %q", inboundTag)
	}
	host := normalizeDomain(hostname)
	for _, policy := range entry.policies {
		for _, domain := range policy.domains {
			if domain == "*" || host == domain || strings.HasSuffix(host, "."+domain) {
				return policy.outbound, nil
			}
		}
	}
	return entry.defaultOutbound, nil
}
