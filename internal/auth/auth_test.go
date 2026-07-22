package auth

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Session{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestHashAndVerifyPassword(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(db, config.SecurityConfig{
		Argon2Time:    1,
		Argon2Memory:  65536,
		Argon2Threads: 2,
		JWTSecret:     "test-secret",
	}, sugar)

	password := "TestPassword123!"
	hash, err := svc.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}

	if !svc.VerifyPassword(password, hash) {
		t.Error("VerifyPassword returned false for correct password")
	}

	if svc.VerifyPassword("wrong-password", hash) {
		t.Error("VerifyPassword returned true for wrong password")
	}

	if svc.VerifyPassword(password, "invalid-hash-format") {
		t.Error("VerifyPassword returned true for invalid hash")
	}
}

func TestGenerateTokens(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(db, config.SecurityConfig{
		JWTSecret:       "test-secret",
		AccessTokenTTL:  15,
		RefreshTokenTTL: 43200,
	}, sugar)

	user := &models.User{
		ID:      1,
		Email:   "test@orvix.email",
		Role:    "admin",
		IsAdmin: true,
	}

	pair, err := svc.GenerateTokens(user)
	if err != nil {
		t.Fatalf("GenerateTokens failed: %v", err)
	}
	if pair.AccessToken == "" {
		t.Error("AccessToken is empty")
	}
	if pair.RefreshToken == "" {
		t.Error("RefreshToken is empty")
	}
	if pair.ExpiresIn <= 0 {
		t.Error("ExpiresIn should be positive")
	}
}

func TestValidateAccessToken(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(db, config.SecurityConfig{
		JWTSecret:      "test-secret",
		AccessTokenTTL: 15,
	}, sugar)

	user := &models.User{
		ID:      1,
		Email:   "test@orvix.email",
		Role:    "admin",
		IsAdmin: true,
	}

	pair, err := svc.GenerateTokens(user)
	if err != nil {
		t.Fatalf("GenerateTokens failed: %v", err)
	}

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken failed: %v", err)
	}

	if claims["sub"] != "1" {
		t.Errorf("expected sub=1, got %v", claims["sub"])
	}
	if claims["email"] != "test@orvix.email" {
		t.Errorf("expected email=test@orvix.email, got %v", claims["email"])
	}

	_, err = svc.ValidateAccessToken("invalid-token")
	if err == nil {
		t.Error("ValidateAccessToken should fail for invalid token")
	}
}

func TestCreateAndLogSession(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(db, config.SecurityConfig{
		JWTSecret:       "test-secret",
		RefreshTokenTTL: 43200,
	}, sugar)

	session, err := svc.CreateSession(1, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if session.UserID != 1 {
		t.Errorf("expected UserID=1, got %d", session.UserID)
	}
	if session.IP != "127.0.0.1" {
		t.Errorf("expected IP=127.0.0.1, got %s", session.IP)
	}

	sessions, err := svc.GetActiveSessions(1)
	if err != nil {
		t.Fatalf("GetActiveSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}

	err = svc.LogSession(1, session.ID)
	if err != nil {
		t.Fatalf("LogSession failed: %v", err)
	}
}

func TestTOTP(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(db, config.SecurityConfig{}, sugar)

	secret, url, err := svc.GenerateTOTPSecret("test@orvix.email")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret failed: %v", err)
	}
	if secret == "" {
		t.Error("secret is empty")
	}
	if url == "" {
		t.Error("url is empty")
	}

	code := "123456"
	valid := svc.ValidateTOTP(secret, code)
	if valid {
		t.Error("ValidateTOTP should return false for invalid code")
	}
}
