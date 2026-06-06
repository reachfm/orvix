package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	encryptionKey []byte
	keyOnce       sync.Once
	keyErr        error
)

const encryptionKeyEnv = "ORVIX_ENCRYPTION_KEY"

// getEncryptionKey loads or generates the AES-256 encryption key.
func getEncryptionKey() ([]byte, error) {
	keyOnce.Do(func() {
		keyHex := os.Getenv(encryptionKeyEnv)
		if keyHex != "" {
			encryptionKey, keyErr = hex.DecodeString(keyHex)
			if keyErr == nil && len(encryptionKey) != 32 {
				keyErr = fmt.Errorf("encryption key must be 32 bytes (64 hex chars), got %d", len(encryptionKey))
				encryptionKey = nil
			}
			return
		}

		encryptionKey = make([]byte, 32)
		if _, err := rand.Read(encryptionKey); err != nil {
			keyErr = fmt.Errorf("failed to generate encryption key: %w", err)
			encryptionKey = nil
			return
		}
	})

	return encryptionKey, keyErr
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns hex(nonce) + ":" + hex(ciphertext).
func Encrypt(plaintext []byte) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return hex.EncodeToString(nonce) + ":" + hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts an encrypted string produced by Encrypt.
func Decrypt(encoded string) ([]byte, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return nil, err
	}

	parts := splitEncrypted(encoded)
	if parts == nil {
		return nil, errors.New("invalid encrypted format")
	}

	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid nonce: %w", err)
	}

	ciphertext, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// EncryptString encrypts a string and returns the encoded result.
func EncryptString(s string) (string, error) {
	return Encrypt([]byte(s))
}

// DecryptString decrypts an encoded string.
func DecryptString(encoded string) (string, error) {
	data, err := Decrypt(encoded)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func splitEncrypted(encoded string) []string {
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == ':' {
			return []string{encoded[:i], encoded[i+1:]}
		}
	}
	return nil
}
