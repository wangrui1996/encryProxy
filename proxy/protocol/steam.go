package protocol

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

	"wangjui.com/enproxy/proxy/config"
)

// Classic Shadowsocks AEAD (SIP004/SIP007), NOT Shadowsocks 2022.
// Each TCP direction is: salt | AEAD(length) | AEAD(payload) | ...
const maxSSChunk = 0x3fff

var errSSReplay = errors.New("replayed Shadowsocks TCP salt")

type ssCipher struct {
	method string
	key    []byte
	seen   *ssSaltCache // only enabled for inbound streams
}

func newSSCipher(cfg *config.ShadowsocksConfig, inbound bool) (*ssCipher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	keySize := 32
	if cfg.Method == "aes-128-gcm" {
		keySize = 16
	}
	s := &ssCipher{method: cfg.Method, key: ssEVPBytesToKey([]byte(cfg.Password), keySize)}
	if inbound {
		s.seen = &ssSaltCache{entries: make(map[[sha256.Size]byte]time.Time)}
	}
	return s, nil
}

// EVP_BytesToKey with MD5 and one iteration (legacy Shadowsocks key derivation).
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

func (s *ssCipher) newAEAD(salt []byte) (cipher.AEAD, error) {
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

// Record only *authenticated* salts so attackers can't poison the replay cache
// by sending arbitrary bytes. Bounded storage also avoids unbounded growth.
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

type ssStream struct {
	net.Conn
	suite      *ssCipher
	readMu     sync.Mutex
	writeMu    sync.Mutex
	readAEAD   cipher.AEAD
	writeAEAD  cipher.AEAD
	readNonce  [12]byte
	writeNonce [12]byte
	pending    []byte
	readSalt   []byte
	checked    bool
}

func newSSStream(conn net.Conn, suite *ssCipher) *ssStream {
	return &ssStream{Conn: conn, suite: suite}
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

func (s *ssStream) initReader() error {
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

func (s *ssStream) openChunk(size int) ([]byte, error) {
	encrypted := make([]byte, size+s.readAEAD.Overhead())
	if _, err := io.ReadFull(s.Conn, encrypted); err != nil {
		return nil, err
	}
	plain, err := s.readAEAD.Open(nil, s.readNonce[:], encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("Shadowsocks authentication failed: %w", err)
	}
	if err := advanceSSNonce(&s.readNonce); err != nil {
		return nil, err
	}
	return plain, nil
}

func (s *ssStream) Read(p []byte) (int, error) {
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

func (s *ssStream) initWriter() error {
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

func (s *ssStream) sealChunk(p []byte) error {
	sealed := s.writeAEAD.Seal(nil, s.writeNonce[:], p, nil)
	if err := advanceSSNonce(&s.writeNonce); err != nil {
		return err
	}
	return writeSSFull(s.Conn, sealed)
}

func (s *ssStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.initWriter(); err != nil {
		return 0, err
	}
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxSSChunk {
			n = maxSSChunk
		}
		header := [2]byte{byte(n >> 8), byte(n)}
		if err := s.sealChunk(header[:]); err != nil {
			return total, err
		}
		if err := s.sealChunk(p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (s *ssStream) CloseWrite() error {
	if cw, ok := s.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}
