package protocol

import (
	"context"
	"fmt"
	"net"

	"wangjui.com/enproxy/proxy/config"
)

// Freedom establishes direct connections to the client-requested destination.
type Freedom struct{ Dial DialFunc }

func (f Freedom) Connect(ctx context.Context, cfg *config.OutboundConfig, target string) (net.Conn, error) {
	if f.Dial == nil {
		return nil, fmt.Errorf("freedom dialer is nil")
	}
	return f.Dial(ctx, cfg, target)
}
