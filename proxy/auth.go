package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	authMagic       = "ENPA"
	authVersion     = byte(1)
	authRequestType = byte(1)
	authReplyType   = byte(2)
	authOK          = byte(0)
	authFailed      = byte(1)
	authTimeout     = 10 * time.Second
	maxAuthPayload  = 64 * 1024
)

type AuthRequest struct {
	Username string
	Password string
}

// EncodeAuthRequest 编码认证请求 Payload。外层加密/长度帧由 writeAuthFrame 负责。
func EncodeAuthRequest(username, password string) ([]byte, error) {
	if len(username) == 0 || len(password) == 0 {
		return nil, fmt.Errorf("username and password must not be empty")
	}
	if len(username) > 0xffff || len(password) > 0xffff {
		return nil, fmt.Errorf("username or password is too long")
	}

	payload := make([]byte, 10+len(username)+len(password))
	copy(payload[:4], authMagic)
	payload[4] = authVersion
	payload[5] = authRequestType
	binary.BigEndian.PutUint16(payload[6:8], uint16(len(username)))
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(password)))
	copy(payload[10:], username)
	copy(payload[10+len(username):], password)
	return payload, nil
}

func DecodeAuthRequest(payload []byte) (*AuthRequest, error) {
	if len(payload) < 10 || string(payload[:4]) != authMagic {
		return nil, fmt.Errorf("invalid authentication request header")
	}
	if payload[4] != authVersion || payload[5] != authRequestType {
		return nil, fmt.Errorf("unsupported authentication request version or type")
	}

	usernameLen := int(binary.BigEndian.Uint16(payload[6:8]))
	passwordLen := int(binary.BigEndian.Uint16(payload[8:10]))
	if usernameLen == 0 || passwordLen == 0 || 10+usernameLen+passwordLen != len(payload) {
		return nil, fmt.Errorf("invalid authentication request length")
	}

	return &AuthRequest{
		Username: string(payload[10 : 10+usernameLen]),
		Password: string(payload[10+usernameLen:]),
	}, nil
}

func EncodeAuthResponse(success bool) []byte {
	status := authFailed
	if success {
		status = authOK
	}
	return []byte{authMagic[0], authMagic[1], authMagic[2], authMagic[3], authVersion, authReplyType, status}
}

func DecodeAuthResponse(payload []byte) error {
	if len(payload) != 7 || string(payload[:4]) != authMagic {
		return fmt.Errorf("invalid authentication response")
	}
	if payload[4] != authVersion || payload[5] != authReplyType {
		return fmt.Errorf("unsupported authentication response version or type")
	}
	if payload[6] != authOK {
		return fmt.Errorf("authentication rejected")
	}
	return nil
}

// readAuthFrame：加密模式复用项目现有 AES-GCM CryptoCodec；明文模式使用
// 4 字节大端长度 + Payload，避免 PlainCodec.Read() 受到 TCP 粘包/拆包影响。
func readAuthFrame(conn net.Conn, crypto CryptoConfig) ([]byte, error) {
	if crypto.Enabled {
		codec, err := NewCodec(true, crypto.Key)
		if err != nil {
			return nil, err
		}
		return codec.Decode(conn, nil)
	}

	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxAuthPayload {
		return nil, fmt.Errorf("invalid authentication frame size: %d", length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("read authentication payload: %w", err)
	}
	return payload, nil
}

func writeAuthFrame(conn net.Conn, payload []byte, crypto CryptoConfig) error {
	if len(payload) == 0 || len(payload) > maxAuthPayload {
		return fmt.Errorf("invalid authentication payload size: %d", len(payload))
	}
	if crypto.Enabled {
		codec, err := NewCodec(true, crypto.Key)
		if err != nil {
			return err
		}
		_, err = codec.Encode(conn, payload)
		return err
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writeFull(conn, header[:]); err != nil {
		return err
	}
	_, err := writeFull(conn, payload)
	return err
}

func (p *Proxy) authenticate(conn net.Conn) (*Account, error) {
	if err := conn.SetDeadline(time.Now().Add(authTimeout)); err != nil {
		return nil, fmt.Errorf("set authentication deadline: %w", err)
	}
	defer conn.SetDeadline(time.Time{})

	payload, err := readAuthFrame(conn, p.inbound.Crypto)
	if err != nil {
		return nil, fmt.Errorf("read authentication frame: %w", err)
	}

	request, err := DecodeAuthRequest(payload)
	if err != nil {
		_ = writeAuthFrame(conn, EncodeAuthResponse(false), p.inbound.Crypto)
		return nil, err
	}

	account, authErr := p.accounts.Authenticate(request.Username, request.Password)
	if authErr != nil {
		if err := writeAuthFrame(conn, EncodeAuthResponse(false), p.inbound.Crypto); err != nil {
			return nil, fmt.Errorf("authentication rejected and response write failed: %w", err)
		}
		return nil, fmt.Errorf("authentication failed")
	}

	if err := writeAuthFrame(conn, EncodeAuthResponse(true), p.inbound.Crypto); err != nil {
		return nil, fmt.Errorf("write authentication response: %w", err)
	}
	return account, nil
}
