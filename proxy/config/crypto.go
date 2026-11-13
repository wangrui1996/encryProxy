package config

import (
    "fmt"
)

type CryptoConfig struct {
    Enabled bool   `json:"enabled"`
    Key     string `json:"key"`
}

func (c *CryptoConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("crypto config is nil")
    }

    if !c.Enabled {
        return nil
    }

    if err := validateAESKey(c.Key); err != nil {
        return err
    }

    return nil
}

func validateAESKey(key string) error {
    keyLen := len([]byte(key))

    switch keyLen {
    case 16, 24, 32:
        return nil
    default:
        return fmt.Errorf(
            "AES key must be 16, 24, or 32 bytes; got %d",
            keyLen,
        )
    }
}