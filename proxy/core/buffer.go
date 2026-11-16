
package core

import "sync"

const CopyBufferSize = 32 * 1024

// copyBufferPool 是 TCP 转发使用的全局缓冲池。
// 所有连接共享，降低高频连接场景的内存分配和 GC 开销。
var copyBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, CopyBufferSize)
	},
}

// GetCopyBuffer 获取一个独占的转发缓冲区。
//
// 使用者必须在不再使用缓冲区后调用 PutCopyBuffer。
// sync.Pool 中的对象可能被 GC 自动清理。
func GetCopyBuffer() []byte {
	return copyBufferPool.Get().([]byte)
}

// PutCopyBuffer 归还转发缓冲区。
//
// 归还后，调用者不得再读写或持有该缓冲区。
func PutCopyBuffer(buf []byte) {
	if cap(buf) != CopyBufferSize {
		return
	}

	copyBufferPool.Put(buf[:CopyBufferSize])
}
