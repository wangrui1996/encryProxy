package protocol

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
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
	for _, field := range []string{"Connection", "Proxy-Connection"} {
		for _, item := range strings.Split(h.Get(field), ",") {
			if key := strings.TrimSpace(item); key != "" {
				h.Del(key)
			}
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(name)
	}
}

func writeHTTPError(conn net.Conn, code int) error {
	text := http.StatusText(code)
	_, err := fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", code, text)
	return err
}

func writeProxyAuthRequired(conn net.Conn) error {
	_, err := io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"enproxy\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	return err
}
