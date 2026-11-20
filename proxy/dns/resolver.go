package dns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	dnsCleanupInterval = time.Minute
	dnsRefreshRetry    = 30 * time.Second
	dnsMaxStaleAge     = 24 * time.Hour
)

type dnsEntry struct {
	ips       []net.IPAddr
	expiresAt time.Time
	retryAt   time.Time
}

type dnsCall struct {
	done chan struct{}
	ips  []net.IPAddr
	err  error
}

// Resolver 负责域名解析、过期后异步刷新和可选的磁盘缓存。
type Resolver struct {
	resolver *net.Resolver

	// ttl 为 0 时，不启用缓存。
	ttl time.Duration

	mu       sync.Mutex
	cache    map[string]dnsEntry
	inflight map[string]*dnsCall
	revision uint64
	savedRev uint64

	cacheFile     string
	cacheMaxBytes int64

	doneCh   chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	queries  sync.WaitGroup
	stopOnce sync.Once
}

// NewResolver 保留原构造入口；不启用磁盘持久化。
func NewResolver(ttl time.Duration) (*Resolver, error) {
	return NewResolverWithCache(ttl, "", 0)
}

// NewResolverWithCache 为 DNS 记录启用可选持久化；maxBytes 为 0 时使用默认大小。
func NewResolverWithCache(ttl time.Duration, cacheFile string, maxBytes int64) (*Resolver, error) {
	if ttl < 0 {
		return nil, fmt.Errorf("negative DNS cache TTL: %s", ttl)
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("negative DNS cache file limit: %d", maxBytes)
	}
	if cacheFile == "" && maxBytes != 0 {
		return nil, fmt.Errorf("DNS cache file limit requires a cache file")
	}
	if cacheFile != "" {
		if ttl == 0 {
			return nil, fmt.Errorf("DNS cache file requires a positive TTL")
		}
		if maxBytes == 0 {
			maxBytes = defaultCacheMaxBytes
		}
		if maxBytes < minCacheMaxBytes {
			return nil, fmt.Errorf("DNS cache file limit must be at least %d bytes", minCacheMaxBytes)
		}
	}

	r := &Resolver{resolver: net.DefaultResolver, ttl: ttl, cacheFile: cacheFile, cacheMaxBytes: maxBytes}
	if ttl == 0 {
		return r, nil
	}

	r.cache = make(map[string]dnsEntry)
	r.inflight = make(map[string]*dnsCall)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.doneCh = make(chan struct{})

	if cacheFile != "" {
		if err := r.loadCache(); err != nil {
			log.Printf("[warn] load DNS cache %q: %v; starting with empty cache", cacheFile, err)
			// 旧文件可能超限或损坏，下一次持久化将其替换为有效的限额文件。
			r.revision++
		}
	}

	go r.cleanupLoop()
	return r, nil
}

// cleanupLoop 保留短期过期记录以便立即返回旧 IP，定期持久化更新。
func (r *Resolver) cleanupLoop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(dnsCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.cleanupExpired()
			if err := r.saveCache(); err != nil {
				log.Printf("[warn] save DNS cache %q: %v", r.cacheFile, err)
			}
		case <-r.ctx.Done():
			return
		}
	}
}

// cleanupExpired 只删除超过最长容忍时间的过期记录，允许短期过期记录后台刷新。
func (r *Resolver) cleanupExpired() {
	if r == nil {
		return
	}

	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for host, entry := range r.cache {
		if !now.Before(entry.expiresAt.Add(dnsMaxStaleAge)) {
			delete(r.cache, host)
			r.revision++
		}
	}
}

// Close 停止查询和清理协程，等待完成后保存最后一次 DNS 缓存快照。
func (r *Resolver) Close() {
	if r == nil || r.cancel == nil {
		return
	}

	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.cancel()
		r.mu.Unlock()
		<-r.doneCh
		r.queries.Wait()
		if err := r.saveCache(); err != nil {
			log.Printf("[warn] save DNS cache %q on close: %v", r.cacheFile, err)
		}
	})
}

func cloneIPAddrs(ips []net.IPAddr) []net.IPAddr {
	result := make([]net.IPAddr, len(ips))
	for i, addr := range ips {
		result[i] = net.IPAddr{IP: append(net.IP(nil), addr.IP...), Zone: addr.Zone}
	}
	return result
}

// Lookup 优先返回已有缓存；缓存过期时立即返回旧 IP 并异步刷新。
func (r *Resolver) Lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if r == nil {
		return nil, fmt.Errorf("DNS resolver is nil")
	}
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, fmt.Errorf("DNS host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	key := strings.ToLower(host)
	if r.ttl == 0 {
		return r.lookupDNS(ctx, key)
	}

	r.mu.Lock()
	if err := r.ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}

	now := time.Now()
	if entry, ok := r.cache[key]; ok {
		if now.Before(entry.expiresAt.Add(dnsMaxStaleAge)) {
			if !now.Before(entry.expiresAt) && !now.Before(entry.retryAt) {
				r.startLookupLocked(key)
			}
			ips := cloneIPAddrs(entry.ips)
			r.mu.Unlock()
			return ips, nil
		}
		delete(r.cache, key)
		r.revision++
	}

	// 首次查询仍等待结果；同一个域名的并发查询只发起一次请求。
	call := r.startLookupLocked(key)
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return cloneIPAddrs(call.ips), call.err
	}
}

// 调用者必须持有 r.mu；每个域名至多一个刷新协程。
func (r *Resolver) startLookupLocked(key string) *dnsCall {
	if call := r.inflight[key]; call != nil {
		return call
	}
	call := &dnsCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.queries.Add(1)
	go r.lookupShared(key, call)
	return call
}

func (r *Resolver) lookupShared(key string, call *dnsCall) {
	defer r.queries.Done()
	lookupCtx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	ips, err := r.lookupDNS(lookupCtx, key)
	cancel()

	r.mu.Lock()
	if err == nil {
		r.cache[key] = dnsEntry{ips: cloneIPAddrs(ips), expiresAt: time.Now().Add(r.ttl)}
		r.revision++
	} else if entry, ok := r.cache[key]; ok {
		// 刷新失败时保留短期旧 IP，限制失败重试频率。
		entry.retryAt = time.Now().Add(dnsRefreshRetry)
		r.cache[key] = entry
	}
	call.ips = cloneIPAddrs(ips)
	call.err = err
	delete(r.inflight, key)
	close(call.done)
	r.mu.Unlock()
}

func (r *Resolver) lookupDNS(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := r.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve domain %q failed: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve domain %q returned no IP", host)
	}
	return ips, nil
}
