package config

import "fmt"

// HTTPConfig 配置 HTTP 代理；HTTPS 目标通过 CONNECT 隧道访问。
// 入站账号来自 accounts；出站账号只用于登录上游。
type HTTPConfig struct {
	AuthRequired bool   `json:"auth_required"`
	Account      string `json:"account,omitempty"`
	Passwd       string `json:"passwd,omitempty"`
}

// Shadowsocks 自带密码认证，与外层 crypto 独立，可同时配置。
// 此处采用传统 AEAD 方法，不定义额外的用户名认证扩展。
type ShadowsocksConfig struct {
	Method   string `json:"method"`
	Password string `json:"password"`
}

func (c *ShadowsocksConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("shadowsocks config is required")
	}

	switch c.Method {
	case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
	default:
		return fmt.Errorf("unsupported shadowsocks method %q", c.Method)
	}

	if c.Password == "" {
		return fmt.Errorf("shadowsocks password is empty")
	}

	return nil
}

func validateProtocol(protocol string, http *HTTPConfig, shadowsocks *ShadowsocksConfig, outbound bool) error {
	switch protocol {
	case "tcp":
		if http != nil || shadowsocks != nil {
			return fmt.Errorf("tcp must not configure http or shadowsocks")
		}
	case "freedom", "blackhole", "direct":
		if !outbound {
			return fmt.Errorf("%s is only supported for outbounds", protocol)
		}
		if http != nil || shadowsocks != nil {
			return fmt.Errorf("%s must not configure http or shadowsocks", protocol)
		}
	case "http":
		if http == nil {
			return fmt.Errorf("http config is required")
		}
		if shadowsocks != nil {
			return fmt.Errorf("http must not configure shadowsocks")
		}
		if outbound {
			if http.AuthRequired && (http.Account == "" || http.Passwd == "") {
				return fmt.Errorf("http authentication requires account and passwd")
			}
			if !http.AuthRequired && (http.Account != "" || http.Passwd != "") {
				return fmt.Errorf("outbound http credentials require auth_required=true")
			}
		} else if http.Account != "" || http.Passwd != "" {
			return fmt.Errorf("inbound http credentials must be configured in accounts")
		}
	case "shadowsocks":
		if http != nil {
			return fmt.Errorf("shadowsocks must not configure http")
		}
		return shadowsocks.Validate()
	default:
		return fmt.Errorf("unsupported protocol %q", protocol)
	}

	return nil
}
