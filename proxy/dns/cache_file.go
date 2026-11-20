package dns

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultCacheMaxBytes int64 = 1 << 20 // 1 MiB
	minCacheMaxBytes     int64 = 128
	cacheFileVersion           = 1
)

type storedDNSCache struct {
	Version int              `json:"version"`
	Entries []storedDNSEntry `json:"entries"`
}

type storedDNSEntry struct {
	Host      string    `json:"host"`
	IPs       []string  `json:"ips"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loadCache 仅加载未超过最长过期容忍时间的有效地址；文件大小在读取前校验。
func (r *Resolver) loadCache() error {
	info, err := os.Stat(r.cacheFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("DNS cache path is not a regular file")
	}
	if info.Size() > r.cacheMaxBytes {
		return fmt.Errorf("DNS cache file is %d bytes, exceeds the %d-byte limit", info.Size(), r.cacheMaxBytes)
	}

	f, err := os.Open(r.cacheFile)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, r.cacheMaxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > r.cacheMaxBytes {
		return fmt.Errorf("DNS cache file grew beyond the %d-byte limit", r.cacheMaxBytes)
	}
	var stored storedDNSCache
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode DNS cache: %w", err)
	}
	if stored.Version != cacheFileVersion {
		return fmt.Errorf("unsupported DNS cache version %d", stored.Version)
	}

	now := time.Now()
	for _, entry := range stored.Entries {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Host), "."))
		if host == "" || entry.ExpiresAt.IsZero() || !now.Before(entry.ExpiresAt.Add(dnsMaxStaleAge)) || net.ParseIP(host) != nil {
			continue
		}
		// 配置变更或系统时钟回拨后，不允许持久化记录超出本次配置的 TTL。
		expiresAt := entry.ExpiresAt
		if maxExpiry := now.Add(r.ttl); expiresAt.After(maxExpiry) {
			expiresAt = maxExpiry
		}
		ips := make([]net.IPAddr, 0, len(entry.IPs))
		for _, raw := range entry.IPs {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				ips = nil
				break
			}
			ips = append(ips, net.IPAddr{IP: net.IP(addr.AsSlice()), Zone: addr.Zone()})
		}
		if len(ips) > 0 {
			r.cache[host] = dnsEntry{ips: ips, expiresAt: expiresAt}
		}
	}
	return nil
}

// saveCache 按最近刷新时间优先写入，超出限制的记录不落盘；使用原子替换避免文件损坏。
func (r *Resolver) saveCache() error {
	if r == nil || r.cacheFile == "" {
		return nil
	}

	r.mu.Lock()
	if r.savedRev == r.revision {
		r.mu.Unlock()
		return nil
	}
	revision := r.revision
	now := time.Now()
	entries := make([]storedDNSEntry, 0, len(r.cache))
	for host, entry := range r.cache {
		if !now.Before(entry.expiresAt.Add(dnsMaxStaleAge)) || len(entry.ips) == 0 {
			continue
		}
		ips := make([]string, 0, len(entry.ips))
		for _, ip := range entry.ips {
			ips = append(ips, ip.String())
		}
		entries = append(entries, storedDNSEntry{Host: host, IPs: ips, ExpiresAt: entry.expiresAt})
	}
	r.mu.Unlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ExpiresAt.Equal(entries[j].ExpiresAt) {
			return entries[i].Host < entries[j].Host
		}
		return entries[i].ExpiresAt.After(entries[j].ExpiresAt)
	})

	data := []byte(`{"version":1,"entries":[`)
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		separatorSize := 0
		if data[len(data)-1] != '[' {
			separatorSize = 1
		}
		if int64(len(data)+separatorSize+len(encoded)+2) > r.cacheMaxBytes {
			continue
		}
		if separatorSize != 0 {
			data = append(data, ',')
		}
		data = append(data, encoded...)
	}
	data = append(data, ']', '}')
	if int64(len(data)) > r.cacheMaxBytes {
		return fmt.Errorf("DNS cache metadata exceeds %d-byte file limit", r.cacheMaxBytes)
	}

	if err := os.MkdirAll(filepath.Dir(r.cacheFile), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.cacheFile), ".dns-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), r.cacheFile); err != nil {
		return err
	}

	r.mu.Lock()
	r.savedRev = revision
	r.mu.Unlock()
	return nil
}
