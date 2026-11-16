package config

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "os"
)

// Load 读取并校验配置文件，不创建任何运行时 Manager。
func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config %q: %w", path, err)
    }

    cfg, err := Parse(data)
    if err != nil {
        return nil, fmt.Errorf("config %q: %w", path, err)
    }

    return cfg, nil
}

// Parse 解析一个完整 JSON 对象，拒绝未知字段、额外 JSON 值及非法配置。
// 输入使用标准 JSON，不支持注释或尾随逗号。
func Parse(data []byte) (*Config, error) {
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()

    var cfg *Config
    if err := decoder.Decode(&cfg); err != nil {
        return nil, fmt.Errorf("decode config: %w", err)
    }
    if cfg == nil {
        return nil, fmt.Errorf("config must be a JSON object")
    }

    var extra json.RawMessage
    if err := decoder.Decode(&extra); err != io.EOF {
        if err == nil {
            return nil, fmt.Errorf("config must contain exactly one JSON object")
        }
        return nil, fmt.Errorf("invalid trailing config data: %w", err)
    }

    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("validate config: %w", err)
    }

    return cfg, nil
}
