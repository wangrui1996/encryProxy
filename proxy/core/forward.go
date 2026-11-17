package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// closeWrite propagates TCP half-closes where available.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface {
		CloseWrite() error
	}); ok {
		if err := cw.CloseWrite(); err != nil {
			_ = c.Close()
		}
	} else {
		_ = c.Close()
	}
}

// copyWithBuffer 使用全局缓冲池执行数据复制。
//
// 缓冲区在整个复制过程中独占，复制结束后归还。
// io.CopyBuffer 保留 io.Copy 的标准复制语义。
func copyWithBuffer(dst io.Writer, src io.Reader) (int64, error) {
	buf := GetCopyBuffer()
	defer PutCopyBuffer(buf)

	return io.CopyBuffer(dst, src, buf)
}

// Relay copies both directions, preserving buffered bytes and
// TCP half-close.
//
// 使用全局缓冲池，避免每次创建连接都分配新的复制缓冲区。
func Relay(downstream net.Conn, downReader io.Reader, upstream net.Conn, upReader io.Reader) error {
	return RelayContext(context.Background(), downstream, downReader, upstream, upReader)
}

// RelayContext preserves half-close on EOF and closes both ends on cancellation.
func RelayContext(ctx context.Context, downstream net.Conn, downReader io.Reader, upstream net.Conn, upReader io.Reader) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			downstream.Close()
			upstream.Close()
		case <-done:
		}
	}()
	defer func() { close(done); <-watcherDone }()

	var wg sync.WaitGroup
	wg.Add(2)

	errs := make(chan error, 2)

	copyOne := func(dst net.Conn, src io.Reader, label string) {
		defer wg.Done()

		// 从缓冲池获取 buf，复制完成后自动归还。
		_, err := copyWithBuffer(dst, src)

		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				errs <- fmt.Errorf("%s: %w", label, err)
			}

			_ = dst.Close()
			_ = downstream.Close()
			_ = upstream.Close()
			return
		}

		// 正常 EOF，尽可能保留 TCP 半关闭语义。
		closeWrite(dst)
	}

	go copyOne(
		upstream,
		downReader,
		"client -> target",
	)

	go copyOne(
		downstream,
		upReader,
		"target -> client",
	)

	wg.Wait()
	close(errs)

	for err := range errs {
		return err
	}

	return nil
}
