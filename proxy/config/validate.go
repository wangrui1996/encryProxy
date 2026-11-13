package config

import (
    "fmt"
    "time"
    "strings"
)

func (c *DNSConfig) Validate() error {
    if c == nil {
        return nil
    }

    if strings.TrimSpace(c.CacheTTL) == "" {
        return fmt.Errorf("dns.cache_ttl is required")
    }

    ttl, err := time.ParseDuration(c.CacheTTL)
    if err != nil {
        return fmt.Errorf(
            "invalid dns.cache_ttl %q: %w",
            c.CacheTTL,
            err,
        )
    }

    if ttl <= 0 {
        return fmt.Errorf("dns.cache_ttl must be greater than zero")
    }

    return nil
}