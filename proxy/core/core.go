package core

import (
	"context"
	"net"
)

// CloseOnCancel guards a bounded handshake. stop joins the watcher before the
// connection is handed to its next owner (especially an HTTP connection pool).
func CloseOnCancel(ctx context.Context, conn net.Conn) func() {
	if ctx == nil {
		ctx = context.Background()
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	return func() { close(done); <-exited }
}
