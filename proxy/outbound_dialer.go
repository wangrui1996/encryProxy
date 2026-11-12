
package proxy

import (
    "context"
    "fmt"
    "net"
    "sync/atomic"
    "time"
)

type OutboundDialer struct {
    dns      *DNSResolver
    dialer   net.Dialer
    nextIP   atomic.Uint64
}

func NewOutboundDialer(dns *DNSResolver) *OutboundDialer {
    return &OutboundDialer{
        dns: dns,
        dialer: net.Dialer{
            Timeout:   5 * time.Second,
            KeepAlive: 30 * time.Second,
        },
    }
}

func (d *OutboundDialer) DialContext(
    ctx context.Context,
    address string,
) (net.Conn, error) {

    if d == nil || d.dns == nil {
        return nil, fmt.Errorf("outbound dialer is not initialized")
    }

    if ctx == nil {
        ctx = context.Background()
    }

    host, port, err := net.SplitHostPort(address)
    if err != nil {
        return nil, fmt.Errorf(
            "invalid outbound address %q: %w",
            address,
            err,
        )
    }

    // IP 地址直接连接。
    if ip := net.ParseIP(host); ip != nil {
        return d.dialer.DialContext(ctx, "tcp", address)
    }

    // DNS 解析。
    ips, err := d.dns.Lookup(ctx, host)
    if err != nil {
        return nil, err
    }

    // 连接整体超时。
    dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    // 多 IP 轮询。
    start := int(d.nextIP.Add(1)-1) % len(ips)

    var lastErr error

    for i := 0; i < len(ips); i++ {
        ip := ips[(start+i)%len(ips)]

        target := net.JoinHostPort(ip.String(), port)

        attemptCtx, attemptCancel := context.WithTimeout(
            dialCtx,
            3*time.Second,
        )

        conn, dialErr := d.dialer.DialContext(
            attemptCtx,
            "tcp",
            target,
        )

        attemptCancel()

        if dialErr == nil {
            return conn, nil
        }

        lastErr = fmt.Errorf(
            "connect %s failed: %w",
            target,
            dialErr,
        )

        if dialCtx.Err() != nil {
            break
        }
    }

    if lastErr == nil {
        lastErr = fmt.Errorf("no available IP")
    }

    return nil, fmt.Errorf(
        "outbound %q connection failed: %w",
        address,
        lastErr,
    )
}
