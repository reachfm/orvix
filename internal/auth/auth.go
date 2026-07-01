package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenInvalid       = errors.New("invalid token")
	ErrSessionExpired     = errors.New("session expired")
)

// Role represents a user role for RBAC.
type Role string

const (
	RoleSuperAdmin Role = "superadmin"
	RoleAdmin      Role = "admin"
	RoleUser       Role = "user"
)

// Authenticator handles JWT-based authentication with Argon2id password hashing.
type Authenticator struct {
	privateKey   *rsa.PrivateKey
	publicKey    *rsa.PublicKey
	db           *gorm.DB
	logger       *zap.Logger
	accessTTL    time.Duration
	refreshTTL   time.Duration
	passwordCost config.AuthConfig
}

// NewAuthenticator creates a new authentication system.
// It loads the RSA key pair from disk if it exists, otherwise generates and persists a new one.
func NewAuthenticator(cfg *config.AuthConfig, db *gorm.DB, logger *zap.Logger) (*Authenticator, error) {
	privateKey, err := loadOrGenerateKey(cfg.JWTKeyPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RSA key: %w", err)
	}

	return &Authenticator{
		privateKey:   privateKey,
		publicKey:    &privateKey.PublicKey,
		db:           db,
		logger:       logger,
		accessTTL:    cfg.JWTAccessTTL,
		refreshTTL:   cfg.JWTRefreshTTL,
		passwordCost: *cfg,
	}, nil
}

// loadOrGenerateKey loads an RSA private key from disk or generates and saves a new one.
func loadOrGenerateKey(keyPath string, logger *zap.Logger) (*rsa.PrivateKey, error) {
	if keyPath == "" {
		keyPath = "/var/lib/orvix/jwt_key.pem"
	}

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil && block.Type == "RSA PRIVATE KEY" {
			key, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes)
			if parseErr == nil {
				logger.Info("loaded persisted JWT signing key", zap.String("path", keyPath))
				return key, nil
			}
			logger.Warn("failed to parse persisted JWT key, generating new one", zap.Error(parseErr))
		}
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("failed to create key directory, key will not persist", zap.Error(err))
		return privateKey, nil
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})

	if err := os.WriteFile(keyPath, pemBlock, 0600); err != nil {
		logger.Warn("failed to persist JWT signing key, key will not survive restart", zap.Error(err))
	} else {
		logger.Info("persisted new JWT signing key", zap.String("path", keyPath))
	}

	return privateKey, nil
}

// GenerateAccessToken creates a short-lived JWT access token (RS256).
func (a *Authenticator) GenerateAccessToken(userID uint, role Role) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  fmt.Sprintf("%d", userID),
		"role": string(role),
		"iat":  now.Unix(),
		"exp":  now.Add(a.accessTTL).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign access token: %w", err)
	}

	return tokenString, nil
}

// GenerateRefreshToken creates a long-lived refresh token stored as HttpOnly cookie.
func (a *Authenticator) GenerateRefreshToken(userID uint) (string, time.Time, error) {
	expiresAt := time.Now().Add(a.refreshTTL)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	token := hex.EncodeToString(b)
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	session := struct {
		UserID    uint
		TokenHash string
		ExpiresAt time.Time
	}{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}

	if err := a.db.Table("sessions").Create(&session).Error; err != nil {
		return "", time.Time{}, fmt.Errorf("failed to store session: %w", err)
	}

	return token, expiresAt, nil
}

// ValidateAccessToken validates a JWT access token and returns user ID and role.
func (a *Authenticator) ValidateAccessToken(tokenString string) (uint, Role, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil {
		return 0, "", ErrTokenInvalid
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return 0, "", ErrTokenInvalid
	}

	exp, ok := claims["exp"].(float64)
	if ok && time.Now().Unix() > int64(exp) {
		return 0, "", ErrTokenExpired
	}

	var userID uint
	fmt.Sscanf(claims["sub"].(string), "%d", &userID)
	role, _ := claims["role"].(string)

	return userID, Role(role), nil
}

// RefreshToken validates a refresh token, rotates it, and returns new tokens.
func (a *Authenticator) RefreshToken(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(refreshToken)))

	var session struct {
		UserID    uint
		ExpiresAt time.Time
	}

	if err := a.db.Table("sessions").
		Where("token_hash = ? AND expires_at > ?", tokenHash, time.Now()).
		First(&session).Error; err != nil {
		return "", "", time.Time{}, ErrSessionExpired
	}

	a.db.Table("sessions").Where("token_hash = ?", tokenHash).Delete(nil)

	accessToken, err := a.GenerateAccessToken(session.UserID, RoleUser)
	if err != nil {
		return "", "", time.Time{}, err
	}

	newRefresh, expiresAt, err := a.GenerateRefreshToken(session.UserID)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return accessToken, newRefresh, expiresAt, nil
}

// InvalidateAllSessions deletes all sessions for a user.
func (a *Authenticator) InvalidateAllSessions(userID uint) error {
	result := a.db.Table("sessions").Where("user_id = ?", userID).Delete(nil)
	if result.Error != nil {
		return fmt.Errorf("failed to invalidate sessions: %w", result.Error)
	}
	a.logger.Info("all sessions invalidated", zap.Uint("user_id", userID))
	return nil
}

// InvalidateOtherSessions deletes all sessions except the one with the given token hash.
func (a *Authenticator) InvalidateOtherSessions(userID uint, currentTokenHash string) error {
	result := a.db.Table("sessions").
		Where("user_id = ? AND token_hash != ?", userID, currentTokenHash).
		Delete(nil)
	if result.Error != nil {
		return fmt.Errorf("failed to invalidate other sessions: %w", result.Error)
	}
	a.logger.Info("other sessions invalidated", zap.Uint("user_id", userID))
	return nil
}

// HashPassword hashes a password using bcrypt.
func (a *Authenticator) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword verifies a password against bcrypt hash.
func (a *Authenticator) VerifyPassword(password, encoded string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password))
	return err == nil
}

func splitHash(encoded string) []string {
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == ':' {
			return []string{encoded[:i], encoded[i+1:]}
		}
	}
	return nil
}

// Middleware returns a Fiber middleware that validates JWT access tokens.
// If the request was already authenticated via API key (auth_method set), it skips JWT validation.
func (a *Authenticator) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Locals("auth_method") != nil {
			return c.Next()
		}

		token := c.Cookies("access_token")
		if token == "" {
			authHeader := c.Get("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			}
		}

		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "missing authentication token",
			})
		}

		userID, role, err := a.ValidateAccessToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or expired token",
			})
		}

		c.Locals("user_id", userID)
		c.Locals("role", role)

		return c.Next()
	}
}

// RequireRole returns a middleware that checks for a specific role.
func RequireRole(role Role) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRole, ok := c.Locals("role").(Role)
		if !ok || userRole != role {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "insufficient permissions",
			})
		}
		return c.Next()
	}
}

// RequireAnyRole returns a middleware that checks for any of the specified roles.
func RequireAnyRole(roles ...Role) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRole, ok := c.Locals("role").(Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "insufficient permissions",
			})
		}
		for _, r := range roles {
			if userRole == r {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "insufficient permissions",
		})
	}
}

// MFAChallengeTTL is the lifetime of an MFA challenge token.
const MFAChallengeTTL = 5 * time.Minute

// MFAChallengeClaim is the JWT claim name used to distinguish
// MFA challenge tokens from real access tokens. Access tokens
// carry "role"; challenge tokens carry "mfa_challenge" instead.
const MFAChallengeClaim = "mfa_challenge"

var mfaChallengeNow = time.Now

// SetMFAChallengeClockForTest overrides the MFA challenge clock and returns a
// restore function. It is intended for expiry tests only.
func SetMFAChallengeClockForTest(now func() time.Time) func() {
	prev := mfaChallengeNow
	mfaChallengeNow = now
	return func() { mfaChallengeNow = prev }
}

// GenerateMFAChallengeToken creates a short-lived token that proves
// the caller passed password authentication but has not yet completed
// MFA. The token MUST NOT be accepted by any protected endpoint.
// It is only usable with the MFA verify endpoint.
func (a *Authenticator) GenerateMFAChallengeToken(userID uint) (string, error) {
	now := mfaChallengeNow()
	claims := jwt.MapClaims{
		"sub":             fmt.Sprintf("%d", userID),
		MFAChallengeClaim: true,
		"iat":             now.Unix(),
		"exp":             now.Add(MFAChallengeTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign MFA challenge token: %w", err)
	}
	return tokenString, nil
}

// ValidateMFAChallengeToken validates an MFA challenge token and
// returns the user ID. Returns an error if the token is invalid,
// expired, or is not an MFA challenge token.
func (a *Authenticator) ValidateMFAChallengeToken(tokenString string) (uint, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil || !token.Valid {
		return 0, ErrTokenInvalid
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, ErrTokenInvalid
	}
	// Must carry the MFA challenge claim.
	if val, _ := claims[MFAChallengeClaim].(bool); !val {
		return 0, fmt.Errorf("not an MFA challenge token")
	}
	exp, ok := claims["exp"].(float64)
	if ok && mfaChallengeNow().Unix() > int64(exp) {
		return 0, ErrTokenExpired
	}
	var userID uint
	fmt.Sscanf(claims["sub"].(string), "%d", &userID)
	return userID, nil
}
