package config

type OutboundConfig struct {
    Tag      string                 `json:"tag"`
    Protocol string                 `json:"protocol"`
    Server   string                 `json:"server"`
    Crypto   CryptoConfig           `json:"crypto"`
    Account  *OutboundAccountConfig `json:"account,omitempty"`
}

// 只有在定义并实现具体上游认证协议后，才能真正使用该字段登录上游。
type OutboundAccountConfig struct {
    Username string `json:"username"`
    Password string `json:"password"`
}