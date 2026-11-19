package protocol

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// originalDestinationKey is only populated for Linux NAT REDIRECT connections.
// Using context instead of changing Handler/HTTPOutbounds keeps all existing
// inbound and outbound protocol implementations compatible.
type originalDestinationKey struct{}

// WithOriginalDestination annotates a redirected connection with its original
// destination, captured before wrapping the underlying socket.
func WithOriginalDestination(ctx context.Context, destination string) context.Context {
	return context.WithValue(ctx, originalDestinationKey{}, destination)
}

func originalDestinationFromContext(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(originalDestinationKey{}).(string)
	return value, ok && value != ""
}

// handleSniff is the transparent mode of the EXISTING HTTP inbound, not another
// inbound protocol. Both modes share the same ResolveProxy, Connect and Relay.
func (h *HTTP) handleSniff(ctx context.Context, downstream net.Conn, original string) error {
	originalHost, port, err := net.SplitHostPort(original)
	if err != nil {
		return fmt.Errorf("invalid NAT original destination %q: %w", original, err)
	}
	if net.ParseIP(originalHost) == nil {
		return fmt.Errorf("invalid NAT original destination IP %q", originalHost)
	}
	host, replay, err := sniffHostname(downstream)
	if err != nil {
		return fmt.Errorf("sniff HTTP inbound TCP stream: %w", err)
	}
	matchHost := originalHost
	if host != "" {
		matchHost = host
	}
	outbound, err := h.Routes.ResolveProxy(h.Inbound.Tag, matchHost)
	if err != nil {
		return fmt.Errorf("route sniffed host %q: %w", matchHost, err)
	}

	// A direct TCP connect must use the original IP:port, not re-resolve a
	// potentially DNS-poisoned Host/SNI. HTTP/SS upstreams receive the hostname
	// so they can resolve it remotely. Fixed TCP outbounds ignore the target.
	target := original
	if host != "" && outbound != "freedom" && outbound != "blackhole" {
		target = net.JoinHostPort(host, port)
	}
	upstream, upstreamReader, err := h.Outbounds.Connect(ctx, outbound, target)
	if errors.Is(err, ErrBlackhole) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("connect original destination %q via %q: %w", target, outbound, err)
	}
	defer upstream.Close()
	return h.Relay(ctx, downstream, replay, upstream, upstreamReader)
}
