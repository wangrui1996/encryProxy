package route

import (
	"fmt"

	"wangjui.com/enproxy/proxy/config"
)

// buildOutboundIndex 构建路由使用的上游 outbound 索引。
// freedom / blackhole 是内置 outbound，不需要配置在 outbounds 中。
// 配置副本避免调用方在 Manager 创建后修改配置影响运行中的路由。
func buildOutboundIndex(outbounds []config.OutboundConfig) (map[string]*config.OutboundConfig, error) {
	result := map[string]*config.OutboundConfig{
		"freedom":   {Tag: "freedom", Protocol: "freedom"},
		"blackhole": {Tag: "blackhole", Protocol: "blackhole"},
	}
	for i := range outbounds {
		out := outbounds[i]
		if err := out.Validate(); err != nil {
			return nil, fmt.Errorf("outbounds[%d]: %w", i, err)
		}
		if _, exists := result[out.Tag]; exists {
			return nil, fmt.Errorf("duplicate outbound tag %q", out.Tag)
		}
		if out.Crypto != nil {
			clone := *out.Crypto
			out.Crypto = &clone
		}
		if out.HTTP != nil {
			clone := *out.HTTP
			out.HTTP = &clone
		}
		if out.Shadowsocks != nil {
			clone := *out.Shadowsocks
			out.Shadowsocks = &clone
		}
		result[out.Tag] = &out
	}
	return result, nil
}

// findOutbound 是 route 内唯一的运行时上游 tag 查找入口。
func findOutbound(index map[string]*config.OutboundConfig, tag string) (*config.OutboundConfig, error) {
	out, ok := index[tag]
	if !ok {
		return nil, fmt.Errorf("unknown outbound %q", tag)
	}
	return out, nil
}
