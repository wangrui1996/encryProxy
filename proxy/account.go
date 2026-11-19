package proxy

import (
	"crypto/subtle"
	"fmt"
	"sync"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
)

// Account 是认证成功后的运行时账号，账号级限速器为所有连接共享。
type Account struct {
	Name      string
	RateLimit *core.RateLimit
}

type accountEntry struct {
	password     string
	cryptoEnable bool
	proxyEnable  bool
	account      *Account
}

// AccountManager 管理下游账号，端口/账号限速器由已认证账号共享。
type AccountManager struct {
	mu       sync.RWMutex
	accounts map[string]*accountEntry
}

func NewAccountManager(configs []config.AccountConfig) (*AccountManager, error) {
	manager := &AccountManager{accounts: make(map[string]*accountEntry, len(configs))}
	for _, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		if _, exists := manager.accounts[cfg.Name]; exists {
			return nil, fmt.Errorf("duplicate account name %q", cfg.Name)
		}
		manager.accounts[cfg.Name] = &accountEntry{
			password:     cfg.Password,
			cryptoEnable: cfg.CryptoEnable,
			proxyEnable:  cfg.ProxyEnable,
			account: &Account{
				Name:      cfg.Name,
				RateLimit: core.NewRateLimit(cfg.RateLimit.UploadKbps, cfg.RateLimit.DownloadKbps),
			},
		}
	}
	return manager, nil
}

func (m *AccountManager) authenticate(username, password string, isCrypto bool) (*Account, error) {
	if m == nil {
		return nil, fmt.Errorf("account manager is nil")
	}
	m.mu.RLock()
	entry := m.accounts[username]
	m.mu.RUnlock()

	// 账号不存在、密码错误、授权类型不符统一返回同一种错误。
	if entry == nil || (isCrypto && !entry.cryptoEnable) || (!isCrypto && !entry.proxyEnable) ||
		subtle.ConstantTimeCompare([]byte(entry.password), []byte(password)) != 1 {
		return nil, fmt.Errorf("invalid username or password")
	}
	return entry.account, nil
}

// AuthenticateCrypto 供 TCP 加密通道的认证使用。
func (m *AccountManager) AuthenticateCrypto(username, password string) (*Account, error) {
	return m.authenticate(username, password, true)
}

// AuthenticateProxy 为之后的 HTTP/其他代理协议认证预留。
func (m *AccountManager) AuthenticateProxy(username, password string) (*Account, error) {
	return m.authenticate(username, password, false)
}

func (m *AccountManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.accounts)
}
