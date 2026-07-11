package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	encryptionKey []byte
	keyOnce       sync.Once
	keyErr        error
)

const (
	encryptionKeyEnv         = "ORVIX_ENCRYPTION_KEY"
	encryptionKeyPathEnv     = "ORVIX_ENCRYPTION_KEY_PATH"
	defaultEncryptionKeyPath = "/var/lib/orvix/encryption_key"
)

// getEncryptionKey resolves the AES-256 key with a stable, persistent
// lifecycle so that data encrypted at rest (license keys/metadata, MFA
// secrets) survives process restarts.
//
// Resolution order:
//  1. ORVIX_ENCRYPTION_KEY (64 hex chars) — explicit operator-provided key
//     (unchanged from prior behaviour, highest priority).
//  2. A persisted key file at ORVIX_ENCRYPTION_KEY_PATH, or the default under
//     /var/lib/orvix — loaded if present and valid.
//  3. Otherwise a new random key is generated AND persisted to that file so
//     the next start reuses it.
//
// Only if the key cannot be persisted does the process fall back to an
// in-memory key, logging a loud warning. This mirrors the JWT signing key's
// degradation contract (auth.loadOrGenerateKey) and never fails the process,
// so tests/CI with a non-writable key path still function within a run.
//
// Prior behaviour generated an ephemeral in-memory key on every start with no
// persistence, silently making previously encrypted data undecryptable after a
// restart (finding C-3).
func getEncryptionKey() ([]byte, error) {
	keyOnce.Do(func() {
		encryptionKey, keyErr = resolveEncryptionKey()
	})
	return encryptionKey, keyErr
}

// resolveEncryptionKey performs the actual key resolution (env → persisted
// file → generate-and-persist). Split out from the sync.Once wrapper so the
// lifecycle is unit-testable without process restarts.
func resolveEncryptionKey() ([]byte, error) {
	// 1) Explicit env key (highest priority; unchanged behaviour).
	if keyHex := os.Getenv(encryptionKeyEnv); keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key must be 32 bytes (64 hex chars), got %d", len(key))
		}
		return key, nil
	}

	keyPath := os.Getenv(encryptionKeyPathEnv)
	if keyPath == "" {
		keyPath = defaultEncryptionKeyPath
	}

	// 2) Load a previously persisted key.
	if data, err := os.ReadFile(keyPath); err == nil {
		if decoded, derr := hex.DecodeString(strings.TrimSpace(string(data))); derr == nil && len(decoded) == 32 {
			return decoded, nil
		}
		log.Printf("orvix: persisted encryption key at %s is invalid; generating a new one", keyPath)
	}

	// 3) Generate a new key and persist it for future restarts.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	if err := persistEncryptionKey(keyPath, key); err != nil {
		log.Printf("orvix: WARNING could not persist encryption key to %s: %v; "+
			"data encrypted now will NOT survive a restart. Set ORVIX_ENCRYPTION_KEY "+
			"or make the key path writable.", keyPath, err)
	}

	return key, nil
}

// persistEncryptionKey writes the AES key (hex-encoded) to path with 0600
// permissions, creating the parent directory (0700) if needed. Same on-disk
// contract as the JWT signing key.
func persistEncryptionKey(path string, key []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600)
}

// Encrypt encrypts plaintext using AES-256-GCM with the process encryption
// key. Returns hex(nonce) + ":" + hex(ciphertext).
func Encrypt(plaintext []byte) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}
	return encryptWithKey(key, plaintext)
}

// encryptWithKey is the key-parameterized core of Encrypt, split out so the
// key lifecycle can be tested independently of the sync.Once-cached key.
func encryptWithKey(key, plaintext []byte) (string, error) {
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
	return decryptWithKey(key, encoded)
}

// decryptWithKey is the key-parameterized core of Decrypt.
func decryptWithKey(key []byte, encoded string) ([]byte, error) {
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
