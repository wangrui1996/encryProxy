//go:build linux

package proxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// Linux netfilter SO_ORIGINAL_DST (IPv4 and IPv6). Only NAT REDIRECT sockets
// are supported; ordinary TCP listeners do not carry an original destination.
const soOriginalDst = 80

func originalDestination(conn net.Conn) (string, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return "", fmt.Errorf("accepted socket does not support SyscallConn")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return "", err
	}
	var target string
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		target, sockErr = readOriginalDst(fd)
	}); err != nil {
		return "", err
	}
	if sockErr != nil {
		return "", fmt.Errorf("SO_ORIGINAL_DST: %w (configure iptables/ip6tables NAT REDIRECT)", sockErr)
	}
	return target, nil
}

func readOriginalDst(fd uintptr) (string, error) {
	var lastErr error
	for _, level := range []int{syscall.IPPROTO_IP, syscall.IPPROTO_IPV6} {
		var addr [128]byte
		size := uint32(len(addr))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(level), soOriginalDst, uintptr(unsafe.Pointer(&addr[0])), uintptr(unsafe.Pointer(&size)), 0)
		if errno != 0 {
			lastErr = errno
			continue
		}
		if size < 8 {
			return "", fmt.Errorf("short SO_ORIGINAL_DST socket address: %d", size)
		}
		family := binary.NativeEndian.Uint16(addr[:2])
		port := binary.BigEndian.Uint16(addr[2:4])
		if port == 0 {
			return "", fmt.Errorf("SO_ORIGINAL_DST returned port zero")
		}
		switch family {
		case syscall.AF_INET:
			return net.JoinHostPort(net.IP(addr[4:8]).String(), fmt.Sprint(port)), nil
		case syscall.AF_INET6:
			if size < 28 {
				return "", fmt.Errorf("short IPv6 SO_ORIGINAL_DST socket address: %d", size)
			}
			return net.JoinHostPort(net.IP(addr[8:24]).String(), fmt.Sprint(port)), nil
		default:
			return "", fmt.Errorf("SO_ORIGINAL_DST returned unsupported family %d", family)
		}
	}
	return "", lastErr
}
