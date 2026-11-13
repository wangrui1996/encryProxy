package config

import (
    "fmt"
)

type OutboundConfig struct {
    Tag      string                 `json:"tag"`
    Protocol string                 `json:"protocol"`
    Server   string                 `json:"server"`
    Crypto   CryptoConfig           `json:"crypto"`
    Account  *OutboundAccountConfig `json:"account,omitempty"`
}

// 只有在定义并实现具体上游认证协议后，才能真正使用该字段登录上游。
type OutboundAccountConfig struct {
    Username string `json:"username"`
    Password string `json:"password"`
}

func (c *OutboundConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("outbound config is nil")
    }

    if c.Tag == "" {
        return fmt.Errorf("tag is empty")
    }

    // 当前只支持 TCP。
    // 空值按默认 TCP 处理，但不在 Validate 中修改配置。
    if c.Protocol != "" && c.Protocol != "tcp" {
        return fmt.Errorf("unsupported protocol %q", c.Protocol)
    }

    if err := validateOutboundAddr(c.Server); err != nil {
        return fmt.Errorf("invalid server address %q: %w", c.Server, err)
    }

    if err := c.Crypto.Validate(); err != nil {
        return fmt.Errorf("crypto: %w", err)
    }

    if c.Account != nil {
        if c.Account.Username == "" {
            return fmt.Errorf("account username is empty")
        }

        if c.Account.Password == "" {
            return fmt.Errorf("account password is empty")
        }
    }

    return nil
}

func validateOutboundList(outbounds []OutboundConfig) error {
    tags := make(map[string]struct{}, len(outbounds))

    for i := range outbounds {
        outbound := &outbounds[i]

        if err := outbound.Validate(); err != nil {
            return fmt.Errorf("outbounds[%d]: %w", i, err)
        }

        if _, exists := tags[outbound.Tag]; exists {
            return fmt.Errorf("duplicate outbound tag %q", outbound.Tag)
        }

        tags[outbound.Tag] = struct{}{}
    }

    return nil
}