package config

import "fmt"

type RateLimitConfig struct {
    UploadKbps   int `json:"up_kbps"`
    DownloadKbps int `json:"down_kbps"`
}

func (c *RateLimitConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("rate limit config is nil")
    }

    if c.UploadKbps < 0 {
        return fmt.Errorf("upload limit cannot be negative")
    }

    if c.DownloadKbps < 0 {
        return fmt.Errorf("download limit cannot be negative")
    }

    return nil
}