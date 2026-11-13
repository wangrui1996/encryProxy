package config

type RouteConfig struct {
    Default string      `json:"default"`
    Rules   []RouteRule `json:"rules"`
}

type RouteRule struct {
    Inbound  []string `json:"inbound"`
    Outbound string   `json:"outbound"`
}