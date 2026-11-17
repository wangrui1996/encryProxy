package core

import (
	"context"
	"net"
	"sync/atomic"

	"golang.org/x/time/rate"
)

type RateLimit struct {
	uploadLimiter   *rate.Limiter
	downloadLimiter *rate.Limiter
}

func NewRateLimit(UploadKbps, DownloadKbps int) *RateLimit {
	r := &RateLimit{}
	if UploadKbps > 0 {
		r.uploadLimiter = rate.NewLimiter(rate.Limit(float64(UploadKbps)*1024), rateLimitBurst)
	}
	if DownloadKbps > 0 {
		r.downloadLimiter = rate.NewLimiter(rate.Limit(float64(DownloadKbps)*1024), rateLimitBurst)
	}
	return r
}

// RateLimitedConn can change its rate-limit policy after protocol auth.
// The limiter itself is shared between all sessions using the same account.
type RateLimitedConn struct {
	net.Conn
	current atomic.Pointer[RateLimit]
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewRateLimitedConn(conn net.Conn, limit *RateLimit) *RateLimitedConn {
	return NewRateLimitedConnWithContext(context.Background(), conn, limit)
}

func NewRateLimitedConnWithContext(parent context.Context, conn net.Conn, limit *RateLimit) *RateLimitedConn {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	c := &RateLimitedConn{Conn: conn, ctx: ctx, cancel: cancel}
	c.current.Store(limit)
	return c
}

func (c *RateLimitedConn) SetLimit(limit *RateLimit) { c.current.Store(limit) }

func waitN(ctx context.Context, limiter *rate.Limiter, n int) error {
	if limiter == nil || n <= 0 {
		return nil
	}
	burst := limiter.Burst()
	for n > 0 {
		size := n
		if size > burst {
			size = burst
		}
		if err := limiter.WaitN(ctx, size); err != nil {
			return err
		}
		n -= size
	}
	return nil
}

// Read counts incoming bytes against the configured upload bandwidth.
func (c *RateLimitedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		if limit := c.current.Load(); limit != nil {
			if waitErr := waitN(c.ctx, limit.uploadLimiter, n); waitErr != nil {
				return n, waitErr
			}
		}
	}
	return n, err
}

// Write counts outgoing bytes against the configured download bandwidth.
func (c *RateLimitedConn) Write(b []byte) (int, error) {
	if limit := c.current.Load(); limit != nil {
		if err := waitN(c.ctx, limit.downloadLimiter, len(b)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}

func (c *RateLimitedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}

func (c *RateLimitedConn) Close() error {
	c.cancel() // interrupt an in-flight rate.WaitN immediately
	return c.Conn.Close()
}
