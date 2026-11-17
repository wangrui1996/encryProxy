package proxy

import (
	"context"
	"fmt"
	"net"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/protocol"
	"wangjui.com/enproxy/proxy/route"
)

// wrapCrypto adapts outer AES-GCM framing to a stream for every inbound protocol.
func wrapCrypto(conn net.Conn, cfg *config.CryptoConfig) (net.Conn, error) {
	if cfg == nil {
		return conn, nil
	}
	return core.NewCryptoConn(conn, cfg.Key)
}

// Also cancel Transport-owned dial/handshake work when its inbound stops.
func newProtocolDialWithContext(parent context.Context, dialer *OutboundDialer) outboundDialFunc {
	return func(ctx context.Context, out *config.OutboundConfig, address string) (net.Conn, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithCancel(ctx)
		done, exited := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(exited)
			select {
			case <-parent.Done():
				cancel()
			case <-done:
			}
		}()
		defer func() { close(done); cancel(); <-exited }()
		conn, err := dialer.DialContext(ctx, address)
		if err != nil {
			return nil, err
		}
		stop := core.CloseOnCancel(ctx, conn)
		defer stop()
		if out == nil || out.Crypto == nil {
			return conn, nil
		}
		if out.Crypto.AuthRequired {
			if err := authenticateOutbound(conn, out.Crypto); err != nil {
				_ = conn.Close()
				return nil, err
			}
		}
		wrapped, err := wrapCrypto(conn, out.Crypto)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return wrapped, nil
	}
}

// outboundDialFunc is private to the assembly layer; config never crosses into protocol.
type outboundDialFunc func(ctx context.Context, out *config.OutboundConfig, address string) (net.Conn, error)

// protocolRoutes adapts route.Manager without changing the routing module's API.
type protocolRoutes struct {
	manager *route.Manager
	dial    outboundDialFunc
}

func (r protocolRoutes) ResolveProxy(inboundTag, hostname string) (*protocol.Outbound, error) {
	out, err := r.manager.ResolveProxy(inboundTag, hostname)
	if err != nil {
		return nil, err
	}
	return r.outbound(out), nil
}

func (r protocolRoutes) ResolveRelay(inboundTag string) (*protocol.Outbound, error) {
	out, err := r.manager.ResolveRelay(inboundTag)
	if err != nil {
		return nil, err
	}
	return r.outbound(out), nil
}

func (r protocolRoutes) outbound(out *config.OutboundConfig) *protocol.Outbound {
	if out == nil {
		return nil
	}
	return &protocol.Outbound{
		Tag: out.Tag, Protocol: out.Protocol, Server: out.Server,
		HTTP:        protocolHTTPOptions(out.HTTP),
		Shadowsocks: protocolShadowsocksOptions(out.Shadowsocks),
		DialContext: func(ctx context.Context, address string) (net.Conn, error) {
			return r.dial(ctx, out, address)
		},
	}
}

func protocolHTTPOptions(cfg *config.HTTPConfig) *protocol.HTTPOptions {
	if cfg == nil {
		return nil
	}
	return &protocol.HTTPOptions{AuthRequired: cfg.AuthRequired, Account: cfg.Account, Passwd: cfg.Passwd}
}

func protocolShadowsocksOptions(cfg *config.ShadowsocksConfig) *protocol.ShadowsocksOptions {
	if cfg == nil {
		return nil
	}
	return &protocol.ShadowsocksOptions{Method: cfg.Method, Password: cfg.Password}
}

func protocolInboundOptions(in config.InboundConfig) protocol.InboundOptions {
	return protocol.InboundOptions{Tag: in.Tag, Protocol: in.Protocol, HTTP: protocolHTTPOptions(in.HTTP), Shadowsocks: protocolShadowsocksOptions(in.Shadowsocks)}
}

func dialProtocolOutbound(ctx context.Context, out *protocol.Outbound, address string) (net.Conn, error) {
	if out == nil || out.DialContext == nil {
		return nil, fmt.Errorf("outbound dialer is nil")
	}
	return out.DialContext(ctx, address)
}
