package route

import (
	"fmt"
	"net/netip"
	"path/filepath"
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

	// 规则仅在初始化时编译；请求匹配不遍历文件中的域名。
	exact  map[string]struct{}
	suffix map[string]struct{}
	ips    map[netip.Addr]struct{}
	cidrs  map[int]map[netip.Addr]struct{}
	any    bool
}

// Manager 保存已编译规则，仅向调用方返回标签。
type Manager struct{ entries map[string]Rule }

// baseDir 用于解析 file: 相对路径；省略时相对当前工作目录。
func NewManager(rules []Rule, baseDir ...string) (*Manager, error) {
	base := "."
	if len(baseDir) != 0 && baseDir[0] != "" {
		base = baseDir[0]
	}
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
			for _, raw := range p.Domains {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					return nil, fmt.Errorf("route[%d] policy[%d]: empty domain", i, j)
				}
				if strings.HasPrefix(raw, "file:") {
					name := strings.TrimSpace(strings.TrimPrefix(raw, "file:"))
					if name == "" {
						return nil, fmt.Errorf("route[%d] policy[%d]: file path is empty", i, j)
					}
					if !filepath.IsAbs(name) {
						name = filepath.Join(base, name)
					}
					if err := loadRuleFile(name, func(value string) error { return compiled.addRule(value, true) }); err != nil {
						return nil, fmt.Errorf("route[%d] policy[%d]: %w", i, j, err)
					}
				} else if err := compiled.addRule(raw, false); err != nil {
					return nil, fmt.Errorf("route[%d] policy[%d]: %w", i, j, err)
				}
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

func (p *Policy) addRule(value string, fromFile bool) error {
	value = strings.TrimSpace(value)
	if value == "*" {
		p.any = true
		return nil
	}
	if strings.HasPrefix(value, "ip:") && !fromFile {
		value = strings.TrimSpace(strings.TrimPrefix(value, "ip:"))
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return fmt.Errorf("invalid IP rule %q: %w", value, err)
		}
		if p.ips == nil {
			p.ips = make(map[netip.Addr]struct{})
		}
		p.ips[addr.Unmap()] = struct{}{}
		return nil
	}
	if strings.HasPrefix(value, "domain:") && !fromFile {
		value = strings.TrimSpace(strings.TrimPrefix(value, "domain:"))
	}
	if fromFile {
		// Clash domain behavior: '+.' 匹配主域名及子域名；普通条目只匹配完整域名。
		if strings.HasPrefix(value, "+.") {
			return p.addDomain(strings.TrimPrefix(value, "+."), true)
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			prefix = prefix.Masked()
			if p.cidrs == nil {
				p.cidrs = make(map[int]map[netip.Addr]struct{})
			}
			if p.cidrs[prefix.Bits()] == nil {
				p.cidrs[prefix.Bits()] = make(map[netip.Addr]struct{})
			}
			p.cidrs[prefix.Bits()][prefix.Addr()] = struct{}{}
			return nil
		}
		if addr, err := netip.ParseAddr(value); err == nil {
			if p.ips == nil {
				p.ips = make(map[netip.Addr]struct{})
			}
			p.ips[addr.Unmap()] = struct{}{}
			return nil
		}
		return p.addDomain(value, false)
	}
	// 旧规则和 domain: 前缀均保持原有的主域名及子域名匹配行为。
	return p.addDomain(value, true)
}

func (p *Policy) addDomain(value string, suffix bool) error {
	value = normalizeDomain(value)
	if value == "" || strings.ContainsAny(value, "/:* \t\r\n") || strings.HasPrefix(value, ".") || strings.Contains(value, "..") {
		return fmt.Errorf("invalid domain rule %q", value)
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return fmt.Errorf("invalid domain rule %q", value)
		}
	}
	if suffix {
		if p.suffix == nil {
			p.suffix = make(map[string]struct{})
		}
		p.suffix[value] = struct{}{}
	} else {
		if p.exact == nil {
			p.exact = make(map[string]struct{})
		}
		p.exact[value] = struct{}{}
	}
	return nil
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

func (p *Policy) matches(host string, ip netip.Addr) bool {
	if p.any {
		return true
	}
	if ip.IsValid() {
		if _, ok := p.ips[ip]; ok {
			return true
		}
		for bits, prefixes := range p.cidrs {
			if bits > ip.BitLen() {
				continue
			}
			prefix, err := ip.Prefix(bits)
			if err == nil {
				if _, ok := prefixes[prefix.Masked().Addr()]; ok {
					return true
				}
			}
		}
		return false
	}
	if _, ok := p.exact[host]; ok {
		return true
	}
	// 每次只查找域名各级后缀，而非遍历所有规则。
	for part := host; part != ""; {
		if _, ok := p.suffix[part]; ok {
			return true
		}
		i := strings.IndexByte(part, '.')
		if i < 0 {
			break
		}
		part = part[i+1:]
	}
	return false
}

// ResolveProxy 按策略顺序匹配，未命中时使用配置的默认出站。
func (m *Manager) ResolveProxy(inboundTag, hostname string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.Type != "proxy" {
		return "", fmt.Errorf("no proxy route for inbound %q", inboundTag)
	}
	host := normalizeDomain(hostname)
	ip, _ := netip.ParseAddr(host)
	ip = ip.Unmap()
	for i := range entry.Policies {
		if entry.Policies[i].matches(host, ip) {
			return entry.Policies[i].Outbound, nil
		}
	}
	return entry.Outbound, nil
}
