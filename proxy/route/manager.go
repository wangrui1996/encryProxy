package route

import (
	"fmt"
	"strings"

	"wangjui.com/enproxy/proxy/config"
)

type routeEntry struct {
	kind  string
	relay *config.OutboundConfig
	proxy *config.ProxyRuleConfig
}

// Manager 只负责选择出站；连接建立和协议握手由 protocol 负责。
type Manager struct {
	entries   map[string]routeEntry
	outbounds map[string]*config.OutboundConfig
}

func NewManager(routes []config.RouteConfig, inbounds []config.InboundConfig, outbounds []config.OutboundConfig) (*Manager, error) {
	inboundMap := make(map[string]config.InboundConfig, len(inbounds))
	for _, in := range inbounds {
		if _, exists := inboundMap[in.Tag]; exists {
			return nil, fmt.Errorf("duplicate inbound %q", in.Tag)
		}
		inboundMap[in.Tag] = in
	}
	outboundMap, err := config.BuildOutboundMap(outbounds)
	if err != nil {
		return nil, err
	}
	m := &Manager{entries: make(map[string]routeEntry, len(inbounds)), outbounds: outboundMap}
	for i := range routes {
		route := &routes[i]
		if err := route.Validate(); err != nil {
			return nil, fmt.Errorf("route[%d]: %w", i, err)
		}
		entry := routeEntry{kind: route.Type}
		switch route.Type {
		case "relay":
			out := m.outbounds[route.Relay.Outbound]
			if out == nil || out.Protocol != "tcp" {
				return nil, fmt.Errorf("route[%d]: relay needs tcp outbound %q", i, route.Relay.Outbound)
			}
			entry.relay = out
		case "proxy":
			entry.proxy = route.Proxy
			defaultOutbound := m.outbounds[route.Proxy.DefaultOutbound]
			if defaultOutbound == nil || (defaultOutbound.Protocol != "freedom" && defaultOutbound.Protocol != "blackhole") {
				return nil, fmt.Errorf("route[%d]: proxy default outbound %q must be freedom or blackhole", i, route.Proxy.DefaultOutbound)
			}
			for _, rule := range route.Proxy.Policies {
				out := m.outbounds[rule.Outbound]
				if out == nil {
					return nil, fmt.Errorf("route[%d]: unknown policy outbound %q", i, rule.Outbound)
				}
				switch out.Protocol {
				case "freedom", "blackhole", "http", "shadowsocks":
				default:
					return nil, fmt.Errorf("route[%d]: proxy outbound %q protocol %q unsupported", i, out.Tag, out.Protocol)
				}
			}
		default:
			return nil, fmt.Errorf("route[%d]: unsupported route type %q", i, route.Type)
		}
		for _, tag := range route.Inbound {
			in, ok := inboundMap[tag]
			if !ok {
				return nil, fmt.Errorf("route[%d]: unknown inbound %q", i, tag)
			}
			if _, exists := m.entries[tag]; exists {
				return nil, fmt.Errorf("duplicate route for inbound %q", tag)
			}
			if (route.Type == "relay") != (in.Protocol == "tcp") {
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

// ResolveProxy 优先按域名规则选择出站，未匹配则回退到必填的 freedom/blackhole 默认出站。
// 域名匹配严格按标签边界检查：example.com 可命中 a.example.com，不会命中 fakeexample.com。
func (m *Manager) ResolveProxy(inboundTag, hostname string) (*config.OutboundConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("route manager is nil")
	}
	entry, ok := m.entries[inboundTag]
	if !ok || entry.kind != "proxy" || entry.proxy == nil {
		return nil, fmt.Errorf("no proxy route for inbound %q", inboundTag)
	}
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	for _, policy := range entry.proxy.Policies {
		for _, domain := range policy.Domain {
			suffix := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
			if suffix == "*" || host == suffix || strings.HasSuffix(host, "."+suffix) {
				return m.outbounds[policy.Outbound], nil
			}
		}
	}
	return m.outbounds[entry.proxy.DefaultOutbound], nil
}
