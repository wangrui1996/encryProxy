
package route

import (
	"fmt"

	"wangjui.com/enproxy/proxy/config"
)

// Manager 根据 Inbound Tag 选择 Outbound。
type Manager struct {
	defaultOutbound *config.OutboundConfig
	rules           map[string]*config.OutboundConfig
}

func NewManager(
	cfg config.RouteConfig,
	inbounds []config.InboundConfig,
	outbounds []config.OutboundConfig,
) (*Manager, error) {
	outboundMap := make(map[string]*config.OutboundConfig, len(outbounds))

	for i := range outbounds {
		outbound := outbounds[i]

		if outbound.Tag == "" {
			return nil, fmt.Errorf("outbound tag is empty")
		}

		if _, exists := outboundMap[outbound.Tag]; exists {
			return nil, fmt.Errorf("duplicate outbound tag: %s", outbound.Tag)
		}

		outboundMap[outbound.Tag] = &outbound
	}

	manager := &Manager{
		rules: make(map[string]*config.OutboundConfig),
	}

	if cfg.Default != "" {
		outbound, exists := outboundMap[cfg.Default]
		if !exists {
			return nil, fmt.Errorf("route default outbound %q does not exist", cfg.Default)
		}

		manager.defaultOutbound = outbound
	}

	inboundTags := make(map[string]struct{}, len(inbounds))

	for _, inbound := range inbounds {
		inboundTags[inbound.Tag] = struct{}{}
	}

	for i, rule := range cfg.Rules {
		if rule.Outbound == "" {
			return nil, fmt.Errorf("route rule %d outbound is empty", i)
		}

		outbound, exists := outboundMap[rule.Outbound]
		if !exists {
			return nil, fmt.Errorf(
				"route rule %d references unknown outbound %q",
				i,
				rule.Outbound,
			)
		}

		if len(rule.Inbound) == 0 {
			return nil, fmt.Errorf("route rule %d has no inbound tags", i)
		}

		for _, tag := range rule.Inbound {
			if _, exists := inboundTags[tag]; !exists {
				return nil, fmt.Errorf(
					"route rule %d references unknown inbound %q",
					i,
					tag,
				)
			}

			if _, exists := manager.rules[tag]; exists {
				return nil, fmt.Errorf(
					"inbound %q appears in more than one route rule",
					tag,
				)
			}

			manager.rules[tag] = outbound
		}
	}

	for _, inbound := range inbounds {
		if _, exists := manager.rules[inbound.Tag]; !exists &&
			manager.defaultOutbound == nil {
			return nil, fmt.Errorf(
				"inbound %q has no route and route.default is empty",
				inbound.Tag,
			)
		}
	}

	return manager, nil
}

// Resolve 根据 Inbound Tag 获取对应的 Outbound。
func (m *Manager) Resolve(inboundTag string) (*config.OutboundConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("route manager is nil")
	}

	if outbound, exists := m.rules[inboundTag]; exists {
		return outbound, nil
	}

	if m.defaultOutbound != nil {
		return m.defaultOutbound, nil
	}

	return nil, fmt.Errorf("no outbound route for inbound %q", inboundTag)
}
