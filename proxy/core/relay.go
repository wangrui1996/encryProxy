package core

import (
	"context"
	"fmt"
	"net"

	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/protocol"
)

// relayHandler 只处理固定目标 TCP 端口转发，不解析客户端代理目标。
type relayHandler struct {
	inbound       config.InboundConfig
	routeResolver RouteResolver
	dial          protocol.DialFunc
}

func (h *relayHandler) Handle(ctx context.Context, inboundConn net.Conn, _ protocol.AuthenticateFunc) error {
	outbound, err := h.routeResolver.ResolveRelay(h.inbound.Tag)
	if err != nil {
		return fmt.Errorf("resolve relay route: %w", err)
	}
	if outbound == nil || outbound.Protocol != "tcp" {
		return fmt.Errorf("inbound %q: relay requires tcp outbound", h.inbound.Tag)
	}

	outboundConn, err := h.dial(ctx, outbound, outbound.Server)
	if err != nil {
		return fmt.Errorf("dial outbound %s (%s): %w", outbound.Tag, outbound.Server, err)
	}
	defer outboundConn.Close()

	return protocol.Relay(inboundConn, inboundConn, outboundConn, outboundConn)
}
