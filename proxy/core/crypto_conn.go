package core

import (
	"io"
	"net"
	"sync"
)

// CryptoConn adapts the existing per-frame AES-GCM Codec to net.Conn, allowing
// application protocols (HTTP in particular) to use the same encrypted tunnel.
// Read/Write each have independent locks, so full-duplex operation is supported.
type CryptoConn struct {
	net.Conn
	decoder Codec
	encoder Codec
	readMu  sync.Mutex
	writeMu sync.Mutex
	pending []byte
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
		payload, err := c.decoder.Decode(c.Conn, nil)
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
