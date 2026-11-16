package proxy

import (
	"context"
	"net"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
	"wangjui.com/enproxy/proxy/protocol"
)

// wrapCrypto adapts outer AES-GCM framing to a stream for every inbound protocol.
func wrapCrypto(conn net.Conn, cfg *config.CryptoConfig) (net.Conn, error) {
	if cfg == nil {
		return conn, nil
	}
	return core.NewCryptoConn(conn, cfg.Key)
}

// Also cancel Transport-owned dial/handshake work when its inbound stops.
func newProtocolDialWithContext(parent context.Context, dialer *OutboundDialer) protocol.DialFunc {
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
