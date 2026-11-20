package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	maxSniffBytes = 32 * 1024
	sniffTimeout  = 300 * time.Millisecond
)

type sniffResult uint8

const (
	sniffNeedMore sniffResult = iota
	sniffNoMatch
	sniffFound
)

// sniffHostname observes the beginning of a transparent TCP connection without
// changing its payload. Every byte read here is replayed to the upstream.
// Only HTTP/1.x Host and TLS ClientHello SNI are inspected; no TLS decryption.
func sniffHostname(conn net.Conn) (string, io.Reader, error) {
	if err := conn.SetReadDeadline(time.Now().Add(sniffTimeout)); err != nil {
		return "", conn, fmt.Errorf("set sniff deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	buf := make([]byte, 0, 2048)
	var chunk [2048]byte
	for {
		host, status := inspectSniff(buf)
		if status == sniffFound || status == sniffNoMatch || len(buf) == maxSniffBytes {
			return host, io.MultiReader(bytes.NewReader(buf), conn), nil
		}
		maxRead := len(chunk)
		if remaining := maxSniffBytes - len(buf); remaining < maxRead {
			maxRead = remaining
		}
		n, err := conn.Read(chunk[:maxRead])
		buf = append(buf, chunk[:n]...)
		if err != nil {
			// A net.Conn.Read may return payload and EOF in the same call.
			if host, status := inspectSniff(buf); status == sniffFound {
				return host, io.MultiReader(bytes.NewReader(buf), conn), nil
			}
			if ne, ok := err.(net.Error); (ok && ne.Timeout()) || errors.Is(err, io.EOF) {
				// Missing or incomplete hostname is not fatal: route by original IP.
				return "", io.MultiReader(bytes.NewReader(buf), conn), nil
			}
			return "", nil, fmt.Errorf("read sniff prefix: %w", err)
		}
		if n == 0 {
			return "", io.MultiReader(bytes.NewReader(buf), conn), nil
		}
	}
}

func inspectSniff(data []byte) (string, sniffResult) {
	if len(data) == 0 {
		return "", sniffNeedMore
	}
	if data[0] == 0x16 { // TLS handshake record
		return sniffTLS(data)
	}
	if (data[0] >= 'A' && data[0] <= 'Z') || (data[0] >= 'a' && data[0] <= 'z') {
		return sniffHTTP(data)
	}
	return "", sniffNoMatch
}

func sniffHTTP(data []byte) (string, sniffResult) {
	lineEnd := bytes.Index(data, []byte("\r\n"))
	if lineEnd < 0 {
		if len(data) > 4096 || bytes.IndexByte(data, 0) >= 0 {
			return "", sniffNoMatch
		}
		return "", sniffNeedMore
	}
	first := string(data[:lineEnd])
	parts := strings.SplitN(first, " ", 3)
	if len(parts) != 3 || parts[0] == "" || (parts[2] != "HTTP/1.0" && parts[2] != "HTTP/1.1") {
		return "", sniffNoMatch
	}
	// Do not interpret binary protocols whose prefixes merely look like text.
	for _, b := range []byte(parts[0]) {
		if b < 'A' || b > 'Z' {
			return "", sniffNoMatch
		}
	}
	pos := lineEnd + 2
	for pos < len(data) {
		end := bytes.Index(data[pos:], []byte("\r\n"))
		if end < 0 {
			return "", sniffNeedMore
		}
		if end == 0 { // end of HTTP request headers
			return "", sniffNoMatch
		}
		line := data[pos : pos+end]
		if i := bytes.IndexByte(line, ':'); i >= 0 && strings.EqualFold(string(line[:i]), "Host") {
			host := normalizeSniffHost(string(bytes.TrimSpace(line[i+1:])), true)
			if host != "" {
				return host, sniffFound
			}
			return "", sniffNoMatch
		}
		pos += end + 2
	}
	return "", sniffNeedMore
}

func sniffTLS(data []byte) (string, sniffResult) {
	// A ClientHello handshake may span multiple TLS records and TCP packets.
	var handshake []byte
	for pos := 0; ; {
		if len(data)-pos < 5 {
			return "", sniffNeedMore
		}
		if data[pos] != 0x16 || data[pos+1] != 0x03 {
			return "", sniffNoMatch
		}
		recordLen := int(binary.BigEndian.Uint16(data[pos+3 : pos+5]))
		if recordLen == 0 || recordLen > 16384 || pos+5+recordLen > maxSniffBytes {
			return "", sniffNoMatch
		}
		if len(data)-pos < 5+recordLen {
			return "", sniffNeedMore
		}
		handshake = append(handshake, data[pos+5:pos+5+recordLen]...)
		if len(handshake) >= 4 {
			if handshake[0] != 1 { // ClientHello
				return "", sniffNoMatch
			}
			handshakeLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
			if handshakeLen > maxSniffBytes-4 {
				return "", sniffNoMatch
			}
			if len(handshake) >= handshakeLen+4 {
				host := parseTLSClientHello(handshake[4 : handshakeLen+4])
				if host == "" {
					return "", sniffNoMatch
				}
				return host, sniffFound
			}
		}
		pos += 5 + recordLen
	}
}

func parseTLSClientHello(hello []byte) string {
	// client_version(2) + random(32) + session_id + cipher_suites +
	// compression_methods + extensions.
	if len(hello) < 34+1 {
		return ""
	}
	pos := 34
	sessionLen := int(hello[pos])
	pos++
	if sessionLen > 32 || pos+sessionLen+2 > len(hello) {
		return ""
	}
	pos += sessionLen
	cipherLen := int(binary.BigEndian.Uint16(hello[pos : pos+2]))
	pos += 2
	if cipherLen == 0 || cipherLen%2 != 0 || pos+cipherLen+1 > len(hello) {
		return ""
	}
	pos += cipherLen
	compressionLen := int(hello[pos])
	pos++
	if compressionLen == 0 || pos+compressionLen+2 > len(hello) {
		return ""
	}
	pos += compressionLen
	extLen := int(binary.BigEndian.Uint16(hello[pos : pos+2]))
	pos += 2
	if pos+extLen != len(hello) {
		return ""
	}
	end := pos + extLen
	for pos+4 <= end {
		typ := binary.BigEndian.Uint16(hello[pos : pos+2])
		n := int(binary.BigEndian.Uint16(hello[pos+2 : pos+4]))
		pos += 4
		if pos+n > end {
			return ""
		}
		if typ == 0 && n >= 5 { // server_name extension
			data := hello[pos : pos+n]
			listLen := int(binary.BigEndian.Uint16(data[:2]))
			if listLen+2 != len(data) {
				return ""
			}
			for k := 2; k+3 <= len(data); {
				nameType := data[k]
				nameLen := int(binary.BigEndian.Uint16(data[k+1 : k+3]))
				k += 3
				if nameLen == 0 || k+nameLen > len(data) {
					return ""
				}
				if nameType == 0 {
					return normalizeSniffHost(string(data[k:k+nameLen]), false)
				}
				k += nameLen
			}
			return ""
		}
		pos += n
	}
	return ""
}

// Normalize untrusted Host/SNI data before using it for routing or dialling.
// Host may include an HTTP port, which is ignored: the original TCP port wins.
func normalizeSniffHost(value string, allowPort bool) string {
	value = strings.TrimSpace(value)
	if allowPort && strings.HasPrefix(value, "[") {
		if i := strings.IndexByte(value, ']'); i >= 0 {
			host := value[1:i]
			rest := value[i+1:]
			if rest == "" || strings.HasPrefix(rest, ":") && validSniffPort(rest[1:]) {
				if net.ParseIP(host) != nil {
					return strings.ToLower(host)
				}
			}
		}
		return ""
	}
	if allowPort && strings.Count(value, ":") == 1 {
		idx := strings.LastIndexByte(value, ':')
		if !validSniffPort(value[idx+1:]) {
			return ""
		}
		value = value[:idx]
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if len(value) == 0 || len(value) > 253 || strings.ContainsAny(value, ":/\\@ \t\r\n\x00") {
		return ""
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return ""
		}
		for _, ch := range []byte(part) {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return ""
			}
		}
	}
	return value
}

func validSniffPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}
