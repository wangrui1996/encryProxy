package protocol

import (
	"context"
	"fmt"
	"net"
	"time"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/core"
)

// ShadowsocksClient dials an upstream Shadowsocks server and sends the
// standard encrypted destination header. No separate user/password exchange.
type ShadowsocksClient struct{ Dial DialFunc }

func (c ShadowsocksClient) Connect(ctx context.Context, out *config.OutboundConfig, target string) (net.Conn, error) {
	if out == nil || out.Protocol != "shadowsocks" || out.Shadowsocks == nil {
		return nil, fmt.Errorf("invalid Shadowsocks outbound")
	}
	if c.Dial == nil {
		return nil, fmt.Errorf("Shadowsocks outbound dialer is nil")
	}
	header, err := encodeTargetAddress(target)
	if err != nil {
		return nil, err
	}
	suite, err := core.NewShadowsocksCipher(out.Shadowsocks.Method, out.Shadowsocks.Password, false)
	if err != nil {
		return nil, err
	}
	raw, err := c.Dial(ctx, out, out.Server)
	if err != nil {
		return nil, err
	}
	stop := core.CloseOnCancel(ctx, raw)
	defer stop()
	conn := core.NewShadowsocksConn(raw, suite)
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.Write(header); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write Shadowsocks target: %w", err)
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
