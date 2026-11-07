package proxy

import (
    "fmt"
    "net"
)

// ProxyManager 管理多个 TCP 代理。
type ProxyManager struct {
    proxy []*Proxy
}

// NewProxyManager 创建代理管理器。
func NewProxyManager(configs []ProxyConfig, trafficManager *TrafficManager) (*ProxyManager, error) {

    if trafficManager == nil {
        return nil, fmt.Errorf("traffic manager is nil")
    }

    if len(configs) == 0 {
        return nil, fmt.Errorf("no proxy configured")
    }

    manager := &ProxyManager{
        proxy: make([]*Proxy, 0, len(configs)),
    }

    // 防止多个代理监听同一个地址。
    listenSet := make(map[string]struct{})

    for _, cfg := range configs {

        if cfg.Listen == "" {
            return nil, fmt.Errorf(
                "proxy %q listen address is empty",
                cfg.Name,
            )
        }

        if cfg.Upstream.Url == "" {
            return nil, fmt.Errorf(
                "proxy %q upstream address is empty",
                cfg.Name,
            )
        }

        if _, exists := listenSet[cfg.Listen]; exists {
            return nil, fmt.Errorf(
                "duplicate proxy listen address: %s",
                cfg.Listen,
            )
        }

        // 检查监听地址格式。
        if _, err := net.ResolveTCPAddr("tcp", cfg.Listen); err != nil {
            return nil, fmt.Errorf(
                "proxy %q invalid listen address: %w",
                cfg.Name,
                err,
            )
        }

        // 检查上游地址格式。
        if _, err := net.ResolveTCPAddr("tcp", cfg.Upstream.Url); err != nil {
            return nil, fmt.Errorf(
                "proxy %q invalid upstream address: %w",
                cfg.Name,
                err,
            )
        }

        listenSet[cfg.Listen] = struct{}{}

        manager.proxy = append(
            manager.proxy,
            NewProxy(cfg, trafficManager),
        )
    }

    return manager, nil
}

// Start 启动所有代理。
func (m *ProxyManager) Start() {
    for _, proxy := range m.proxy {
        p := proxy

        go p.Run()
    }
}

// Count 获取代理数量。
func (m *ProxyManager) Count() int {
    return len(m.proxy)
}