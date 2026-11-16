package protocol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"wangjui.com/enproxy/proxy/config"
)

// HTTP implements the inbound HTTP forward proxy: ordinary HTTP requests
// and HTTPS CONNECT tunnels. No TLS interception is performed.
type HTTP struct {
	Inbound    config.InboundConfig
	Routes     Resolver
	Dial       DialFunc
	Relay      RelayFunc
	transports sync.Map // map[OutboundTag]*http.Transport, shared by concurrent sessions
}

func NewHTTP(in config.InboundConfig, routes Resolver, dial DialFunc, relay RelayFunc) (*HTTP, error) {
	if in.Protocol != "http" || in.HTTP == nil {
		return nil, fmt.Errorf("HTTP inbound config required")
	}
	if routes == nil || dial == nil || relay == nil {
		return nil, fmt.Errorf("HTTP route resolver, dialer and relay are required")
	}
	return &HTTP{Inbound: in, Routes: routes, Dial: dial, Relay: relay}, nil
}

func (h *HTTP) Handle(ctx context.Context, conn net.Conn, authenticate AuthenticateFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reader := bufio.NewReaderSize(conn, 32*1024)
	for {
		// Deadline only covers the next request line/headers, not a streaming body.
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		req, err := http.ReadRequest(reader)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = writeHTTPError(conn, http.StatusBadRequest)
			return fmt.Errorf("read HTTP proxy request: %w", err)
		}
		if h.Inbound.HTTP.AuthRequired {
			username, password, ok := parseProxyBasic(req.Header.Get("Proxy-Authorization"))
			if !ok || authenticate == nil || authenticate(username, password) != nil {
				// Do not drain an unauthenticated request body.
				_ = writeProxyAuthRequired(conn)
				return nil
			}
		}
		target, hostname, err := targetOfRequest(req)
		if err != nil {
			_ = writeHTTPError(conn, http.StatusBadRequest)
			return err
		}
		outbound, err := h.Routes.ResolveProxy(h.Inbound.Tag, hostname)
		if err != nil {
			_ = writeHTTPError(conn, http.StatusBadGateway)
			return err
		}
		if outbound == nil {
			_ = writeHTTPError(conn, http.StatusBadGateway)
			return fmt.Errorf("nil outbound for %s", hostname)
		}
		if outbound.Protocol == "blackhole" {
			// Silence, rather than an error response, is the entire point of blackhole.
			return (Blackhole{}).Handle(conn)
		}
		if req.Method == http.MethodConnect {
			return h.handleConnect(ctx, conn, reader, outbound, target)
		}
		keepAlive, err := h.handleForward(ctx, conn, req, outbound)
		if err != nil {
			return err
		}
		if !keepAlive {
			return nil
		}
	}
}

// handleConnect tunnels TLS without inspecting or modifying the encrypted application data.
func (h *HTTP) handleConnect(ctx context.Context, downstream net.Conn, downstreamReader *bufio.Reader, outbound *config.OutboundConfig, target string) error {
	upstream, upstreamReader, err := connectTarget(ctx, h.Dial, outbound, target)
	if err != nil {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return err
	}
	defer upstream.Close()
	if _, err := io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return err
	}
	return h.Relay(downstream, downstreamReader, upstream, upstreamReader)
}

func (h *HTTP) getTransport(out *config.OutboundConfig) (*http.Transport, error) {
	if item, ok := h.transports.Load(out.Tag); ok {
		return item.(*http.Transport), nil
	}
	var tr *http.Transport
	switch out.Protocol {
	case "freedom":
		tr = &http.Transport{
			DialContext:  func(ctx context.Context, _, address string) (net.Conn, error) { return h.Dial(ctx, out, address) },
			MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
			IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second, DisableCompression: true,
			ForceAttemptHTTP2: true,
		}
	case "http":
		var err error
		tr, err = (HTTPClient{Dial: h.Dial}).Transport(out)
		if err != nil {
			return nil, err
		}
	case "shadowsocks":
		// http.Transport uses origin-form HTTP over the Shadowsocks TCP target stream.
		// HTTPS URLs negotiate TLS with the destination through this stream.
		tr = &http.Transport{
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				return (ShadowsocksClient{Dial: h.Dial}).Connect(ctx, out, address)
			},
			MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
			IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second, DisableCompression: true,
		}
	default:
		return nil, fmt.Errorf("unsupported HTTP outbound %q", out.Protocol)
	}
	existing, loaded := h.transports.LoadOrStore(out.Tag, tr)
	if loaded {
		tr.CloseIdleConnections()
		return existing.(*http.Transport), nil
	}
	return tr, nil
}

func (h *HTTP) handleForward(ctx context.Context, downstream net.Conn, req *http.Request, outbound *config.OutboundConfig) (bool, error) {
	tr, err := h.getTransport(outbound)
	if err != nil {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return false, err
	}
	outReq := req.Clone(ctx)
	outReq.RequestURI = ""
	outReq.Host = outReq.URL.Host
	// Do not leak downstream proxy credentials or hop-by-hop headers upstream.
	stripHopHeaders(outReq.Header)
	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return false, fmt.Errorf("HTTP forward %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	stripHopHeaders(resp.Header)
	if err := resp.Write(downstream); err != nil {
		return false, fmt.Errorf("write HTTP proxy response: %w", err)
	}
	return !req.Close && !resp.Close, nil
}

// Close is called after sessions stop, so no new transports can be created.
func (h *HTTP) Close() error {
	h.transports.Range(func(key, value interface{}) bool {
		value.(*http.Transport).CloseIdleConnections()
		h.transports.Delete(key)
		return true
	})
	return nil
}
