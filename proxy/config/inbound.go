package config

import (
    "fmt"
)

type InboundConfig struct {
    Tag         string             `json:"tag"`
    Listen      string             `json:"listen"`
    Protocol    string             `json:"protocol"`
    Crypto      *CryptoConfig      `json:"crypto,omitempty"`
    HTTP        *HTTPConfig        `json:"http,omitempty"`
    Shadowsocks *ShadowsocksConfig `json:"shadowsocks,omitempty"`
    RateLimit   RateLimitConfig    `json:"rate_limit"`
}

func (c *InboundConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("inbound config is nil")
    }

    if c.Tag == "" {
        return fmt.Errorf("tag is empty")
    }

    if err := validateListenAddr(c.Listen); err != nil {
        return fmt.Errorf("invalid listen address %q: %w", c.Listen, err)
    }

    if err := validateProtocol(c.Protocol, c.HTTP, c.Shadowsocks, false); err != nil {
        return err
    }

    if c.Crypto != nil && (c.Crypto.Account != "" || c.Crypto.Passwd != "") {
        return fmt.Errorf("inbound crypto credentials must be configured in accounts")
    }

    if err := c.Crypto.Validate(); err != nil {
        return fmt.Errorf("crypto: %w", err)
    }

    if err := c.RateLimit.Validate(); err != nil {
        return fmt.Errorf("rate limit: %w", err)
    }

    return nil
}

func validateInboundList(inbounds []InboundConfig) error {
    tags := make(map[string]struct{}, len(inbounds))
    listens := make(map[string]struct{}, len(inbounds))

    for i := range inbounds {
        inbound := &inbounds[i]

        if err := inbound.Validate(); err != nil {
            return fmt.Errorf("inbounds[%d]: %w", i, err)
        }

        if _, exists := tags[inbound.Tag]; exists {
            return fmt.Errorf("duplicate inbound tag %q", inbound.Tag)
        }
        tags[inbound.Tag] = struct{}{}

        if _, exists := listens[inbound.Listen]; exists {
            return fmt.Errorf("duplicate inbound listen address %q", inbound.Listen)
        }
        listens[inbound.Listen] = struct{}{}
    }

    return nil
}
