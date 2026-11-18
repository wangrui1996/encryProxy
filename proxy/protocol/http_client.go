package protocol

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"wangjui.com/enproxy/proxy/core"
)

// HTTPClient implements the HTTP upstream proxy transport (client side).
// Its Dial is supplied by the outbound manager, so optional encrypted outer transport works.
type HTTPClient struct{ Dial DialFunc }

func (c HTTPClient) proxyURL(out *Outbound) (*url.URL, error) {
	if out == nil || out.HTTP == nil {
		return nil, fmt.Errorf("invalid HTTP outbound")
	}
	u := &url.URL{Scheme: "http", Host: out.Server}
	if out.HTTP.AuthRequired {
		u.User = url.UserPassword(out.HTTP.Account, out.HTTP.Passwd)
	}
	return u, nil
}

// Transport builds a transport for ordinary http/https URL requests.
// http.Transport automatically writes absolute-form HTTP proxy requests and
// establishes CONNECT when accessing absolute https:// URLs.
func (c HTTPClient) Transport(out *Outbound) (*http.Transport, error) {
	proxyURL, err := c.proxyURL(out)
	if err != nil {
		return nil, err
	}
	if c.Dial == nil {
		return nil, fmt.Errorf("outbound dialer is nil")
	}
	return newHTTPTransport(c.Dial, proxyURL, true), nil
}

// NewDirectHTTPTransport uses an origin stream for direct and Shadowsocks outbounds.
func NewDirectHTTPTransport(dial DialFunc, http2 bool) *http.Transport {
	return newHTTPTransport(dial, nil, http2)
}

func newHTTPTransport(dial DialFunc, proxyURL *url.URL, http2 bool) *http.Transport {
	var proxy func(*http.Request) (*url.URL, error)
	if proxyURL != nil {
		proxy = http.ProxyURL(proxyURL)
	}
	return &http.Transport{
		Proxy: proxy,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			conn, err := dial(ctx, address)
			if err != nil {
				return nil, err
			}
			return &httpStallConn{Conn: conn, timeout: httpStallTimeout}, nil
		},
		MaxResponseHeaderBytes: maxHTTPResponseHeaderBytes,
		MaxIdleConns:           64,
		MaxIdleConnsPerHost:    16,
		IdleConnTimeout:        60 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		DisableCompression:     true,
		ForceAttemptHTTP2:      http2,
	}
}

// Connect establishes an HTTP CONNECT tunnel through an upstream proxy.
// The returned bufio.Reader must be used by the relay, as it may hold early TLS bytes.
func (c HTTPClient) Connect(ctx context.Context, out *Outbound, target string) (net.Conn, *bufio.Reader, error) {
	if _, err := c.proxyURL(out); err != nil {
		return nil, nil, err
	}
	if c.Dial == nil {
		return nil, nil, fmt.Errorf("outbound dialer is nil")
	}
	upstream, err := c.Dial(ctx, out.Server)
	if err != nil {
		return nil, nil, err
	}
	stop := core.CloseOnCancel(ctx, upstream)
	defer stop()
	closeOnError := func(err error) (net.Conn, *bufio.Reader, error) { _ = upstream.Close(); return nil, nil, err }
	if err := upstream.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return closeOnError(err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: make(http.Header),
		Proto:  "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
	if out.HTTP.AuthRequired {
		creds := base64.StdEncoding.EncodeToString([]byte(out.HTTP.Account + ":" + out.HTTP.Passwd))
		req.Header.Set("Proxy-Authorization", "Basic "+creds)
	}
	if err := req.Write(upstream); err != nil {
		return closeOnError(fmt.Errorf("write CONNECT request: %w", err))
	}
	resp, reader, err := readConnectResponse(upstream, req)
	if err != nil {
		return closeOnError(fmt.Errorf("read CONNECT response: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		// Do not drain a potentially unbounded error body from an untrusted proxy.
		return closeOnError(fmt.Errorf("HTTP upstream CONNECT refused (%s)", resp.Status))
	}
	if err := upstream.SetDeadline(time.Time{}); err != nil {
		return closeOnError(err)
	}
	return upstream, reader, nil
}
