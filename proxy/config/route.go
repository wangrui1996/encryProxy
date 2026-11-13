package config

import "fmt"

type RouteConfig struct {
    Default string      `json:"default"`
    Rules   []RouteRule `json:"rules"`
}

type RouteRule struct {
    Inbound  []string `json:"inbound"`
    Outbound string   `json:"outbound"`
}

func (c *RouteConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("route config is nil")
    }

    if c.Default == "" {
        return fmt.Errorf("default outbound is empty")
    }

    for i := range c.Rules {
        rule := &c.Rules[i]

        if len(rule.Inbound) == 0 {
            return fmt.Errorf("rules[%d] inbound is empty", i)
        }

        if rule.Outbound == "" {
            return fmt.Errorf("rules[%d] outbound is empty", i)
        }

        seen := make(map[string]struct{}, len(rule.Inbound))

        for _, tag := range rule.Inbound {
            if tag == "" {
                return fmt.Errorf("rules[%d] contains empty inbound tag", i)
            }

            if _, exists := seen[tag]; exists {
                return fmt.Errorf(
                    "rules[%d] contains duplicate inbound tag %q",
                    i,
                    tag,
                )
            }

            seen[tag] = struct{}{}
        }
    }

    return nil
}