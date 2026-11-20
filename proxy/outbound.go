package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"wangjui.com/spider/proxy/config"
	"wangjui.com/spider/proxy/core"
	"wangjui.com/spider/proxy/protocol"
)

// OutboundManager 持有所有入口共享的出站表和底层拨号器。
type OutboundManager struct {
	entries map[string]*outbound
	dialer  *OutboundDialer
}

type outbound struct {
	kind    string
	options protocol.Outbound
	crypto  *config.CryptoConfig
}

func NewOutboundManager(configs []config.OutboundConfig, dialer *OutboundDialer) (*OutboundManager, error) {
	if dialer == nil {
		return nil, fmt.Errorf("outbound dialer is nil")
	}
	m := &OutboundManager{
		dialer: dialer,
		entries: map[string]*outbound{
			"freedom":   {kind: "freedom"},
			"blackhole": {kind: "blackhole"},
		},
	}
	for i, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("outbounds[%d]: %w", i, err)
		}
		if _, ok := m.entries[cfg.Tag]; ok {
			return nil, fmt.Errorf("duplicate outbound %q", cfg.Tag)
		}
		out := &outbound{
			kind: cfg.Protocol,
			options: protocol.Outbound{
				Server:      cfg.Server,
				HTTP:        protocolHTTPOptions(cfg.HTTP),
				Shadowsocks: protocolShadowsocksOptions(cfg.Shadowsocks),
			},
		}
		if cfg.Crypto != nil {
			crypto := *cfg.Crypto
			out.crypto = &crypto
		}
		m.entries[cfg.Tag] = out
	}
	return m, nil
}

// Each inbound supplies its lifetime so detached HTTP transport dials stop on shutdown.
type inboundOutbounds struct {
	*OutboundManager
	parent context.Context
}

func (m inboundOutbounds) lookup(tag string) (*outbound, error) {
	out, ok := m.entries[tag]
	if !ok {
		return nil, fmt.Errorf("unknown outbound %q", tag)
	}
	if out.kind == "blackhole" {
		return nil, protocol.ErrBlackhole
	}
	return out, nil
}

func (m inboundOutbounds) Connect(ctx context.Context, tag, target string) (net.Conn, io.Reader, error) {
	out, err := m.lookup(tag)
	if err != nil {
		return nil, nil, err
	}
	dial := m.dialFunc(out)
	switch out.kind {
	case "tcp":
		conn, err := dial(ctx, out.options.Server)
		return conn, conn, err
	case "freedom":
		conn, err := dial(ctx, target)
		return conn, conn, err
	case "http":
		return (protocol.HTTPClient{Dial: dial}).Connect(ctx, &out.options, target)
	case "shadowsocks":
		conn, err := (protocol.ShadowsocksClient{Dial: dial}).Connect(ctx, &out.options, target)
		return conn, conn, err
	default:
		return nil, nil, fmt.Errorf("unsupported outbound %q", out.kind)
	}
}

func (m inboundOutbounds) NewHTTPTransport(tag string) (*http.Transport, error) {
	out, err := m.lookup(tag)
	if err != nil {
		return nil, err
	}
	dial := m.dialFunc(out)
	switch out.kind {
	case "http":
		return (protocol.HTTPClient{Dial: dial}).Transport(&out.options)
	case "tcp":
		// TCP 固定出口不解析目标地址，只连接已配置的上游 Server。
		return protocol.NewDirectHTTPTransport(func(ctx context.Context, _ string) (net.Conn, error) {
			return dial(ctx, out.options.Server)
		}, false), nil
	case "freedom":
		return protocol.NewDirectHTTPTransport(dial, true), nil
	case "shadowsocks":
		client := protocol.ShadowsocksClient{Dial: dial}
		return protocol.NewDirectHTTPTransport(func(ctx context.Context, address string) (net.Conn, error) {
			return client.Connect(ctx, &out.options, address)
		}, false), nil
	default:
		return nil, fmt.Errorf("unsupported HTTP outbound %q", out.kind)
	}
}

func (m inboundOutbounds) dialFunc(out *outbound) protocol.DialFunc {
	return func(ctx context.Context, address string) (net.Conn, error) {
		if ctx == nil {
			ctx = context.Background()
		}

		ctx, cancel := context.WithCancel(ctx)
		done, exited := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(exited)
			select {
			case <-m.parent.Done():
				cancel()
			case <-done:
			}
		}()
		defer func() {
			close(done)
			cancel()
			<-exited
		}()

		conn, err := m.dialer.DialContext(ctx, address)
		if err != nil {
			return nil, err
		}

		crypto := out.crypto
		if crypto == nil {
			return conn, nil
		}

		stop := core.CloseOnCancel(ctx, conn)
		defer stop()

		if crypto.AuthRequired {
			if err := authenticateOutbound(conn, crypto); err != nil {
				_ = conn.Close()
				return nil, err
			}
		}

		wrapped, err := wrapCrypto(conn, crypto)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return wrapped, nil
	}
}
