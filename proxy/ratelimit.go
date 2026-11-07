package proxy

import (
	"context"
	"net"
	"golang.org/x/time/rate"
)

type RateLimitConfig struct {
	UploadKbps   int `json:"up_kbps"`   // 上行限速 KB/s
	DownloadKbps int `json:"down_kbps"` // 下行限速 KB/s
}

type RateLimit struct {
	uploadLimiter   *rate.Limiter
	downloadLimiter *rate.Limiter
}

func NewRateLimit(cfg RateLimitConfig) *RateLimit {
	r := &RateLimit{}

	const burst = 32 * 1024

	if cfg.UploadKbps > 0 {
		limit := rate.Limit(float64(cfg.UploadKbps) * 1024)
		r.uploadLimiter = rate.NewLimiter(limit, burst)
	}

	if cfg.DownloadKbps > 0 {
		limit := rate.Limit(float64(cfg.DownloadKbps) * 1024)
		r.downloadLimiter = rate.NewLimiter(limit, burst)
	}

	return r
}

type RateLimitedConn struct {
	net.Conn
	limit *RateLimit
}

func NewRateLimitedConn(conn net.Conn, limit *RateLimit) *RateLimitedConn {
	return &RateLimitedConn{
		Conn:  conn,
		limit: limit,
	}
}

func waitN(limiter *rate.Limiter, n int) error {
	if limiter == nil || n <= 0 {
		return nil
	}

	burst := limiter.Burst()

	for n > 0 {
		size := n
		if size > burst {
			size = burst
		}

		if err := limiter.WaitN(context.Background(), size); err != nil {
			return err
		}

		n -= size
	}

	return nil
}

// 下游 -> 代理：上行限速。
func (c *RateLimitedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)

	if n > 0 && c.limit != nil {
		if waitErr := waitN(c.limit.uploadLimiter, n); waitErr != nil {
			return n, waitErr
		}
	}

	return n, err
}

// 代理 -> 下游：下行限速。
func (c *RateLimitedConn) Write(b []byte) (int, error) {
	if c.limit != nil {
		if err := waitN(c.limit.downloadLimiter, len(b)); err != nil {
			return 0, err
		}
	}

	return c.Conn.Write(b)
}

func (c *RateLimitedConn) CloseWrite() error {
	cw, ok := c.Conn.(interface {
		CloseWrite() error
	})
	if !ok {
		return net.ErrClosed
	}

	return cw.CloseWrite()
}
