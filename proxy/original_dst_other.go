//go:build !linux

package proxy

import (
	"fmt"
	"net"
)

func originalDestination(_ net.Conn) (string, error) {
	return "", fmt.Errorf("transparent NAT REDIRECT inbound is supported on Linux only")
}
