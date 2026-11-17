package core

import "sync"

var copyBufferPool = sync.Pool{New: func() any { return make([]byte, CopyBufferSize) }}

func GetCopyBuffer() []byte { return copyBufferPool.Get().([]byte) }

func PutCopyBuffer(buf []byte) {
	if cap(buf) != CopyBufferSize {
		return
	}
	copyBufferPool.Put(buf[:CopyBufferSize])
}

var cryptoBufferPool = sync.Pool{New: func() any { return make([]byte, cryptoBufferSize) }}

func getCryptoBuffer() []byte { return cryptoBufferPool.Get().([]byte) }
func putCryptoBuffer(b []byte) {
	if cap(b) != cryptoBufferSize {
		return
	}
	cryptoBufferPool.Put(b[:cryptoBufferSize])
}
