package protocol

import (
	"context"
	"fmt"
	"net"
	"time"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
)

// Shadowsocks is an inbound classic AEAD Shadowsocks TCP server.
// Routing is shared with HTTP; UDP ASSOCIATE/relay is a separate feature.
type Shadowsocks struct {
	Inbound config.InboundConfig
	Routes  Resolver
	Dial    DialFunc
	Relay   RelayFunc
	cipher  *core.ShadowsocksCipher
}

func NewShadowsocks(in config.InboundConfig, routes Resolver, dial DialFunc, relay RelayFunc) (*Shadowsocks, error) {
	if in.Protocol != "shadowsocks" || in.Shadowsocks == nil {
		return nil, fmt.Errorf("Shadowsocks inbound config required")
	}
	if routes == nil || dial == nil || relay == nil {
		return nil, fmt.Errorf("Shadowsocks route resolver, dialer and relay are required")
	}
	suite, err := core.NewShadowsocksCipher(in.Shadowsocks.Method, in.Shadowsocks.Password, true)
	if err != nil {
		return nil, err
	}
	return &Shadowsocks{Inbound: in, Routes: routes, Dial: dial, Relay: relay, cipher: suite}, nil
}

func (s *Shadowsocks) Handle(ctx context.Context, downstream net.Conn, _ AuthenticateFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	clear := core.NewShadowsocksConn(downstream, s.cipher)
	// Initial salt, encrypted address header and routing phase must not wait forever.
	_ = downstream.SetReadDeadline(time.Now().Add(15 * time.Second))
	target, hostname, err := readTargetAddress(clear)
	_ = downstream.SetReadDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("read Shadowsocks destination: %w", err)
	}
	outbound, err := s.Routes.ResolveProxy(s.Inbound.Tag, hostname)
	if err != nil {
		return fmt.Errorf("route Shadowsocks destination %s: %w", hostname, err)
	}
	if outbound == nil {
		return fmt.Errorf("nil outbound for %s", hostname)
	}
	if outbound.Protocol == "blackhole" {
		return (Blackhole{}).Handle(downstream)
	}
	upstream, upstreamReader, err := connectTarget(ctx, s.Dial, outbound, target)
	if err != nil {
		return fmt.Errorf("connect Shadowsocks destination %s: %w", target, err)
	}
	defer upstream.Close()
	return s.Relay(clear, clear, upstream, upstreamReader)
}
