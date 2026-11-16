package config

import (
	"fmt"
)

type OutboundConfig struct {
	Tag         string             `json:"tag"`
	Protocol    string             `json:"protocol"`
	Server      string             `json:"server"`
	Crypto      *CryptoConfig      `json:"crypto,omitempty"`
	HTTP        *HTTPConfig        `json:"http,omitempty"`
	Shadowsocks *ShadowsocksConfig `json:"shadowsocks,omitempty"`
}

func (c *OutboundConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("outbound config is nil")
	}

	if c.Tag == "freedom" || c.Tag == "blackhole" {
		return fmt.Errorf("outbound tag %q is built in; remove its outbounds entry", c.Tag)
	}
	if c.Protocol == "freedom" || c.Protocol == "blackhole" || c.Protocol == "direct" {
		return fmt.Errorf("use built-in freedom or blackhole directly in routes instead of configuring protocol %q", c.Protocol)
	}
	if c.Tag == "" {
		return fmt.Errorf("tag is empty")
	}

	if err := validateProtocol(c.Protocol, c.HTTP, c.Shadowsocks, true); err != nil {
		return err
	}

	if c.Protocol == "direct" || c.Protocol == "freedom" || c.Protocol == "blackhole" {
		if c.Server != "" || c.Crypto != nil {
			return fmt.Errorf("%s outbound must not configure server or crypto", c.Protocol)
		}
	} else if err := validateOutboundAddr(c.Server); err != nil {
		return fmt.Errorf("invalid server address %q: %w", c.Server, err)
	}

	if err := c.Crypto.Validate(); err != nil {
		return fmt.Errorf("crypto: %w", err)
	}

	if c.Crypto != nil && c.Crypto.AuthRequired {
		if c.Crypto.Account == "" || c.Crypto.Passwd == "" {
			return fmt.Errorf("crypto authentication requires account and passwd")
		}
	}

	if c.Crypto != nil && !c.Crypto.AuthRequired &&
		(c.Crypto.Account != "" || c.Crypto.Passwd != "") {
		return fmt.Errorf("crypto credentials require auth_required=true")
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

// BuiltinOutbounds returns fresh built-in values; callers cannot change global state.
func BuiltinOutbounds() map[string]*OutboundConfig {
	return map[string]*OutboundConfig{
		"freedom":   {Tag: "freedom", Protocol: "freedom"},
		"blackhole": {Tag: "blackhole", Protocol: "blackhole"},
	}
}

// BuildOutboundMap is shared by config validation and route construction.
func BuildOutboundMap(outbounds []OutboundConfig) (map[string]*OutboundConfig, error) {
	if err := validateOutboundList(outbounds); err != nil {
		return nil, err
	}
	result := BuiltinOutbounds()
	for i := range outbounds {
		out := outbounds[i]
		result[out.Tag] = &out
	}
	return result, nil
}
