package config

type InboundConfig struct {
    Tag          string          `json:"tag"`
    Listen       string          `json:"listen"`
    AuthRequired bool            `json:"auth_required"`
    Crypto       CryptoConfig    `json:"crypto"`
    RateLimit    RateLimitConfig `json:"rate_limit"`
}

type CryptoConfig struct {
    Enabled bool   `json:"enabled"`
    Key     string `json:"key"`
}

type RateLimitConfig struct {
    UploadKbps   int `json:"up_kbps"`
    DownloadKbps int `json:"down_kbps"`
}