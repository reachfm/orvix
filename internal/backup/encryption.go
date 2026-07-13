package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// BackupEncryptionKey is the 32-byte key derived from a user-supplied password.
// Must be set before calling EncryptBackup or DecryptBackup.
var BackupEncryptionKey []byte

// DeriveBackupKey derives a 32-byte AES-256 key from a password using HKDF-SHA256.
func DeriveBackupKey(password string, salt []byte) []byte {
	hkdf := hkdf.New(sha256.New, []byte(password), salt, []byte("orvix-backup-v1"))
	key := make([]byte, 32)
	io.ReadFull(hkdf, key)
	return key
}

// EncryptBackup encrypts plaintext with AES-256-GCM using BackupEncryptionKey.
func EncryptBackup(plaintext []byte) ([]byte, error) {
	if len(BackupEncryptionKey) != 32 {
		return nil, fmt.Errorf("backup encryption key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(BackupEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes-gcm: %w", err)
	}
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext := aesgcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptBackup decrypts AES-256-GCM ciphertext (nonce || ciphertext).
func DecryptBackup(ciphertext []byte) ([]byte, error) {
	if len(BackupEncryptionKey) != 32 {
		return nil, fmt.Errorf("backup encryption key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(BackupEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes-gcm: %w", err)
	}
	if len(ciphertext) < aesgcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:aesgcm.NonceSize()], ciphertext[aesgcm.NonceSize():]
	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// ComputeBackupChecksum returns hex-encoded SHA-256 of data.
func ComputeBackupChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
