package protocol

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func parseProxyBasic(raw string) (string, string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(raw), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", "", false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", "", false
	}
	user, pass, ok := strings.Cut(string(data), ":")
	return user, pass, ok && user != "" && pass != ""
}

// targetOfRequest requires standard proxy absolute-form requests (not origin-form).
func targetOfRequest(req *http.Request) (address string, hostname string, err error) {
	if req.Method == http.MethodConnect {
		address = req.Host
		if req.URL != nil && req.URL.Host != "" {
			address = req.URL.Host
		}
		hostname, _, err = net.SplitHostPort(address)
		if err != nil {
			return "", "", fmt.Errorf("CONNECT target must specify host:port: %w", err)
		}
		if err := validateTarget(address); err != nil {
			return "", "", err
		}
		return address, hostname, nil
	}
	if req.URL == nil || !req.URL.IsAbs() || req.URL.User != nil {
		return "", "", fmt.Errorf("HTTP proxy requires an absolute URL")
	}
	scheme := strings.ToLower(req.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", fmt.Errorf("unsupported request URL scheme %q", scheme)
	}
	hostname = req.URL.Hostname()
	address = req.URL.Host
	if req.URL.Port() == "" {
		port := "80"
		if scheme == "https" {
			port = "443"
		}
		address = net.JoinHostPort(hostname, port)
	}
	if err := validateTarget(address); err != nil {
		return "", "", err
	}
	return address, hostname, nil
}

// stripHopHeaders also strips arbitrary fields named by Connection/Proxy-Connection.
func stripHopHeaders(h http.Header) {
	// Collect first: Connection may itself name Proxy-Connection.
	var named []string
	for _, field := range []string{"Connection", "Proxy-Connection"} {
		for _, value := range h.Values(field) {
			for _, item := range strings.Split(value, ",") {
				if key := strings.TrimSpace(item); key != "" {
					named = append(named, key)
				}
			}
		}
	}
	for _, key := range named {
		h.Del(key)
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(name)
	}
}

func writeHTTPError(conn net.Conn, code int) error {
	_ = conn.SetWriteDeadline(time.Now().Add(httpErrorWriteTimeout))
	defer conn.SetWriteDeadline(time.Time{})
	text := http.StatusText(code)
	_, err := fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", code, text)
	return err
}

func writeProxyAuthRequired(conn net.Conn) error {
	_ = conn.SetWriteDeadline(time.Now().Add(httpErrorWriteTimeout))
	defer conn.SetWriteDeadline(time.Time{})
	_, err := io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"spider\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	return err
}

// maxHTTPHeaderBytes limits the request line and all header fields combined.
// The request body and CONNECT tunnel data are not subject to this limit.
const maxHTTPHeaderBytes = 64 * 1024

var errHTTPHeaderTooLarge = errors.New("HTTP request header too large")

// httpHeaderLimiter scans bytes while a request line/headers are being parsed.
//
// It sits *below* the persistent bufio.Reader. ReadRequest may read ahead into
// the body or a CONNECT stream: scanning stops at the empty header line, but all
// bytes remain available from the same bufio.Reader. For keep-alive requests,
// beginRequest also scans bytes already buffered from an earlier read.
//
// A limiter belongs to one inbound connection and is not used concurrently.
type httpHeaderLimiter struct {
	source     io.Reader
	maxBytes   int
	headerSize int
	previous   byte
	beforePrev byte
	inHeader   bool
	tooLarge   bool
}

func newHTTPHeaderLimiter(source io.Reader, maxBytes int) *httpHeaderLimiter {
	return &httpHeaderLimiter{source: source, maxBytes: maxBytes}
}

func (l *httpHeaderLimiter) beginRequest(reader *bufio.Reader) {
	l.headerSize = 0
	l.previous = 0
	l.beforePrev = 0
	l.inHeader = true
	l.tooLarge = false

	// Peek(Buffered()) never fetches more bytes from the connection.
	// HTTP pipelining can leave the next request's header in this buffer.
	if n := reader.Buffered(); n > 0 {
		buffered, _ := reader.Peek(n)
		l.scan(buffered)
	}
}

func (l *httpHeaderLimiter) Read(p []byte) (int, error) {
	if l.tooLarge {
		return 0, errHTTPHeaderTooLarge
	}
	n, err := l.source.Read(p)
	l.scan(p[:n])
	return n, err
}

func (l *httpHeaderLimiter) scan(data []byte) {
	if !l.inHeader || l.tooLarge {
		return
	}
	for _, b := range data {
		l.headerSize++
		if l.headerSize > l.maxBytes {
			l.tooLarge = true
			return
		}

		// An empty HTTP header line ends with either LF LF, LF CR LF,
		// or CR LF CR LF; Go's HTTP parser accepts LF-only lines too.
		if b == '\n' && (l.previous == '\n' ||
			(l.previous == '\r' && l.beforePrev == '\n')) {
			l.inHeader = false
			return
		}
		l.beforePrev, l.previous = l.previous, b
	}
}

// HTTP body I/O has a rolling timeout, not a total transfer duration.
// Tune this for deliberately very slow rate limits or long silent streams.
const httpHeaderTimeout = 30 * time.Second
const httpIdleTimeout = 90 * time.Second
const httpStallTimeout = 2 * time.Minute
const httpErrorWriteTimeout = 10 * time.Second
const maxHTTPResponseHeaderBytes = 64 * 1024

// timedHTTPIO joins an expired callback before returning, so an old timer can
// never close a connection after it has been returned to a connection pool.
func timedHTTPIO(timeout time.Duration, abort func(), operation func() (int, error)) (int, error) {
	done := make(chan struct{})
	timer := time.AfterFunc(timeout, func() { defer close(done); abort() })
	n, err := operation()
	if !timer.Stop() {
		<-done
		return n, fmt.Errorf("HTTP transfer stalled: %w", os.ErrDeadlineExceeded)
	}
	return n, err
}

type httpStallConn struct {
	net.Conn
	timeout time.Duration
}

// Read 透传。Transport 可能在请求体仍持续上传时并发等待响应头。
func (c *httpStallConn) Write(p []byte) (int, error) {
	// Bound each write even when Response.Write supplies a very large slice.
	total := 0
	for len(p) > 0 {
		size := len(p)
		if size > 32*1024 {
			size = 32 * 1024
		}
		n, err := timedHTTPIO(c.timeout, func() { _ = c.Conn.Close() }, func() (int, error) { return c.Conn.Write(p[:size]) })
		total += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// Track body completion without draining an early-rejected request. Transport
// owns Close, but must not drain the downstream bufio.Reader concurrently with
// the next request. Handle closes the connection unless the body was consumed.
type httpBodyReader struct {
	source    io.ReadCloser
	abort     context.CancelFunc
	timeout   time.Duration
	remaining int64
	complete  atomic.Bool
	request   bool
}

func (b *httpBodyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := timedHTTPIO(b.timeout, b.abort, func() (int, error) { return b.source.Read(p) })
	if b.remaining > 0 {
		b.remaining -= int64(n)
		if b.remaining == 0 {
			b.complete.Store(true)
		}
	}
	if err == io.EOF {
		b.complete.Store(true)
	}
	return n, err
}
func (b *httpBodyReader) Close() error {
	if b.request {
		return nil
	}
	return b.source.Close()
}

func hasHTTPToken(h http.Header, field, token string) bool {
	for _, value := range h.Values(field) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// ReadResponse alone has no header byte budget. Reuse the header scanner and
// keep its bufio.Reader for the tunnel so any read-ahead bytes are preserved.
func readConnectResponse(conn net.Conn, req *http.Request) (*http.Response, *bufio.Reader, error) {
	limiter := newHTTPHeaderLimiter(conn, maxHTTPResponseHeaderBytes)
	reader := bufio.NewReader(limiter)
	remaining := maxHTTPResponseHeaderBytes
	for {
		limiter.maxBytes = remaining
		limiter.beginRequest(reader)
		if limiter.tooLarge {
			return nil, nil, fmt.Errorf("CONNECT response headers too large")
		}
		resp, err := http.ReadResponse(reader, req)
		if limiter.tooLarge {
			return nil, nil, fmt.Errorf("CONNECT response headers too large")
		}
		if err != nil {
			return nil, nil, err
		}
		remaining -= limiter.headerSize
		if resp.StatusCode >= 100 && resp.StatusCode < 200 && resp.StatusCode != http.StatusSwitchingProtocols {
			continue
		}
		return resp, reader, nil
	}
}
