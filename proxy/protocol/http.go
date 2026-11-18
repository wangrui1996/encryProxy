package protocol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"wangjui.com/enproxy/proxy/core"
)

// HTTP implements the inbound HTTP forward proxy: ordinary HTTP requests
// and HTTPS CONNECT tunnels. No TLS interception is performed.
type HTTP struct {
	Inbound    InboundOptions
	Routes     Resolver
	Outbounds  HTTPOutbounds
	Relay      RelayFunc
	transports sync.Map // map[OutboundTag]*http.Transport, shared by concurrent sessions
}

func NewHTTP(in InboundOptions, routes Resolver, outbounds HTTPOutbounds, relay RelayFunc) (*HTTP, error) {
	if in.Protocol != "http" || in.HTTP == nil {
		return nil, fmt.Errorf("HTTP inbound config required")
	}
	if routes == nil || outbounds == nil || relay == nil {
		return nil, fmt.Errorf("HTTP route resolver, dialer and relay are required")
	}
	return &HTTP{Inbound: in, Routes: routes, Outbounds: outbounds, Relay: relay}, nil
}

func (h *HTTP) Handle(ctx context.Context, conn net.Conn, authenticate AuthenticateFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	limiter := newHTTPHeaderLimiter(conn, maxHTTPHeaderBytes)
	reader := bufio.NewReaderSize(limiter, 32*1024)
	for {
		limiter.beginRequest(reader)
		if limiter.tooLarge {
			_ = writeHTTPError(conn, http.StatusRequestHeaderFieldsTooLarge)
			return errHTTPHeaderTooLarge
		}
		// Wait for the next request separately from parsing its headers.
		_ = conn.SetReadDeadline(time.Now().Add(httpIdleTimeout))
		if _, err := reader.Peek(1); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("wait for HTTP request: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(httpHeaderTimeout))
		req, err := http.ReadRequest(reader)
		_ = conn.SetReadDeadline(time.Time{})
		if limiter.tooLarge {
			// Return without draining a potentially large or unbounded body.
			// The caller closes the downstream connection when Handle returns.
			_ = writeHTTPError(conn, http.StatusRequestHeaderFieldsTooLarge)
			return fmt.Errorf("read HTTP proxy request: %w", errHTTPHeaderTooLarge)
		}
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
		if req.Header.Get("Upgrade") != "" || hasHTTPToken(req.Header, "Connection", "upgrade") {
			_ = writeHTTPError(conn, http.StatusNotImplemented)
			return nil
		}
		for _, value := range req.Header.Values("Expect") {
			if !strings.EqualFold(strings.TrimSpace(value), "100-continue") || req.Method == http.MethodConnect {
				_ = writeHTTPError(conn, http.StatusExpectationFailed)
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
func (h *HTTP) handleConnect(ctx context.Context, downstream net.Conn, downstreamReader *bufio.Reader, outbound string, target string) error {
	upstream, upstreamReader, err := h.Outbounds.Connect(ctx, outbound, target)
	if errors.Is(err, ErrBlackhole) {
		return downstream.Close()
	}
	if err != nil {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return err
	}
	defer upstream.Close()
	if _, err := io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return err
	}
	return h.Relay(ctx, downstream, downstreamReader, upstream, upstreamReader)
}

func (h *HTTP) getTransport(tag string) (*http.Transport, error) {
	if item, ok := h.transports.Load(tag); ok {
		return item.(*http.Transport), nil
	}
	tr, err := h.Outbounds.NewHTTPTransport(tag)
	if err != nil {
		return nil, err
	}
	existing, loaded := h.transports.LoadOrStore(tag, tr)
	if loaded {
		tr.CloseIdleConnections()
		return existing.(*http.Transport), nil
	}
	return tr, nil
}

func (h *HTTP) handleForward(ctx context.Context, downstream net.Conn, req *http.Request, outbound string) (bool, error) {
	tr, err := h.getTransport(outbound)
	if errors.Is(err, ErrBlackhole) {
		return false, downstream.Close()
	}
	if err != nil {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return false, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := core.CloseOnCancel(ctx, downstream)
	defer stop()
	abort := func() { cancel(); _ = downstream.Close() }
	downstreamIO := &httpStallConn{Conn: downstream, timeout: httpStallTimeout}
	if hasHTTPToken(req.Header, "Expect", "100-continue") && req.Body != nil && req.Body != http.NoBody {
		if _, err := io.WriteString(downstreamIO, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
			return false, err
		}
	}
	outReq := req.Clone(ctx)
	outReq.Header.Del("Expect")
	var requestBody *httpBodyReader
	if req.Body != nil && req.Body != http.NoBody {
		requestBody = &httpBodyReader{source: req.Body, abort: abort, timeout: httpStallTimeout, remaining: req.ContentLength, request: true}
		outReq.Body = requestBody
	}
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
	if resp.StatusCode == http.StatusSwitchingProtocols {
		_ = writeHTTPError(downstream, http.StatusBadGateway)
		return false, fmt.Errorf("unexpected HTTP upgrade from upstream")
	}
	// An early response may arrive before the request body has been consumed.
	// Do not parse another request from the same reader in that case.
	keepAlive := !req.Close && !resp.Close && (requestBody == nil || requestBody.complete.Load())
	if !keepAlive {
		resp.Close = true
	}
	// Transport may negotiate HTTP/2 upstream; downstream is HTTP/1.x.
	resp.Proto, resp.ProtoMajor, resp.ProtoMinor = req.Proto, req.ProtoMajor, req.ProtoMinor
	if req.ProtoMinor == 0 {
		resp.TransferEncoding = nil
		if resp.ContentLength < 0 {
			resp.Close = true
			keepAlive = false
		}
	} else if resp.ContentLength < 0 && req.Method != http.MethodHead && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		resp.TransferEncoding = []string{"chunked"}
	}
	resp.Body = &httpBodyReader{source: resp.Body, abort: abort, timeout: httpStallTimeout, remaining: -1}
	stripHopHeaders(resp.Header)
	if err := resp.Write(downstreamIO); err != nil {
		return false, fmt.Errorf("write HTTP proxy response: %w", err)
	}
	return keepAlive, nil
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
