
package proxy

import (
    "context"
    "fmt"
    "net"
    "strings"
    "sync"
    "time"
)

type dnsEntry struct {
    ips       []net.IPAddr
    expiresAt time.Time
}

type dnsCall struct {
    done chan struct{}
    ips  []net.IPAddr
    err  error
}

// DNSResolver 负责域名解析和可选缓存。
type DNSResolver struct {
    resolver *net.Resolver

    // ttl 为 0 时，不启用缓存。
    ttl time.Duration

    mu       sync.Mutex
    cache    map[string]dnsEntry
    inflight map[string]*dnsCall
}

func NewDNSResolver(cfg *DNSConfig) (*DNSResolver, error) {
    r := &DNSResolver{
        resolver: net.DefaultResolver,
    }

    // 未配置 DNS：不启用缓存。
    if cfg == nil {
        return r, nil
    }

    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    ttl, err := time.ParseDuration(cfg.CacheTTL)
    if err != nil {
        return nil, err
    }

    r.ttl = ttl
    r.cache = make(map[string]dnsEntry)
    r.inflight = make(map[string]*dnsCall)

    return r, nil
}

func cloneIPAddrs(ips []net.IPAddr) []net.IPAddr {
    result := make([]net.IPAddr, len(ips))

    for i, addr := range ips {
        result[i] = net.IPAddr{
            IP:   append(net.IP(nil), addr.IP...),
            Zone: addr.Zone,
        }
    }

    return result
}

// Lookup 查询域名对应的 IP。
func (r *DNSResolver) Lookup(
    ctx context.Context,
    host string,
) ([]net.IPAddr, error) {

    if r == nil {
        return nil, fmt.Errorf("DNS resolver is nil")
    }

    host = strings.TrimSuffix(
        strings.TrimSpace(host),
        ".",
    )

    if host == "" {
        return nil, fmt.Errorf("DNS host is empty")
    }

    // 如果本身就是 IP，直接返回。
    if ip := net.ParseIP(host); ip != nil {
        return []net.IPAddr{{IP: ip}}, nil
    }

    if ctx == nil {
        ctx = context.Background()
    }

    key := strings.ToLower(host)

    // 未配置缓存：每次调用都执行 DNS 查询。
    if r.ttl == 0 {
        return r.lookupDNS(ctx, key)
    }

    // 开启缓存。
    r.mu.Lock()

    if entry, ok := r.cache[key]; ok {
        if time.Now().Before(entry.expiresAt) {
            ips := cloneIPAddrs(entry.ips)
            r.mu.Unlock()
            return ips, nil
        }

        delete(r.cache, key)
    }

    // 同一个域名只允许一个 DNS 查询。
    if call, ok := r.inflight[key]; ok {
        r.mu.Unlock()

        select {
        case <-call.done:
            return cloneIPAddrs(call.ips), call.err
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }

    call := &dnsCall{
        done: make(chan struct{}),
    }

    r.inflight[key] = call
    r.mu.Unlock()

    // DNS 查询有独立的超时时间。
    lookupCtx, cancel := context.WithTimeout(
        context.Background(),
        5*time.Second,
    )

    ips, err := r.lookupDNS(lookupCtx, key)
    cancel()

    r.mu.Lock()

    if err == nil {
        r.cache[key] = dnsEntry{
            ips:       cloneIPAddrs(ips),
            expiresAt: time.Now().Add(r.ttl),
        }
    }

    call.ips = cloneIPAddrs(ips)
    call.err = err

    delete(r.inflight, key)
    close(call.done)

    r.mu.Unlock()

    return cloneIPAddrs(ips), err
}

func (r *DNSResolver) lookupDNS(
    ctx context.Context,
    host string,
) ([]net.IPAddr, error) {

    ips, err := r.resolver.LookupIPAddr(ctx, host)
    if err != nil {
        return nil, fmt.Errorf(
            "resolve domain %q failed: %w",
            host,
            err,
        )
    }

    if len(ips) == 0 {
        return nil, fmt.Errorf(
            "resolve domain %q returned no IP",
            host,
        )
    }

    return ips, nil
}
