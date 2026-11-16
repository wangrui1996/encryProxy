package config

import "fmt"

// CryptoConfig 存在即表示本层启用 AES-GCM；nil 表示普通明文 TCP。
// 认证凭据只允许配置在 outbound 的 crypto 下。
type CryptoConfig struct {
	Key          string `json:"key"`
	AuthRequired bool   `json:"auth_required"`
	Account      string `json:"account,omitempty"`
	Passwd       string `json:"passwd,omitempty"`
}

func (c *CryptoConfig) Validate() error {
	if c == nil {
		return nil
	}
	return validateAESKey(c.Key)
}

func validateAESKey(key string) error {
	n := len([]byte(key))
	switch n {
	case 16, 24, 32:
		return nil
	default:
		return fmt.Errorf("AES key must be 16, 24, or 32 bytes; got %d", n)
	}
}
