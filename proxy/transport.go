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

// newProtocolDialWithContext is the only outbound dialing injection point.
// It performs the raw network dial, optional ENPA authentication and optional
// outer AES-GCM wrapping. Protocol handlers only decide when and where to dial.
func newProtocolDialWithContext(parent context.Context, dialer *OutboundDialer) protocol.DialFunc {
	if parent == nil {
		parent = context.Background()
	}

	return func(ctx context.Context, out *protocol.Outbound, address string) (net.Conn, error) {
		if out == nil {
			return nil, fmt.Errorf("outbound is nil")
		}
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
		defer func() {
			close(done)
			cancel()
			<-exited
		}()

		conn, err := dialer.DialContext(ctx, address)
		if err != nil {
			return nil, err
		}

		crypto := protocolCryptoConfig(out.Crypto)
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

// protocolRoutes adapts route.Manager without changing the routing module's API.
type protocolRoutes struct {
	manager *route.Manager
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
		Tag:         out.Tag,
		Protocol:    out.Protocol,
		Server:      out.Server,
		Crypto:      protocolCryptoOptions(out.Crypto),
		HTTP:        protocolHTTPOptions(out.HTTP),
		Shadowsocks: protocolShadowsocksOptions(out.Shadowsocks),
	}
}

func protocolCryptoOptions(cfg *config.CryptoConfig) *protocol.CryptoOptions {
	if cfg == nil {
		return nil
	}
	return &protocol.CryptoOptions{
		Key:          cfg.Key,
		AuthRequired: cfg.AuthRequired,
		Account:      cfg.Account,
		Passwd:       cfg.Passwd,
	}
}

func protocolCryptoConfig(cfg *protocol.CryptoOptions) *config.CryptoConfig {
	if cfg == nil {
		return nil
	}
	return &config.CryptoConfig{
		Key:          cfg.Key,
		AuthRequired: cfg.AuthRequired,
		Account:      cfg.Account,
		Passwd:       cfg.Passwd,
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
