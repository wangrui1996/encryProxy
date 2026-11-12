package proxy

// Config 是 enproxy 的完整运行配置。
type Config struct {
	Web       WebConfig        `json:"web"`
	Inbounds  []InboundConfig  `json:"inbounds"`
	Outbounds []OutboundConfig `json:"outbounds"`
	Accounts  []AccountConfig  `json:"accounts"`
	Route     RouteConfig      `json:"route"`
}

type InboundConfig struct {
	Tag          string          `json:"tag"`
	Listen       string          `json:"listen"`
	AuthRequired bool            `json:"auth_required"`
	Crypto       CryptoConfig    `json:"crypto"`
	RateLimit    RateLimitConfig `json:"rate_limit"`
}

type OutboundConfig struct {
	Tag      string                 `json:"tag"`
	Protocol string                 `json:"protocol"`
	Server   string                 `json:"server"`
	Crypto   CryptoConfig           `json:"crypto"`
	Account  *OutboundAccountConfig `json:"account,omitempty"`
}

// OutboundAccountConfig 保存登录上游所需的凭据。
// 注意：protocol=tcp 只是普通 TCP；当前代码不会自动发送这些凭据。
// 只有在定义并实现具体上游认证协议后，才能真正使用该字段登录上游。
type OutboundAccountConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AccountConfig struct {
	Name      string          `json:"name"`
	Password  string          `json:"password"`
	Enabled   bool            `json:"enabled"`
	RateLimit RateLimitConfig `json:"rate_limit"`
}

type CryptoConfig struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
}

type RateLimitConfig struct {
	UploadKbps   int `json:"up_kbps"`
	DownloadKbps int `json:"down_kbps"`
}

type RouteConfig struct {
	Default string      `json:"default"`
	Rules   []RouteRule `json:"rules"`
}

type RouteRule struct {
	Inbound  []string `json:"inbound"`
	Outbound string   `json:"outbound"`
}

type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
}
