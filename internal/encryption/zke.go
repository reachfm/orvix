package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

var encryptionKey []byte

func Init(key string) error {
	if key == "" {
		encryptionKey = nil
		return nil
	}
	hash := sha256.New()
	hash.Write([]byte(key))
	encryptionKey = hash.Sum(nil)
	return nil
}

func IsEnabled() bool {
	return encryptionKey != nil
}

func EncryptString(plaintext string) (string, error) {
	if !IsEnabled() {
		return plaintext, nil
	}
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func DecryptString(ciphertext string) (string, error) {
	if !IsEnabled() {
		return ciphertext, nil
	}
	if ciphertext == "" {
		return "", nil
	}
	encrypted, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}
	nonceSize := aesGCM.NonceSize()
	if len(encrypted) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertextBytes := encrypted[:nonceSize], encrypted[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}
	return string(plaintext), nil
}

// EncryptFile encrypts a file in-place by reading, encrypting, and rewriting.
// The first 8 bytes store the nonce, followed by the ciphertext.
// If encryption is not enabled, the file is left unchanged.
func EncryptFile(path string) error {
	if !IsEnabled() {
		return nil
	}
	plaintext, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file for encryption: %w", err)
	}
	if len(plaintext) == 0 {
		return nil
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := aesGCM.Seal(nil, nonce, plaintext, nil)
	// Format: [4-byte nonce length][nonce][ciphertext]
	nonceLen := uint32(len(nonce))
	output := make([]byte, 4+len(nonce)+len(ciphertext))
	output[0] = byte(nonceLen >> 24)
	output[1] = byte(nonceLen >> 16)
	output[2] = byte(nonceLen >> 8)
	output[3] = byte(nonceLen)
	copy(output[4:], nonce)
	copy(output[4+len(nonce):], ciphertext)
	return os.WriteFile(path, output, 0600)
}

// DecryptFile decrypts a file encrypted by EncryptFile.
// If encryption is not enabled, the file is left unchanged.
func DecryptFile(path string) error {
	if !IsEnabled() {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read encrypted file: %w", err)
	}
	if len(data) < 4 {
		return fmt.Errorf("file too short to be encrypted")
	}
	nonceLen := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if len(data) < 4+int(nonceLen)+16 {
		return fmt.Errorf("file too short or corrupted")
	}
	nonce := data[4 : 4+nonceLen]
	ciphertext := data[4+nonceLen:]
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("failed to decrypt file (wrong key?): %w", err)
	}
	return os.WriteFile(path, plaintext, 0600)
}
