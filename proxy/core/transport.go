package core

import (
	"context"
	"net"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/protocol"
)

// wrapCrypto adapts outer AES-GCM framing to a stream for every inbound protocol.
func wrapCrypto(conn net.Conn, cfg *config.CryptoConfig) (net.Conn, error) {
	if cfg == nil {
		return conn, nil
	}
	return NewCryptoConn(conn, cfg.Key)
}

// newProtocolDial owns outbound connection/authentication/encryption once for all protocols.
func newProtocolDial(dialer Dialer) protocol.DialFunc {
	return func(ctx context.Context, out *config.OutboundConfig, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, address)
		if err != nil {
			return nil, err
		}
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
