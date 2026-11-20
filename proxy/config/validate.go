package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DNS 配置校验。
func (c *DNSConfig) Validate() error {
	// DNS 配置允许不配置。
	if c == nil {
		return nil
	}

	if strings.TrimSpace(c.CacheTTL) == "" {
		return fmt.Errorf("cache_ttl is required")
	}

	ttl, err := time.ParseDuration(c.CacheTTL)
	if err != nil {
		return fmt.Errorf("invalid cache_ttl %q: %w", c.CacheTTL, err)
	}

	if ttl <= 0 {
		return fmt.Errorf("cache_ttl must be greater than zero")
	}
	if c.CacheMaxBytes < 0 {
		return fmt.Errorf("cache_max_bytes must not be negative")
	}
	if c.CacheFile != "" && strings.TrimSpace(c.CacheFile) == "" {
		return fmt.Errorf("cache_file must not be blank")
	}
	if c.CacheFile == "" && c.CacheMaxBytes != 0 {
		return fmt.Errorf("cache_max_bytes requires cache_file")
	}
	if c.CacheFile != "" && c.CacheMaxBytes > 0 && c.CacheMaxBytes < 128 {
		return fmt.Errorf("cache_max_bytes must be at least 128")
	}

	return nil
}

// Web 配置校验。
func (c *WebConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("web config is nil")
	}

	// Web 未启用时，不要求配置监听地址。
	if !c.Enabled {
		return nil
	}

	if strings.TrimSpace(c.User) == "" || c.Passwd == "" {
		return fmt.Errorf("web user and passwd are required when enabled")
	}

	if err := validateListenAddr(c.Listen); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", c.Listen, err)
	}

	return nil
}

// 校验 TCP 监听地址。
// 允许 :8080 这种监听所有网卡的写法。
func validateListenAddr(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	// Listen 地址允许 host 为空，例如 :8080。
	_ = host

	return validatePort(port)
}

// 校验上游服务器地址。
// Outbound 必须指定有效的主机名或 IP 地址。
func validateOutboundAddr(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	if host == "" {
		return fmt.Errorf("server host is empty")
	}

	return validatePort(port)
}

// 校验端口号。
func validatePort(port string) error {
	if port == "" {
		return fmt.Errorf("port is empty")
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q", port)
	}

	if portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("port out of range: %d", portNumber)
	}

	return nil
}

// Log 配置校验。空 level 由日志初始化层按默认等级处理。
func (c *LogConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("log config is nil")
	}

	switch c.Level {
	case "", "panic", "fatal", "error", "warn", "warning", "info", "debug", "trace":
		return nil
	default:
		return fmt.Errorf("unsupported log level %q", c.Level)
	}
}
