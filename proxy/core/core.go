package core

import (
	"context"
	"net"
)

const (
	// 定义加密流每帧发送的明文分块大小，单位为字节
	framePayloadSize = 32 * 1024
	// 自定义加密帧体的最大长度，单位为字节
	maxFrameSize     = 1024 * 1024
	// 个转发方向使用的复制缓冲区大小，单位为字节
	CopyBufferSize = 32 * 1024
	// 限速器的令牌桶容量，单位为字节，控制允许的短时突发量，不代表每秒传输速率
	rateLimitBurst = 32 * 1024
	// AES-GCM 每帧最多 32KiB 明文，额外包含 12-byte nonce 与 16-byte tag。此池与 CopyBuffer 独立，避免不同长度混用。
	cryptoBufferSize = CopyBufferSize + 12 + 16
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
