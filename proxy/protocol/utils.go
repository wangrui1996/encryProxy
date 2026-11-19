package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// validateTarget is shared by HTTP CONNECT, freedom and Shadowsocks requests.
// It validates host:port syntax without forcing DNS resolution at this layer.
func validateTarget(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid target address %q: %w", address, err)
	}
	if host == "" || strings.ContainsAny(host, " \r\n\t/@\\\x00") {
		return fmt.Errorf("invalid target host %q", host)
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("invalid target IPv6 address %q", host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid target port %q", port)
	}
	return nil
}

// SOCKS5-style destination encoding shared by Shadowsocks and future SOCKS handlers: ATYP | ADDR | PORT.
func readTargetAddress(reader io.Reader) (address, hostname string, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(reader, atyp[:]); err != nil {
		return "", "", err
	}
	switch atyp[0] {
	case 1: // IPv4
		var raw [4]byte
		if _, err = io.ReadFull(reader, raw[:]); err != nil {
			return "", "", err
		}
		hostname = net.IP(raw[:]).String()
	case 4: // IPv6
		var raw [16]byte
		if _, err = io.ReadFull(reader, raw[:]); err != nil {
			return "", "", err
		}
		hostname = net.IP(raw[:]).String()
	case 3: // Domain
		var n [1]byte
		if _, err = io.ReadFull(reader, n[:]); err != nil {
			return "", "", err
		}
		if n[0] == 0 {
			return "", "", fmt.Errorf("empty Shadowsocks hostname")
		}
		data := make([]byte, n[0])
		if _, err = io.ReadFull(reader, data); err != nil {
			return "", "", err
		}
		hostname = string(data)
	default:
		return "", "", fmt.Errorf("invalid Shadowsocks ATYP %d", atyp[0])
	}
	var port [2]byte
	if _, err = io.ReadFull(reader, port[:]); err != nil {
		return "", "", err
	}
	address = net.JoinHostPort(hostname, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	if err = validateTarget(address); err != nil {
		return "", "", err
	}
	return address, hostname, nil
}

func encodeTargetAddress(target string) ([]byte, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	host, portText, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portText)
	buf := make([]byte, 0, 1+1+len(host)+2)
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = append(buf, 1)
			buf = append(buf, ip4...)
		} else {
			buf = append(buf, 4)
			buf = append(buf, ip.To16()...)
		}
	} else {
		if len(host) > 255 || strings.ContainsAny(host, "\x00") {
			return nil, fmt.Errorf("invalid Shadowsocks target domain length")
		}
		buf = append(buf, 3, byte(len(host)))
		buf = append(buf, host...)
	}
	buf = binary.BigEndian.AppendUint16(buf, uint16(port))
	return buf, nil
}
