package protocol

import (
	"context"
	"fmt"
	"net"
)

// RelayResolver only exposes the fixed-target TCP route needed by this handler.
type RelayResolver interface {
	ResolveRelay(inboundTag string) (string, error)
}

// TCP handles only the TCP protocol-specific routing and outbound connection.
// The byte forwarding implementation is injected from core by the assembly layer.
type TCP struct {
	inbound       InboundOptions
	routeResolver RelayResolver
	outbounds     OutboundConnector
	relay         RelayFunc
}

func NewTCP(inbound InboundOptions, routes RelayResolver, outbounds OutboundConnector, relay RelayFunc) (*TCP, error) {
	if inbound.Protocol != "tcp" || routes == nil || outbounds == nil || relay == nil {
		return nil, fmt.Errorf("TCP inbound requires tcp protocol, route resolver, dialer and relay")
	}
	return &TCP{inbound: inbound, routeResolver: routes, outbounds: outbounds, relay: relay}, nil
}

func (h *TCP) Handle(ctx context.Context, inboundConn net.Conn, _ AuthenticateFunc) error {
	outbound, err := h.routeResolver.ResolveRelay(h.inbound.Tag)
	if err != nil {
		return fmt.Errorf("resolve relay route: %w", err)
	}
	outboundConn, reader, err := h.outbounds.Connect(ctx, outbound, "")
	if err != nil {
		return fmt.Errorf("connect tcp outbound %s: %w", outbound, err)
	}
	defer outboundConn.Close()

	return h.relay(ctx, inboundConn, inboundConn, outboundConn, reader)
}
