package core

import (
    "encoding/binary"
    "fmt"
    "io"
)

const (
    framePayloadSize = 32 * 1024 // 每次最多读取 32 KB 明文
    maxFrameSize = 1024 * 1024 // 单条密文记录最大长度
)

type Codec interface {
    Decode(src io.Reader, buf []byte) ([]byte, error) // 从输入流读取数据并解码
    Encode(dst io.Writer, src []byte) (int, error) // 编码数据并写入输出流
}

// PlainCodec
type PlainCodec struct{}

func (c *PlainCodec) Decode(src io.Reader, buf []byte) ([]byte, error) {
    n, err := src.Read(buf)
    return buf[:n], err
}

func (c *PlainCodec) Encode(dst io.Writer, src []byte) (int, error) {
    return writeFull(dst, src)
}

// CryptoCodec
type CryptoCodec struct {
    key []byte
}

func NewCryptoCodec(key []byte) *CryptoCodec {
    return &CryptoCodec{
        key: key,
    }
}

// 加密并写入一条记录。
//
// 数据格式：
// +------------------+----------------------+
// | 4 bytes length   | encrypted payload    |
// +------------------+----------------------+
func (c *CryptoCodec) Encode(dst io.Writer, src []byte) (int, error) {
    encrypted, err := encrypt(c.key, src)
    if err != nil {
        return 0, fmt.Errorf("encrypt: %w", err)
    }

    if len(encrypted) == 0 || len(encrypted) > maxFrameSize {
        return 0, fmt.Errorf("invalid encrypted frame size: %d", len(encrypted))
    }

    var header [4]byte
    binary.BigEndian.PutUint32(header[:], uint32(len(encrypted)))

    n, err := writeFull(dst, header[:])
    if err != nil {
        return n, err
    }

    m, err := writeFull(dst, []byte(encrypted))
    return n + m, err
}

// 读取完整密文记录并解密
func (c *CryptoCodec) Decode(src io.Reader, buf []byte) ([]byte, error) {
    var header [4]byte

    _, err := io.ReadFull(src, header[:])
    if err != nil {
        return nil, err
    }

    frameSize := binary.BigEndian.Uint32(header[:])

    if frameSize == 0 || frameSize > maxFrameSize {
        return nil, fmt.Errorf("invalid encrypted frame size: %d", frameSize)
    }

    encrypted := make([]byte, int(frameSize))

    if _, err := io.ReadFull(src, encrypted); err != nil {
        return nil, fmt.Errorf("read encrypted frame: %w", err)
    }

    plaintext, err := decrypt(c.key, encrypted)
    if err != nil {
        return nil, fmt.Errorf("decrypt: %w", err)
    }

    return []byte(plaintext), nil
}

// Codec Factory
func NewCodec(enabled bool, key string) (Codec, error) {
    if !enabled {
        return &PlainCodec{}, nil
    }

    if key == "" {
        return nil, fmt.Errorf("crypto enabled but key is empty")
    }

    return NewCryptoCodec([]byte(key)), nil
}

// Write Helper
func writeFull(dst io.Writer, data []byte) (int, error) {
    total := 0

    for len(data) > 0 {
        n, err := dst.Write(data)

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
