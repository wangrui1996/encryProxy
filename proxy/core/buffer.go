package core

import "sync"


var copyBufferPool = sync.Pool{New: func() any { return make([]byte, CopyBufferSize) }}
var cryptoBufferPool = sync.Pool{New: func() any { return make([]byte, cryptoBufferSize) }}

func getCopyBuffer() []byte {
	return copyBufferPool.Get().([]byte)
}

func putCopyBuffer(buf []byte) {
	if cap(buf) != CopyBufferSize {
		return
	}
	copyBufferPool.Put(buf[:CopyBufferSize])
}

func getCryptoBuffer() []byte {
	return cryptoBufferPool.Get().([]byte)
}

func putCryptoBuffer(b []byte) {
	if cap(b) != cryptoBufferSize {
		return
	}
	cryptoBufferPool.Put(b[:cryptoBufferSize])
}
