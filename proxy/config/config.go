package config

// Config 是 enproxy 的完整运行配置。
type Config struct {
    Inbounds  []InboundConfig  `json:"inbounds"`
    DNS      *DNSConfig      `json:"dns,omitempty"`
    Web       WebConfig        `json:"web"`
    Outbounds []OutboundConfig `json:"outbounds"`
    Accounts  []AccountConfig  `json:"accounts"`
    Route     RouteConfig      `json:"route"`
}

type DNSConfig struct {
    CacheTTL string `json:"cache_ttl"`
}

type WebConfig struct {
    Enabled bool   `json:"enabled"`
    Listen  string `json:"listen"`
}
