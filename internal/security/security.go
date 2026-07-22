package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/features"
	"github.com/orvixemail/orvix/internal/license"
	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

type AuditService struct {
	db *gorm.DB
}

func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{db: db}
}

func (s *AuditService) Log(userID, tenantID *uint, action, resource, resourceID, ip, details string) error {
	entry := &models.AuditLog{
		UserID:     userID,
		TenantID:   tenantID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		IP:         ip,
		Details:    details,
		CreatedAt:  time.Now(),
	}
	return s.db.Create(entry).Error
}

func (s *AuditService) Query(filters map[string]interface{}, limit, offset int) ([]models.AuditLog, error) {
	var logs []models.AuditLog
	query := s.db.Order("created_at DESC")
	for key, val := range filters {
		query = query.Where(key, val)
	}
	if err := query.Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

type APIKeyService struct {
	db *gorm.DB
}

func NewAPIKeyService(db *gorm.DB) *APIKeyService {
	return &APIKeyService{db: db}
}

func (s *APIKeyService) Generate(userID uint, name string, permissions []string) (string, error) {
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("failed to generate API key: %w", err)
	}

	apiKey := hex.EncodeToString(keyBytes)
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(apiKey)))

	permJSON := "[]"
	if len(permissions) > 0 {
		permJSON = fmt.Sprintf(`["%s"]`, permissions[0])
		for _, p := range permissions[1:] {
			permJSON = fmt.Sprintf(`%s,"%s"`, permJSON, p)
		}
		permJSON = `[` + permJSON + `]`
	}

	entry := &models.APIKey{
		UserID:      userID,
		KeyHash:     keyHash,
		Name:        name,
		Permissions: permJSON,
		Active:      true,
	}

	if err := s.db.Create(entry).Error; err != nil {
		return "", fmt.Errorf("failed to store API key: %w", err)
	}

	return apiKey, nil
}

func (s *APIKeyService) Validate(key string) (*models.APIKey, error) {
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	var entry models.APIKey
	if err := s.db.Where("key_hash = ? AND active = ?", keyHash, true).First(&entry).Error; err != nil {
		return nil, fmt.Errorf("invalid API key: %w", err)
	}

	return &entry, nil
}

func (s *APIKeyService) Revoke(id uint) error {
	return s.db.Model(&models.APIKey{}).Where("id = ?", id).Update("active", false).Error
}

func RateLimitMiddleware(limit int, window int) fiber.Handler {
	type visitor struct {
		count   int
		resetAt time.Time
	}

	var mu sync.Mutex
	visitors := make(map[string]*visitor)

	return func(c *fiber.Ctx) error {
		ip := c.IP()

		mu.Lock()
		v, exists := visitors[ip]
		if !exists || time.Now().After(v.resetAt) {
			visitors[ip] = &visitor{
				count:   1,
				resetAt: time.Now().Add(time.Duration(window) * time.Second),
			}
			mu.Unlock()
			return c.Next()
		}

		v.count++
		if v.count > limit {
			mu.Unlock()
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":       "rate limit exceeded",
				"retry_after": int(time.Until(v.resetAt).Seconds()),
			})
		}
		mu.Unlock()

		return c.Next()
	}
}

func SecureHeadersMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		return c.Next()
	}
}

func APIKeyAuthMiddleware(svc *APIKeyService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Get("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}
		if key == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "API key required",
			})
		}

		_, err := svc.Validate(key)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid API key",
			})
		}

		return c.Next()
	}
}

func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func CSRFMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Only apply CSRF to state-changing methods on non-API routes
		if strings.HasPrefix(c.Path(), "/api/") {
			return c.Next()
		}
		if c.Method() == "GET" || c.Method() == "HEAD" || c.Method() == "OPTIONS" {
			token, err := GenerateCSRFToken()
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate CSRF token"})
			}
			c.Cookie(&fiber.Cookie{
				Name:     "csrf_token",
				Value:    token,
				HTTPOnly: false,
				Secure:   true,
				SameSite: "Strict",
				MaxAge:   3600,
			})
			c.Set("X-CSRF-Token", token)
			return c.Next()
		}

		cookieToken := c.Cookies("csrf_token")
		headerToken := c.Get("X-CSRF-Token")

		if cookieToken == "" || headerToken == "" || cookieToken != headerToken {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "CSRF token mismatch"})
		}

		return c.Next()
	}
}

func LicenseGateMiddleware(licenseSvc *license.Service, featuresSvc *features.Manager) func(string) fiber.Handler {
	return func(featureKey string) fiber.Handler {
		return func(c *fiber.Ctx) error {
			if !featuresSvc.IsEnabled(featureKey, nil) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": fmt.Sprintf("feature '%s' is not enabled on this license tier", featureKey),
				})
			}
			return c.Next()
		}
	}
}

func AuthRequiredMiddleware(authSvc *auth.Service) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Get("Authorization")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "authorization header required",
			})
		}

		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		mapClaims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or expired token",
			})
		}

		userIDStr, _ := mapClaims["sub"].(string)
		userID, _ := strconv.ParseUint(userIDStr, 10, 64)
		email, _ := mapClaims["email"].(string)
		role, _ := mapClaims["role"].(string)
		tenantIDFloat, _ := mapClaims["tid"].(float64)

		c.Locals("user_id", uint(userID))
		c.Locals("email", email)
		c.Locals("role", role)
		c.Locals("tenant_id", uint(tenantIDFloat))

		return c.Next()
	}
}
