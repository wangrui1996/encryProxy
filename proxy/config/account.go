package config

type AccountConfig struct {
    Name      string          `json:"name"`
    Password  string          `json:"password"`
    Enabled   bool            `json:"enabled"`
    RateLimit RateLimitConfig `json:"rate_limit"`
}