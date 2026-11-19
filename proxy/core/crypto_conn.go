package core

import (
	"io"
	"net"
	"sync"
)

type CryptoConn struct {
	net.Conn
	decoder Codec
	encoder Codec
	readMu  sync.Mutex
	writeMu sync.Mutex
	pending []byte
	readBuf []byte // 每连接只分配一次；不能在 pending 消费完之前覆盖
}

func NewCryptoConn(conn net.Conn, key string) (*CryptoConn, error) {
	dec, err := NewCodec(true, key)
	if err != nil {
		return nil, err
	}
	enc, err := NewCodec(true, key)
	if err != nil {
		return nil, err
	}
	return &CryptoConn{Conn: conn, decoder: dec, encoder: enc}, nil
}

func (c *CryptoConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	for len(c.pending) == 0 {
		if c.readBuf == nil {
			c.readBuf = make([]byte, cryptoBufferSize)
		}
		payload, err := c.decoder.Decode(c.Conn, c.readBuf)
		if err != nil {
			return 0, err
		}
		if len(payload) == 0 {
			continue
		}
		c.pending = payload
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *CryptoConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(b) != 0 {
		n := len(b)
		if n > framePayloadSize {
			n = framePayloadSize
		}
		if _, err := c.encoder.Encode(c.Conn, b[:n]); err != nil {
			return total, err
		}
		total += n
		b = b[n:]
	}
	return total, nil
}

func (c *CryptoConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return io.ErrClosedPipe
}
