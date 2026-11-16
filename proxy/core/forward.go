package core

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// closeWrite propagates TCP half-closes where available.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err != nil {
			_ = c.Close()
		}
	} else {
		_ = c.Close()
	}
}

// Relay copies both directions, preserving buffered bytes and TCP half-close.
func Relay(downstream net.Conn, downReader io.Reader, upstream net.Conn, upReader io.Reader) error {
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	copyOne := func(dst net.Conn, src io.Reader, label string) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				errs <- fmt.Errorf("%s: %w", label, err)
			}
			_ = dst.Close()
			_ = downstream.Close()
			_ = upstream.Close()
			return
		}
		closeWrite(dst)
	}
	go copyOne(upstream, downReader, "client -> target")
	go copyOne(downstream, upReader, "target -> client")
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}
