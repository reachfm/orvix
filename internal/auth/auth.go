package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"github.com/pquerna/otp/totp"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

type Service struct {
	db     *gorm.DB
	cfg    config.SecurityConfig
	logger *zap.SugaredLogger
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type AuthResponse struct {
	TokenPair
	UserID       uint   `json:"user_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	TOTPRequired bool   `json:"totp_required"`
}

func NewService(db *gorm.DB, cfg config.SecurityConfig, logger *zap.SugaredLogger) *Service {
	return &Service{db: db, cfg: cfg, logger: logger}
}

func (s *Service) HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, s.cfg.Argon2Time, s.cfg.Argon2Memory, s.cfg.Argon2Threads, 32)

	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		s.cfg.Argon2Memory, s.cfg.Argon2Time, s.cfg.Argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash))

	return encoded, nil
}

func (s *Service) VerifyPassword(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}

	var argon2Memory, argon2Time uint32
	var argon2Threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &argon2Memory, &argon2Time, &argon2Threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	computedHash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, 32)

	return subtle.ConstantTimeCompare(expectedHash, computedHash) == 1
}

func (s *Service) GenerateTokens(user *models.User) (*TokenPair, error) {
	accessClaims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", user.ID),
		"email": user.Email,
		"role":  user.Role,
		"tid":   user.TenantID,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Duration(s.cfg.AccessTokenTTL) * time.Minute).Unix(),
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshBytes := make([]byte, 32)
	if _, err := rand.Read(refreshBytes); err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	refreshToken := base64.RawURLEncoding.EncodeToString(refreshBytes)

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshToken,
		ExpiresIn:    s.cfg.AccessTokenTTL * 60,
	}, nil
}

func (s *Service) ValidateAccessToken(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

func (s *Service) CreateSession(userID uint, ip, userAgent string) (*models.Session, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}
	tokenHash := base64.RawURLEncoding.EncodeToString(tokenBytes)

	refreshBytes := make([]byte, 32)
	if _, err := rand.Read(refreshBytes); err != nil {
		return nil, fmt.Errorf("failed to generate refresh hash: %w", err)
	}
	refreshHash := base64.RawURLEncoding.EncodeToString(refreshBytes)

	session := &models.Session{
		UserID:      userID,
		TokenHash:   tokenHash,
		RefreshHash: refreshHash,
		IP:          ip,
		UserAgent:   userAgent,
		ExpiresAt:   time.Now().Add(time.Duration(s.cfg.RefreshTokenTTL) * time.Minute),
		LastSeenAt:  time.Now(),
	}

	if err := s.db.Create(session).Error; err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

func (s *Service) LogSession(userID, sessionID uint) error {
	result := s.db.Where("id = ? AND user_id = ?", sessionID, userID).Delete(&models.Session{})
	return result.Error
}

func (s *Service) GetActiveSessions(userID uint) ([]models.Session, error) {
	var sessions []models.Session
	if err := s.db.Where("user_id = ? AND expires_at > ?", userID, time.Now()).Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *Service) GenerateTOTPSecret(email string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "OrvixEM",
		AccountName: email,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to generate TOTP secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

func (s *Service) ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

func (s *Service) EnableTOTP(userID uint, secret, code string) error {
	if !s.ValidateTOTP(secret, code) {
		return fmt.Errorf("invalid TOTP code")
	}
	return s.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_secret":  secret,
		"totp_enabled": true,
	}).Error
}

func (s *Service) DisableTOTP(userID uint, code string) error {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return err
	}
	if !s.ValidateTOTP(user.TOTPSecret, code) {
		return fmt.Errorf("invalid TOTP code")
	}
	return s.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_secret":  "",
		"totp_enabled": false,
	}).Error
}
