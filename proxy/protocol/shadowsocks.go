package protocol

import (
	"context"
	"fmt"
	"net"
	"time"

	"wangjui.com/enproxy/proxy/config"
)

// Shadowsocks is an inbound classic AEAD Shadowsocks TCP server.
// Routing is shared with HTTP; UDP ASSOCIATE/relay is a separate feature.
type Shadowsocks struct {
	Inbound config.InboundConfig
	Routes  Resolver
	Dial    DialFunc
	cipher  *ssCipher
}

func NewShadowsocks(in config.InboundConfig, routes Resolver, dial DialFunc) (*Shadowsocks, error) {
	if in.Protocol != "shadowsocks" || in.Shadowsocks == nil {
		return nil, fmt.Errorf("Shadowsocks inbound config required")
	}
	if routes == nil || dial == nil {
		return nil, fmt.Errorf("Shadowsocks route resolver and dialer are required")
	}
	suite, err := newSSCipher(in.Shadowsocks, true)
	if err != nil {
		return nil, err
	}
	return &Shadowsocks{Inbound: in, Routes: routes, Dial: dial, cipher: suite}, nil
}

func (s *Shadowsocks) Handle(ctx context.Context, downstream net.Conn, _ AuthenticateFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	clear := newSSStream(downstream, s.cipher)
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
	return Relay(clear, clear, upstream, upstreamReader)
}
