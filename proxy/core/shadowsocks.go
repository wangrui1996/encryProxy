package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)


const maxSSChunk = 0x3fff

var errSSReplay = errors.New("replayed Shadowsocks TCP salt")

type ShadowsocksCipher struct {
	method string
	key    []byte
	seen   *ssSaltCache // only enabled for inbound streams
}

func NewShadowsocksCipher(method, password string, inbound bool) (*ShadowsocksCipher, error) {
	keySize := 32
	switch method {
	case "aes-128-gcm":
		keySize = 16
	case "aes-256-gcm", "chacha20-ietf-poly1305":
	default:
		return nil, fmt.Errorf("unsupported Shadowsocks method %q", method)
	}
	if password == "" {
		return nil, errors.New("Shadowsocks password is empty")
	}
	s := &ShadowsocksCipher{method: method, key: ssEVPBytesToKey([]byte(password), keySize)}
	if inbound {
		s.seen = &ssSaltCache{entries: make(map[[sha256.Size]byte]time.Time)}
	}
	return s, nil
}

func ssEVPBytesToKey(password []byte, keyLen int) []byte {
	key := make([]byte, 0, keyLen)
	var last []byte
	for len(key) < keyLen {
		hash := md5.New() // Required for interoperability with classic Shadowsocks.
		_, _ = hash.Write(last)
		_, _ = hash.Write(password)
		last = hash.Sum(nil)
		key = append(key, last...)
	}
	return key[:keyLen]
}

func (s *ShadowsocksCipher) newAEAD(salt []byte) (cipher.AEAD, error) {
	if len(salt) != len(s.key) {
		return nil, fmt.Errorf("invalid Shadowsocks salt size %d", len(salt))
	}
	subkey := make([]byte, len(s.key))
	if _, err := io.ReadFull(hkdf.New(sha1.New, s.key, salt, []byte("ss-subkey")), subkey); err != nil {
		return nil, err
	}
	switch s.method {
	case "aes-128-gcm", "aes-256-gcm":
		block, err := aes.NewCipher(subkey)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	case "chacha20-ietf-poly1305":
		return chacha20poly1305.New(subkey)
	default:
		return nil, fmt.Errorf("unsupported Shadowsocks method %q", s.method)
	}
}

type ssSaltCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]time.Time
}

func (c *ssSaltCache) accept(salt []byte) error {
	if c == nil {
		return nil
	}
	id := sha256.Sum256(salt)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if exp, ok := c.entries[id]; ok && exp.After(now) {
		return errSSReplay
	}
	if len(c.entries) >= 65536 {
		for k, expiry := range c.entries {
			if !expiry.After(now) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= 65536 {
			// Evict the oldest only when saturated.
			var oldest [sha256.Size]byte
			var t time.Time
			for k, expiry := range c.entries {
				if t.IsZero() || expiry.Before(t) {
					oldest, t = k, expiry
				}
			}
			delete(c.entries, oldest)
		}
	}
	c.entries[id] = now.Add(10 * time.Minute)
	return nil
}

type ShadowsocksConn struct {
	net.Conn
	suite      *ShadowsocksCipher
	readMu     sync.Mutex
	writeMu    sync.Mutex
	readAEAD   cipher.AEAD
	writeAEAD  cipher.AEAD
	readNonce  [12]byte
	writeNonce [12]byte
	pending    []byte
	readBuf    []byte // 每连接复用，pending 未消费完前不覆盖
	readSalt   []byte
	checked    bool
}

func NewShadowsocksConn(conn net.Conn, suite *ShadowsocksCipher) *ShadowsocksConn {
	return &ShadowsocksConn{Conn: conn, suite: suite}
}

func advanceSSNonce(n *[12]byte) error {
	for i := range n {
		n[i]++
		if n[i] != 0 {
			return nil
		}
	}
	return errors.New("Shadowsocks nonce exhausted")
}

func writeSSFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

func (s *ShadowsocksConn) initReader() error {
	if s.readAEAD != nil {
		return nil
	}
	salt := make([]byte, len(s.suite.key))
	if _, err := io.ReadFull(s.Conn, salt); err != nil {
		return err
	}
	a, err := s.suite.newAEAD(salt)
	if err != nil {
		return err
	}
	s.readAEAD, s.readSalt = a, salt
	return nil
}

func (s *ShadowsocksConn) openChunk(size int) ([]byte, error) {
	needed := size + s.readAEAD.Overhead()
	if s.readBuf == nil {
		s.readBuf = make([]byte, maxSSChunk+s.readAEAD.Overhead())
	}
	encrypted := s.readBuf[:needed]
	if _, err := io.ReadFull(s.Conn, encrypted); err != nil {
		return nil, err
	}
	plain, err := s.readAEAD.Open(encrypted[:0], s.readNonce[:], encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("Shadowsocks authentication failed: %w", err)
	}
	if err := advanceSSNonce(&s.readNonce); err != nil {
		return nil, err
	}
	return plain, nil
}

func (s *ShadowsocksConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for len(s.pending) == 0 {
		if err := s.initReader(); err != nil {
			return 0, err
		}
		length, err := s.openChunk(2)
		if err != nil {
			return 0, err
		}
		if !s.checked {
			if err := s.suite.seen.accept(s.readSalt); err != nil {
				return 0, err
			}
			s.checked = true
		}
		size := int(length[0])<<8 | int(length[1])
		if size <= 0 || size > maxSSChunk {
			return 0, fmt.Errorf("invalid Shadowsocks chunk length %d", size)
		}
		data, err := s.openChunk(size)
		if err != nil {
			return 0, err
		}
		s.pending = data
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *ShadowsocksConn) initWriter() error {
	if s.writeAEAD != nil {
		return nil
	}
	salt := make([]byte, len(s.suite.key))
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	a, err := s.suite.newAEAD(salt)
	if err != nil {
		return err
	}
	if err := writeSSFull(s.Conn, salt); err != nil {
		return err
	}
	s.writeAEAD = a
	return nil
}

func (s *ShadowsocksConn) sealChunk(p []byte, scratch []byte) error {
	sealed := s.writeAEAD.Seal(scratch[:0], s.writeNonce[:], p, nil)
	if err := advanceSSNonce(&s.writeNonce); err != nil {
		return err
	}
	return writeSSFull(s.Conn, sealed)
}

func (s *ShadowsocksConn) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.initWriter(); err != nil {
		return 0, err
	}
	scratch := getCopyBuffer()
	defer putCopyBuffer(scratch)
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxSSChunk {
			n = maxSSChunk
		}
		header := [2]byte{byte(n >> 8), byte(n)}
		if err := s.sealChunk(header[:], scratch); err != nil {
			return total, err
		}
		if err := s.sealChunk(p[:n], scratch); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (s *ShadowsocksConn) CloseWrite() error {
	if cw, ok := s.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}
