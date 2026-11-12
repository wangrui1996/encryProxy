
package proxy

type Config struct {
	Proxy []ProxyConfig `json:"proxy"`
	Web   WebConfig   `json:"web"`
}

type ProxyConfig struct {
	Name          string          `json:"name"`
	Listen        string         `json:"listen"`
	CryptoEnabled bool           `json:"crypto_enabled"`
	Key           string         `json:"key"`
	RateLimit     RateLimitConfig `json:"rate_limit"`
	Upstream      UpstreamConfig  `json:"upstream"`
}

type RateLimitConfig struct {
	UploadKbps   int `json:"up_kbps"`
	DownloadKbps int `json:"down_kbps"`
}

type UpstreamConfig struct {
	Name          string `json:"name"`
	Url           string `json:"url"`
	CryptoEnabled bool   `json:"crypto_enabled"`
	Key           string `json:"key"`
}

type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
}
