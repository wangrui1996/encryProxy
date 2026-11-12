
package proxy

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "fmt"
    "io"
)

func encrypt(key []byte, plaintext []byte) ([]byte, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, fmt.Errorf("create AES cipher: %w", err)
    }

    aead, err := cipher.NewGCM(block)
    if err != nil {
        return nil, fmt.Errorf("create GCM: %w", err)
    }

    nonce := make([]byte, aead.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return nil, fmt.Errorf("generate nonce: %w", err)
    }

    // 输出格式：nonce + ciphertext + authentication tag
    return aead.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key []byte, ciphertext []byte) ([]byte, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, fmt.Errorf("create AES cipher: %w", err)
    }

    aead, err := cipher.NewGCM(block)
    if err != nil {
        return nil, fmt.Errorf("create GCM: %w", err)
    }

    if len(ciphertext) < aead.NonceSize()+aead.Overhead() {
        return nil, fmt.Errorf("ciphertext too short: %d", len(ciphertext))
    }

    nonce := ciphertext[:aead.NonceSize()]
    encrypted := ciphertext[aead.NonceSize():]

    plaintext, err := aead.Open(nil, nonce, encrypted, nil)
    if err != nil {
        return nil, fmt.Errorf("authentication or decryption failed: %w", err)
    }

    return plaintext, nil
}
