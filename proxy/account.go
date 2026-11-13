package proxy

import (
	"crypto/subtle"
	"fmt"
	"sync"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
)

type accountEntry struct {
	password string
	enabled  bool
	account  *core.Account
}

// AccountManager 管理所有下游登录账号。
type AccountManager struct {
	mu       sync.RWMutex
	accounts map[string]*accountEntry
}

func NewAccountManager(
	configs []config.AccountConfig,
) (*AccountManager, error) {
	manager := &AccountManager{
		accounts: make(map[string]*accountEntry, len(configs)),
	}

	for _, cfg := range configs {
		if cfg.Name == "" {
			return nil, fmt.Errorf("account name is empty")
		}
		if cfg.Password == "" {
			return nil, fmt.Errorf(
				"account %q password is empty",
				cfg.Name,
			)
		}
		if _, exists := manager.accounts[cfg.Name]; exists {
			return nil, fmt.Errorf(
				"duplicate account name: %s",
				cfg.Name,
			)
		}

		manager.accounts[cfg.Name] = &accountEntry{
			password: cfg.Password,
			enabled:  cfg.Enabled,
			account: &core.Account{
				Name: cfg.Name,
				RateLimit: core.NewRateLimit(
					cfg.RateLimit,
				),
			},
		}
	}

	return manager, nil
}

// Authenticate 验证账号密码。
// 认证失败时不区分账号不存在、密码错误或账号禁用。
func (m *AccountManager) Authenticate(
	username,
	password string,
) (*core.Account, error) {
	if m == nil {
		return nil, fmt.Errorf("account manager is nil")
	}

	m.mu.RLock()
	entry, exists := m.accounts[username]
	m.mu.RUnlock()

	if !exists || entry == nil || !entry.enabled {
		return nil, fmt.Errorf("invalid username or password")
	}

	if subtle.ConstantTimeCompare(
		[]byte(entry.password),
		[]byte(password),
	) != 1 {
		return nil, fmt.Errorf("invalid username or password")
	}

	return entry.account, nil
}

func (m *AccountManager) Count() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.accounts)
}