package core

import (
    "context"
    "net"
)

// DNSResolver 定义 DNS 查询能力。
type DNSResolver interface {
    Lookup(ctx context.Context, host string) ([]net.IPAddr, error)
}

type TrafficRecorder interface {
    // 根据 core 实际使用的统计操作定义方法。
}