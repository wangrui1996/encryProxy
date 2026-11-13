package config

import (
    "fmt"
)

type AccountConfig struct {
    Name      string          `json:"name"`
    Password  string          `json:"password"`
    Enabled   bool            `json:"enabled"`
    RateLimit RateLimitConfig `json:"rate_limit"`
}

func (c *AccountConfig) Validate() error {
    if c == nil {
        return fmt.Errorf("account config is nil")
    }

    if c.Name == "" {
        return fmt.Errorf("account name is empty")
    }

    if c.Password == "" {
        return fmt.Errorf("account %q password is empty", c.Name)
    }

    if err := c.RateLimit.Validate(); err != nil {
        return fmt.Errorf("account %q: %w", c.Name, err)
    }

    return nil
}

func validateAccountList(accounts []AccountConfig) error {
    names := make(map[string]struct{}, len(accounts))

    for i := range accounts {
        account := &accounts[i]

        if err := account.Validate(); err != nil {
            return fmt.Errorf("accounts[%d]: %w", i, err)
        }

        if _, exists := names[account.Name]; exists {
            return fmt.Errorf("duplicate account name %q", account.Name)
        }

        names[account.Name] = struct{}{}
    }

    return nil
}