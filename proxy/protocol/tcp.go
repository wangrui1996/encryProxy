package protocol

import (
	"context"
	"fmt"
	"net"

	"wangjui.com/enproxy/proxy/config"
)

// RelayResolver only exposes the fixed-target TCP route needed by this handler.
type RelayResolver interface {
	ResolveRelay(inboundTag string) (*config.OutboundConfig, error)
}

// TCP handles only the TCP protocol-specific routing and outbound connection.
// The byte forwarding implementation is injected from core by the assembly layer.
type TCP struct {
	inbound       config.InboundConfig
	routeResolver RelayResolver
	dial          DialFunc
	relay         RelayFunc
}

func NewTCP(inbound config.InboundConfig, routes RelayResolver, dial DialFunc, relay RelayFunc) (*TCP, error) {
	if inbound.Protocol != "tcp" || routes == nil || dial == nil || relay == nil {
		return nil, fmt.Errorf("TCP inbound requires tcp protocol, route resolver, dialer and relay")
	}
	return &TCP{inbound: inbound, routeResolver: routes, dial: dial, relay: relay}, nil
}

func (h *TCP) Handle(ctx context.Context, inboundConn net.Conn, _ AuthenticateFunc) error {
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

	return h.relay(inboundConn, inboundConn, outboundConn, outboundConn)
}
