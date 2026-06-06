package compliance

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// ZeroKnowledgeEncryption provides client-side encryption where the server
// never has access to the plaintext key. Keys are derived from user passwords.
type ZeroKnowledgeEncryption struct {
	db *gorm.DB
}

// EncryptedBlob represents an encrypted email or attachment stored on the server.
type EncryptedBlob struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	BlobType  string    `gorm:"not null" json:"blob_type"`
	Salt      string    `gorm:"not null" json:"salt"`
	Nonce     string    `gorm:"not null" json:"nonce"`
	Ciphertext string   `gorm:"type:text;not null" json:"ciphertext"`
	CreatedAt time.Time `json:"created_at"`
}

// NewZeroKnowledgeEncryption creates a new ZKE service.
func NewZeroKnowledgeEncryption(db *gorm.DB) *ZeroKnowledgeEncryption {
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	return &ZeroKnowledgeEncryption{db: db}
}

// DeriveKey derives a 256-bit encryption key from a user password using Argon2id.
// The salt must be stored alongside the encrypted data and sent to the client
// during decryption. Returns hex-encoded salt and hex-encoded key.
func (zke *ZeroKnowledgeEncryption) DeriveKey(password string) (saltHex string, keyHex string, err error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", "", fmt.Errorf("failed to generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return hex.EncodeToString(salt), hex.EncodeToString(key), nil
}

// DeriveKeyFromSalt derives a key from a password using a known salt (for decryption).
func (zke *ZeroKnowledgeEncryption) DeriveKeyFromSalt(password, saltHex string) (string, error) {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return "", fmt.Errorf("invalid salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return hex.EncodeToString(key), nil
}

// Encrypt encrypts plaintext using AES-256-GCM with the derived key.
// Returns the nonce and ciphertext as hex strings.
func (zke *ZeroKnowledgeEncryption) Encrypt(plaintext []byte, keyHex string) (nonceHex string, ciphertextHex string, err error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", "", fmt.Errorf("invalid key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return hex.EncodeToString(nonce), hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext using AES-256-GCM with the derived key.
func (zke *ZeroKnowledgeEncryption) Decrypt(nonceHex, ciphertextHex, keyHex string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce: %w", err)
	}

	ciphertext, err := hex.DecodeString(ciphertextHex)
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
		return nil, errors.New("decryption failed - wrong key or corrupted data")
	}

	return plaintext, nil
}

// StoreEncrypted saves an encrypted blob to the database.
func (zke *ZeroKnowledgeEncryption) StoreEncrypted(userID uint, blobType, salt, nonce, ciphertext string) (*EncryptedBlob, error) {
	blob := &EncryptedBlob{
		UserID:     userID,
		BlobType:   blobType,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}

	if err := zke.db.Create(blob).Error; err != nil {
		return nil, fmt.Errorf("failed to store encrypted blob: %w", err)
	}

	return blob, nil
}

// GetEncrypted retrieves an encrypted blob by ID.
func (zke *ZeroKnowledgeEncryption) GetEncrypted(id, userID uint) (*EncryptedBlob, error) {
	var blob EncryptedBlob
	if err := zke.db.Where("id = ? AND user_id = ?", id, userID).First(&blob).Error; err != nil {
		return nil, fmt.Errorf("encrypted blob not found: %w", err)
	}
	return &blob, nil
}

// EncryptEmailPayload encrypts a complete email payload as JSON.
func (zke *ZeroKnowledgeEncryption) EncryptEmailPayload(payload interface{}, password string) (map[string]string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	saltHex, keyHex, err := zke.DeriveKey(password)
	if err != nil {
		return nil, err
	}

	nonceHex, ciphertextHex, err := zke.Encrypt(data, keyHex)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"salt":       saltHex,
		"nonce":      nonceHex,
		"ciphertext": ciphertextHex,
	}, nil
}

// DecryptEmailPayload decrypts an email payload using the user's password.
func (zke *ZeroKnowledgeEncryption) DecryptEmailPayload(payload map[string]string, password string, result interface{}) error {
	keyHex, err := zke.DeriveKeyFromSalt(password, payload["salt"])
	if err != nil {
		return err
	}

	data, err := zke.Decrypt(payload["nonce"], payload["ciphertext"], keyHex)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("failed to unmarshal decrypted payload: %w", err)
	}

	return nil
}
