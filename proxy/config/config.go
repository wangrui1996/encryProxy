package config

import (
    "fmt"
)

// Config 是 enproxy 的完整运行配置。
type Config struct {
    Inbounds  []InboundConfig  `json:"inbounds"`
    DNS      *DNSConfig      `json:"dns,omitempty"`
    Web       WebConfig        `json:"web"`
    Outbounds []OutboundConfig `json:"outbounds"`
    Accounts  []AccountConfig  `json:"accounts"`
    Route     RouteConfig      `json:"route"`
}

type DNSConfig struct {
    CacheTTL string `json:"cache_ttl"`
}

type WebConfig struct {
    Enabled bool   `json:"enabled"`
    Listen  string `json:"listen"`
}

func (cfg *Config) Validate() error {
    if cfg == nil {
        return fmt.Errorf("config is nil")
    }

    if len(cfg.Inbounds) == 0 {
        return fmt.Errorf("no inbound configured")
    }

    if len(cfg.Outbounds) == 0 {
        return fmt.Errorf("no outbound configured")
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
    if err := cfg.Route.Validate(); err != nil {
        return fmt.Errorf("route: %w", err)
    }

    // 跨配置引用校验
    if err := cfg.validateReferences(); err != nil {
        return err
    }

    return nil
}

// 校验配置之间的引用关系。
func (cfg *Config) validateReferences() error {
    inboundTags := make(map[string]struct{}, len(cfg.Inbounds))
    outboundTags := make(map[string]struct{}, len(cfg.Outbounds))

    for i := range cfg.Inbounds {
        inboundTags[cfg.Inbounds[i].Tag] = struct{}{}
    }

    for i := range cfg.Outbounds {
        outboundTags[cfg.Outbounds[i].Tag] = struct{}{}
    }

    // Default 必须引用已定义的 Outbound。
    if _, exists := outboundTags[cfg.Route.Default]; !exists {
        return fmt.Errorf(
            "route default references unknown outbound %q",
            cfg.Route.Default,
        )
    }

    // Route Rules 引用检查。
    for i := range cfg.Route.Rules {
        rule := &cfg.Route.Rules[i]

        if _, exists := outboundTags[rule.Outbound]; !exists {
            return fmt.Errorf(
                "route.rules[%d] references unknown outbound %q",
                i,
                rule.Outbound,
            )
        }

        for _, tag := range rule.Inbound {
            if _, exists := inboundTags[tag]; !exists {
                return fmt.Errorf(
                    "route.rules[%d] references unknown inbound %q",
                    i,
                    tag,
                )
            }
        }
    }

    // 开启认证的 Inbound 必须有可用账号。
    hasEnabledAccount := false
    for i := range cfg.Accounts {
        if cfg.Accounts[i].Enabled {
            hasEnabledAccount = true
            break
        }
    }

    for i := range cfg.Inbounds {
        if cfg.Inbounds[i].AuthRequired && !hasEnabledAccount {
            return fmt.Errorf(
                "inbound %q requires authentication, but no enabled account is configured",
                cfg.Inbounds[i].Tag,
            )
        }
    }

    return nil
}