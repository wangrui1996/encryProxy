package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JSON 中仍使用统一的 rule 字段，Go 中根据 Type 保存为具体结构体。
// Relay 和 Proxy 必须且只能设置与 Type 对应的一项。
type RouteConfig struct {
	Type    string           `json:"type"`
	Inbound []string         `json:"inbound"`
	Relay   *RelayRuleConfig `json:"-"`
	Proxy   *ProxyRuleConfig `json:"-"`
}

type RelayRuleConfig struct {
	Outbound string `json:"outbound"`
}

type ProxyRuleConfig struct {
	DefaultOutbound string        `json:"default_outbound"`
	Policies        []ProxyPolicy `json:"policies,omitempty"`
}

// Policies 按配置顺序匹配；同一策略内的 domain 为 OR 关系。
// domain 匹配域名自身与其子域名；特殊值 "*" 匹配任意目标域名。
// 未命中时使用 DefaultOutbound。实际匹配逻辑由 route 模块实现。
type ProxyPolicy struct {
	Domain   []string `json:"domain"`
	Outbound string   `json:"outbound"`
}

func (c *RouteConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    string          `json:"type"`
		Inbound []string        `json:"inbound"`
		Rule    json.RawMessage `json:"rule"`
	}
	if err := decodeRouteJSON(data, &raw); err != nil {
		return err
	}

	next := RouteConfig{
		Type:    raw.Type,
		Inbound: raw.Inbound,
	}
	if len(raw.Rule) == 0 || bytes.Equal(bytes.TrimSpace(raw.Rule), []byte("null")) {
		return fmt.Errorf("route rule is required")
	}

	switch raw.Type {
	case "relay":
		next.Relay = &RelayRuleConfig{}
		if err := decodeRouteJSON(raw.Rule, next.Relay); err != nil {
			return fmt.Errorf("relay rule: %w", err)
		}
	case "proxy":
		next.Proxy = &ProxyRuleConfig{}
		if err := decodeRouteJSON(raw.Rule, next.Proxy); err != nil {
			return fmt.Errorf("proxy rule: %w", err)
		}
	default:
		return fmt.Errorf("unsupported route type %q", raw.Type)
	}

	*c = next
	return nil
}

func (c RouteConfig) MarshalJSON() ([]byte, error) {
	var rule interface{}
	switch c.Type {
	case "relay":
		if c.Relay == nil || c.Proxy != nil {
			return nil, fmt.Errorf("relay route requires only a relay rule")
		}
		rule = c.Relay
	case "proxy":
		if c.Proxy == nil || c.Relay != nil {
			return nil, fmt.Errorf("proxy route requires only a proxy rule")
		}
		rule = c.Proxy
	default:
		return nil, fmt.Errorf("unsupported route type %q", c.Type)
	}

	return json.Marshal(struct {
		Type    string      `json:"type"`
		Inbound []string    `json:"inbound"`
		Rule    interface{} `json:"rule"`
	}{
		Type:    c.Type,
		Inbound: c.Inbound,
		Rule:    rule,
	})
}

// 防止不同类型的 rule 字段被静默忽略。
func decodeRouteJSON(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing route JSON")
	}
	return nil
}

func (c *RouteConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("route config is nil")
	}

	if len(c.Inbound) == 0 {
		return fmt.Errorf("inbound is empty")
	}

	seen := make(map[string]struct{}, len(c.Inbound))
	for _, tag := range c.Inbound {
		if tag == "" {
			return fmt.Errorf("contains empty inbound tag")
		}
		if _, exists := seen[tag]; exists {
			return fmt.Errorf("contains duplicate inbound tag %q", tag)
		}
		seen[tag] = struct{}{}
	}

	switch c.Type {
	case "relay":
		if c.Relay == nil || c.Proxy != nil {
			return fmt.Errorf("relay route requires only a relay rule")
		}
		if c.Relay.Outbound == "" {
			return fmt.Errorf("outbound is empty")
		}
	case "proxy":
		if c.Proxy == nil || c.Relay != nil {
			return fmt.Errorf("proxy route requires only a proxy rule")
		}
		if c.Proxy.DefaultOutbound == "" {
			return fmt.Errorf("default outbound is empty")
		}
		for i := range c.Proxy.Policies {
			policy := &c.Proxy.Policies[i]
			if policy.Outbound == "" {
				return fmt.Errorf("policies[%d] outbound is empty", i)
			}
			if len(policy.Domain) == 0 {
				return fmt.Errorf("policies[%d] has no domain condition", i)
			}
			for _, domain := range policy.Domain {
				if strings.TrimSpace(domain) == "" {
					return fmt.Errorf("policies[%d] contains empty domain", i)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported route type %q", c.Type)
	}

	return nil
}
