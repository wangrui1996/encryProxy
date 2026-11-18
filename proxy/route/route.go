package route

import (
	"fmt"
	"strings"
)

// Rule 描述同一组入口的路由。Outbound 是固定 TCP 出口或代理默认出口。
type Rule struct {
	Type     string
	Inbounds []string
	Outbound string
	Policies []Policy
}

type Policy struct {
	Domains  []string
	Outbound string
}

// Manager 保存已编译规则，仅向调用方返回标签。
type Manager struct{ entries map[string]Rule }

func NewManager(rules []Rule) (*Manager, error) {
	m := &Manager{entries: make(map[string]Rule)}
	for i, rule := range rules {
		if len(rule.Inbounds) == 0 || rule.Outbound == "" {
			return nil, fmt.Errorf("route[%d]: missing inbound or outbound", i)
		}
		switch rule.Type {
		case "relay":
			if len(rule.Policies) != 0 {
				return nil, fmt.Errorf("route[%d]: unexpected relay policies", i)
			}
		case "proxy":
		default:
			return nil, fmt.Errorf("route[%d]: unknown type %q", i, rule.Type)
		}
		entry := Rule{Type: rule.Type, Outbound: rule.Outbound}
		for j, p := range rule.Policies {
			if len(p.Domains) == 0 || p.Outbound == "" {
				return nil, fmt.Errorf("route[%d] policy[%d]: missing domain or outbound", i, j)
			}
			compiled := Policy{Outbound: p.Outbound}
			for _, d := range p.Domains {
				if strings.TrimSpace(d) == "" {
					return nil, fmt.Errorf("route[%d] policy[%d]: empty domain", i, j)
				}
				compiled.Domains = append(compiled.Domains, normalizeDomain(d))
			}
			entry.Policies = append(entry.Policies, compiled)
		}
		for _, tag := range rule.Inbounds {
			if tag == "" {
				return nil, fmt.Errorf("route[%d]: empty inbound", i)
			}
			if _, exists := m.entries[tag]; exists {
				return nil, fmt.Errorf("duplicate route for inbound %q", tag)
			}
			m.entries[tag] = entry
		}
	}
	return m, nil
}

func (m *Manager) ResolveRelay(inboundTag string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.Type != "relay" {
		return "", fmt.Errorf("no relay route for inbound %q", inboundTag)
	}
	return entry.Outbound, nil
}

func normalizeDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// ResolveProxy 按策略顺序匹配，未命中时使用必填的 freedom/blackhole 默认出站。
// example.com 可命中 a.example.com，不会命中 fakeexample.com。
func (m *Manager) ResolveProxy(inboundTag, hostname string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.Type != "proxy" {
		return "", fmt.Errorf("no proxy route for inbound %q", inboundTag)
	}
	host := normalizeDomain(hostname)
	for _, policy := range entry.Policies {
		for _, domain := range policy.Domains {
			if domain == "*" || host == domain || strings.HasSuffix(host, "."+domain) {
				return policy.Outbound, nil
			}
		}
	}
	return entry.Outbound, nil
}
