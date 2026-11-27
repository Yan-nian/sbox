package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	encMagic       = "SBOXENC1"
	encSaltLen     = 16
	encNonceLen    = 12
	argon2Time     = 1
	argon2Memory   = 64 * 1024
	argon2Threads  = 4
	argon2KeyLen   = 32
)

// IsEncrypted reports whether data carries the SBOXENC1 encrypted backup header.
func IsEncrypted(data []byte) bool {
	return len(data) >= len(encMagic) && bytes.Equal(data[:len(encMagic)], []byte(encMagic))
}

func encryptPayload(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("backup: passphrase must not be empty for encryption")
	}

	salt := make([]byte, encSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("backup: generate salt: %w", err)
	}

	nonce := make([]byte, encNonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("backup: generate nonce: %w", err)
	}

	key := argon2.IDKey([]byte(passphrase), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: create gcm: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(encMagic))

	out := make([]byte, 0, len(encMagic)+encSaltLen+encNonceLen+len(ciphertext))
	out = append(out, []byte(encMagic)...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

func decryptPayload(data []byte, passphrase string) ([]byte, error) {
	if !IsEncrypted(data) {
		return data, nil
	}
	if passphrase == "" {
		return nil, errors.New("backup: archive is encrypted; passphrase is required")
	}

	headerLen := len(encMagic) + encSaltLen + encNonceLen
	if len(data) < headerLen+16 {
		return nil, errors.New("backup: corrupted encrypted archive (truncated payload)")
	}

	salt := data[len(encMagic) : len(encMagic)+encSaltLen]
	nonce := data[len(encMagic)+encSaltLen : headerLen]
	ciphertext := data[headerLen:]

	key := argon2.IDKey([]byte(passphrase), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: create gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(encMagic))
	if err != nil {
		return nil, errors.New("backup: decryption failed (wrong passphrase or corrupted archive)")
	}
	return plaintext, nil
}
