package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return logger
}

func TestHashAndVerifyPassword(t *testing.T) {
	logger := testLogger(t)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
		passwordCost: config.AuthConfig{
			Argon2Time:     1,
			Argon2Memory:   1024,
			Argon2Threads:  1,
			PasswordMinLen: 6,
		},
	}

	hash, err := a.HashPassword("test-password-123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !a.VerifyPassword("test-password-123", hash) {
		t.Fatal("VerifyPassword should return true for correct password")
	}

	if a.VerifyPassword("wrong-password", hash) {
		t.Fatal("VerifyPassword should return false for wrong password")
	}
}

func TestSpecialCharacterPasswords(t *testing.T) {
	logger := testLogger(t)
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
		passwordCost: config.AuthConfig{
			Argon2Time:     1,
			Argon2Memory:   1024,
			Argon2Threads:  1,
			PasswordMinLen: 6,
		},
	}

	passwords := []string{
		"MaghaghaMos086",
		"Password123!",
		"Password$123",
		"Password With Spaces",
		"Password\\Slash123",
		"Password\"Quote123",
		"Password'SingleQuote123",
	}

	for _, pw := range passwords {
		hash, err := a.HashPassword(pw)
		if err != nil {
			t.Fatalf("HashPassword(%q) failed: %v", pw, err)
		}
		if !a.VerifyPassword(pw, hash) {
			t.Errorf("VerifyPassword(%q, hash) should return true", pw)
		}
		if a.VerifyPassword("wrong", hash) {
			t.Errorf("VerifyPassword(wrong, hash_for_%q) should return false", pw)
		}
	}
}

func TestGenerateAndValidateAccessToken(t *testing.T) {
	logger := testLogger(t)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}

	token, err := a.GenerateAccessToken(42, RoleUser)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	uid, role, err := a.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken failed: %v", err)
	}
	if uid != 42 {
		t.Fatalf("expected uid=42, got %d", uid)
	}
	if role != RoleUser {
		t.Fatalf("expected role=user, got %s", role)
	}
}

func TestTokenExpiry(t *testing.T) {
	logger := testLogger(t)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  1 * time.Nanosecond,
		refreshTTL: 30 * 24 * time.Hour,
	}

	token, err := a.GenerateAccessToken(1, RoleUser)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	_, _, err = a.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("expected token to be expired")
	}
}

func TestLoadOrGenerateKey_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key.pem")
	logger := testLogger(t)

	key1, err := loadOrGenerateKey(keyPath, logger)
	if err != nil {
		t.Fatalf("first loadOrGenerateKey failed: %v", err)
	}

	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Fatal("key file should have been created")
	}

	data, _ := os.ReadFile(keyPath)
	if len(data) == 0 {
		t.Fatal("key file should have content")
	}

	key2, err := loadOrGenerateKey(keyPath, logger)
	if err != nil {
		t.Fatalf("second loadOrGenerateKey failed: %v", err)
	}

	t.Logf("key1.D = %v", key1.D)
	t.Logf("key2.D = %v", key2.D)
	if key1.D.Cmp(key2.D) != 0 {
		t.Fatal("loaded key should match saved key")
	}
}

func TestGenerateDifferentTokensForSameUser(t *testing.T) {
	logger := testLogger(t)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}

	token1, _ := a.GenerateAccessToken(1, RoleUser)
	time.Sleep(2 * time.Second)
	token2, _ := a.GenerateAccessToken(1, RoleUser)

	if token1 == token2 {
		t.Fatal("two access tokens generated at different times should differ")
	}
}

func TestInvalidToken(t *testing.T) {
	logger := testLogger(t)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}

	_, _, err := a.ValidateAccessToken("invalid-token-string")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestEmptyPassword(t *testing.T) {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	logger := testLogger(t)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		passwordCost: config.AuthConfig{
			Argon2Time:     1,
			Argon2Memory:   1024,
			Argon2Threads:  1,
			PasswordMinLen: 6,
		},
		logger: logger,
	}

	hash, err := a.HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword with empty string failed: %v", err)
	}

	if !a.VerifyPassword("", hash) {
		t.Fatal("VerifyPassword should work with empty string")
	}
}

func TestGenerateAccessTokenWithDifferentRoles(t *testing.T) {
	logger := testLogger(t)
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}

	roles := []Role{RoleUser, RoleAdmin, RoleSuperAdmin}
	for _, role := range roles {
		token, err := a.GenerateAccessToken(1, role)
		if err != nil {
			t.Fatalf("GenerateAccessToken with role %s failed: %v", role, err)
		}
		_, gotRole, err := a.ValidateAccessToken(token)
		if err != nil {
			t.Fatalf("ValidateAccessToken with role %s failed: %v", role, err)
		}
		if gotRole != role {
			t.Fatalf("expected role=%s, got %s", role, gotRole)
		}
	}
}

func TestLoadOrGenerateKey_MissingDirectoryCreated(t *testing.T) {
	tmpDir := t.TempDir()
	deepPath := filepath.Join(tmpDir, "deeply", "nested", "jwt_key.pem")
	logger := testLogger(t)

	key, err := loadOrGenerateKey(deepPath, logger)
	if err != nil {
		t.Fatalf("loadOrGenerateKey with nested path failed: %v", err)
	}
	if key == nil {
		t.Fatal("expected valid key")
	}

	if _, err := os.Stat(deepPath); os.IsNotExist(err) {
		t.Fatal("key file should have been created in nested directory")
	}
}

func TestLoadOrGenerateKey_EmptyPath(t *testing.T) {
	tmpDir := t.TempDir()
	logger := testLogger(t)

	origKeyDir := os.Getenv("JWT_KEY_DIR")
	defer os.Setenv("JWT_KEY_DIR", origKeyDir)
	os.Setenv("JWT_KEY_DIR", tmpDir)

	key, err := loadOrGenerateKey(filepath.Join(tmpDir, "default_key.pem"), logger)
	if err != nil {
		t.Fatalf("loadOrGenerateKey failed: %v", err)
	}
	if key == nil {
		t.Fatal("expected valid key")
	}
}

func TestSplitHash(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"abc:def", []string{"abc", "def"}},
		{"a:b:c", []string{"a", "b:c"}},
		{"", nil},
		{"no-separator", nil},
	}

	for _, tt := range tests {
		result := splitHash(tt.input)
		if tt.expected == nil {
			if result != nil {
				t.Errorf("splitHash(%q) = %v, want nil", tt.input, result)
			}
			continue
		}
		if result == nil {
			t.Errorf("splitHash(%q) = nil, want %v", tt.input, tt.expected)
			continue
		}
		if result[0] != tt.expected[0] || result[1] != tt.expected[1] {
			t.Errorf("splitHash(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}
