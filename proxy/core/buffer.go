package core

import "sync"

const CopyBufferSize = 32 * 1024

var copyBufferPool = sync.Pool{New: func() any { return make([]byte, CopyBufferSize) }}

func GetCopyBuffer() []byte { return copyBufferPool.Get().([]byte) }

func PutCopyBuffer(buf []byte) {
	if cap(buf) != CopyBufferSize {
		return
	}
	copyBufferPool.Put(buf[:CopyBufferSize])
}

// AES-GCM 每帧最多 32KiB 明文，额外包含 12-byte nonce 与 16-byte tag。
// 此池与 CopyBuffer 独立，避免不同长度混用。
const cryptoBufferSize = CopyBufferSize + 12 + 16

var cryptoBufferPool = sync.Pool{New: func() any { return make([]byte, cryptoBufferSize) }}

func getCryptoBuffer() []byte { return cryptoBufferPool.Get().([]byte) }
func putCryptoBuffer(b []byte) {
	if cap(b) != cryptoBufferSize {
		return
	}
	cryptoBufferPool.Put(b[:cryptoBufferSize])
}
