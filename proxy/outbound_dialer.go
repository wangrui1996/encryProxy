package proxy

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"wangjui.com/enproxy/proxy/dns"
)

const happyEyeballsDelay = 250 * time.Millisecond

type OutboundDialer struct {
	resolver *dns.Resolver
	dialer   net.Dialer
	nextIP   atomic.Uint64
}

func NewOutboundDialer(resolver *dns.Resolver) *OutboundDialer {
	return &OutboundDialer{
		resolver: resolver,
		dialer: net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (d *OutboundDialer) DialContext(ctx context.Context, address string) (net.Conn, error) {
	if d == nil || d.resolver == nil {
		return nil, fmt.Errorf("outbound dialer is not initialized")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid outbound address %q: %w", address, err)
	}

	// One deadline covers DNS lookup and all TCP connection attempts.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if ip := net.ParseIP(host); ip != nil {
		return d.dialer.DialContext(dialCtx, "tcp", address)
	}

	ips, err := d.resolver.Lookup(dialCtx, host)
	if err != nil {
		return nil, err
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("DNS returned no IP for %q", host)
	}

	// Preserve the existing round-robin start, then split by address family.
	start := int(d.nextIP.Add(1)-1) % len(ips)
	var ipv4, ipv6 []net.IPAddr
	for i := 0; i < len(ips); i++ {
		ip := ips[(start+i)%len(ips)]
		if ip.IP.To4() != nil {
			ipv4 = append(ipv4, ip)
		} else {
			ipv6 = append(ipv6, ip)
		}
	}

	// With only one address family, preserve serial dialing behavior.
	if len(ipv4) == 0 || len(ipv6) == 0 {
		addrs := ipv4
		if len(addrs) == 0 {
			addrs = ipv6
		}
		conn, err := d.dialIPs(dialCtx, addrs, port)
		if err != nil {
			return nil, fmt.Errorf("outbound %q connection failed: %w", address, err)
		}
		return conn, nil
	}

	// Happy Eyeballs: race the two families; dial serially within each family.
	primary, fallback := ipv6, ipv4
	if ips[start].IP.To4() != nil {
		primary, fallback = ipv4, ipv6
	}

	type dialResult struct {
		conn    net.Conn
		err     error
		primary bool
	}
	results := make(chan dialResult)
	finished := make(chan struct{})
	defer close(finished)

	startRacer := func(addrs []net.IPAddr, isPrimary bool) {
		go func() {
			conn, err := d.dialIPs(dialCtx, addrs, port)
			select {
			case results <- dialResult{conn: conn, err: err, primary: isPrimary}:
			case <-finished:
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}

	startRacer(primary, true)
	timer := time.NewTimer(happyEyeballsDelay)
	defer timer.Stop()
	fallbackStarted := false
	primaryDone, fallbackDone := false, false
	var primaryErr, fallbackErr error

	for {
		select {
		case <-dialCtx.Done():
			return nil, fmt.Errorf("outbound %q connection failed: %w", address, dialCtx.Err())
		case <-timer.C:
			if !fallbackStarted {
				fallbackStarted = true
				startRacer(fallback, false)
			}
		case result := <-results:
			if result.err == nil {
				if err := dialCtx.Err(); err != nil {
					_ = result.conn.Close()
					return nil, fmt.Errorf("outbound %q connection failed: %w", address, err)
				}
				return result.conn, nil
			}
			if result.primary {
				primaryDone, primaryErr = true, result.err
				if !fallbackStarted {
					// Primary failed before the delay: try the fallback immediately.
					fallbackStarted = true
					timer.Stop()
					startRacer(fallback, false)
				}
			} else {
				fallbackDone, fallbackErr = true, result.err
			}
			if primaryDone && fallbackDone {
				return nil, fmt.Errorf("outbound %q connection failed: primary: %v; fallback: %w", address, primaryErr, fallbackErr)
			}
		}
	}
}

func (d *OutboundDialer) dialIPs(ctx context.Context, ips []net.IPAddr, port string) (net.Conn, error) {
	var lastErr error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		target := net.JoinHostPort(ip.String(), port)
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := d.dialer.DialContext(attemptCtx, "tcp", target)
		attemptCancel()
		if err == nil {
			return conn, nil
		}
		lastErr = fmt.Errorf("connect %s failed: %w", target, err)
	}
	return nil, lastErr
}
