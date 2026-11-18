package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// 在支持的情况下传播 TCP 半关闭
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

// 使用全局缓冲池执行数据复制，复制结束后归还缓冲区。
func copyWithBuffer(dst io.Writer, src io.Reader) (int64, error) {
	buf := getCopyBuffer()
	defer putCopyBuffer(buf)

	return io.CopyBuffer(dst, src, buf)
}

// 在双向复制数据时保留 TCP 半关闭语义，并在 ctx 取消或出错时关闭两端连接。
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

		closeWrite(dst) // 正常 EOF，尽可能保留 TCP 半关闭语义。
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
