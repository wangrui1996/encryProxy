package core

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

const (
	framePayloadSize = 32 * 1024
	maxFrameSize     = 1024 * 1024
)

type Codec interface {
	// Decode 返回值可能引用 buf；调用方在使用结束前不得修改或复用 buf。
	Decode(src io.Reader, buf []byte) ([]byte, error)
	Encode(dst io.Writer, src []byte) (int, error)
}

type PlainCodec struct{}

func (c *PlainCodec) Decode(src io.Reader, buf []byte) ([]byte, error) {
	if len(buf) == 0 {
		buf = make([]byte, framePayloadSize)
	}
	n, err := src.Read(buf)
	return buf[:n], err
}
func (c *PlainCodec) Encode(dst io.Writer, src []byte) (int, error) { return writeFull(dst, src) }

type CryptoCodec struct {
	key     []byte
	once    sync.Once
	aead    cipher.AEAD
	initErr error
}

// 保留原有构造函数签名；首次 Encode/Decode 时仅初始化一次 AEAD。
func NewCryptoCodec(key []byte) *CryptoCodec {
	return &CryptoCodec{key: append([]byte(nil), key...)}
}
func (c *CryptoCodec) getAEAD() (cipher.AEAD, error) {
	c.once.Do(func() { c.aead, c.initErr = newAESGCM(c.key) })
	return c.aead, c.initErr
}

// 线协议完全不变：4-byte big endian length | random nonce | ciphertext | tag。
func (c *CryptoCodec) Encode(dst io.Writer, src []byte) (int, error) {
	a, err := c.getAEAD()
	if err != nil {
		return 0, fmt.Errorf("encrypt: %w", err)
	}
	nonceLen := a.NonceSize()
	frameLen := nonceLen + len(src) + a.Overhead()
	if frameLen == 0 || frameLen > maxFrameSize {
		return 0, fmt.Errorf("invalid encrypted frame size: %d", frameLen)
	}
	var frame []byte
	pooled := frameLen <= cryptoBufferSize
	if pooled {
		frame = getCryptoBuffer()[:frameLen]
		defer putCryptoBuffer(frame)
	} else {
		frame = make([]byte, frameLen)
	}
	nonce := frame[:nonceLen]
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return 0, fmt.Errorf("generate nonce: %w", err)
	}
	sealed := a.Seal(frame[:nonceLen], nonce, src, nil)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(sealed)))
	n, err := writeFull(dst, header[:])
	if err != nil {
		return n, err
	}
	m, err := writeFull(dst, sealed)
	return n + m, err
}

func (c *CryptoCodec) Decode(src io.Reader, buf []byte) ([]byte, error) {
	a, err := c.getAEAD()
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	var header [4]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header[:]))
	if size == 0 || size > maxFrameSize {
		return nil, fmt.Errorf("invalid encrypted frame size: %d", size)
	}
	if size < a.NonceSize()+a.Overhead() {
		return nil, fmt.Errorf("ciphertext too short: %d", size)
	}
	if cap(buf) < size {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	if _, err := io.ReadFull(src, buf); err != nil {
		return nil, fmt.Errorf("read encrypted frame: %w", err)
	}
	nonce := buf[:a.NonceSize()]
	encrypted := buf[a.NonceSize():]
	// 原地解密，无需再为明文分配内存；结果可能引用 buf。
	plaintext, err := a.Open(encrypted[:0], nonce, encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("authentication or decryption failed: %w", err)
	}
	return plaintext, nil
}

func NewCodec(enabled bool, key string) (Codec, error) {
	if !enabled {
		return &PlainCodec{}, nil
	}
	if key == "" {
		return nil, fmt.Errorf("crypto enabled but key is empty")
	}
	if n := len(key); n != 16 && n != 24 && n != 32 {
		return nil, fmt.Errorf("AES key must be 16, 24, or 32 bytes; got %d", n)
	}
	return NewCryptoCodec([]byte(key)), nil
}

func writeFull(dst io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := dst.Write(data)
		if n < 0 || n > len(data) {
			return total, fmt.Errorf("invalid write count %d", n)
		}
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
