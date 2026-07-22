package license

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

type Tier int

const (
	TierUnknown    Tier = 0
	TierSMB        Tier = 1
	TierISP        Tier = 2
	TierEnterprise Tier = 3
)

func (t Tier) String() string {
	switch t {
	case TierSMB:
		return "smb"
	case TierISP:
		return "isp"
	case TierEnterprise:
		return "enterprise"
	default:
		return "unknown"
	}
}

func ParseTier(s string) Tier {
	switch s {
	case "smb":
		return TierSMB
	case "isp":
		return TierISP
	case "enterprise":
		return TierEnterprise
	default:
		return TierUnknown
	}
}

type Entitlement struct {
	Feature string `json:"feature"`
	Enabled bool   `json:"enabled"`
}

type LicenseClaims struct {
	jwt.RegisteredClaims
	Tier         string `json:"tier"`
	MaxDomains   int    `json:"max_domains"`
	MaxMailboxes int    `json:"max_mailboxes"`
	HardwareID   string `json:"hardware_id,omitempty"`
	Features     string `json:"features,omitempty"`
}

type Service struct {
	db     *gorm.DB
	cfg    config.LicenseConfig
	pubKey *rsa.PublicKey
}

func NewService(db *gorm.DB, cfg config.LicenseConfig) *Service {
	s := &Service{db: db, cfg: cfg}
	s.loadPublicKey()
	s.validateStoredLicense()
	return s
}

func (s *Service) loadPublicKey() {
	if s.cfg.EmbeddedPublicKey != "" {
		block, _ := pem.Decode([]byte(s.cfg.EmbeddedPublicKey))
		if block != nil {
			key, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err == nil {
				if pk, ok := key.(*rsa.PublicKey); ok {
					s.pubKey = pk
					return
				}
			}
		}
	}

	if s.cfg.PublicKeyPath != "" {
		data, err := os.ReadFile(s.cfg.PublicKeyPath)
		if err == nil {
			block, _ := pem.Decode(data)
			if block != nil {
				key, err := x509.ParsePKIXPublicKey(block.Bytes)
				if err == nil {
					if pk, ok := key.(*rsa.PublicKey); ok {
						s.pubKey = pk
					}
				}
			}
		}
	}
}

func (s *Service) validateStoredLicense() {
	var licenses []models.License
	s.db.Where("active = ?", true).Find(&licenses)
	for _, lic := range licenses {
		// Check expiry
		if lic.ExpiresAt.Before(time.Now()) {
			// Check offline grace - if grace has expired, deactivate
			if !lic.OfflineUntil.After(time.Now()) {
				s.db.Model(&lic).Update("active", false)
			}
		}
	}
}

func (s *Service) ValidateLicenseKey(tokenString string) (*LicenseClaims, error) {
	if s.pubKey == nil {
		return nil, fmt.Errorf("no public key loaded")
	}

	token, err := jwt.ParseWithClaims(tokenString, &LicenseClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.pubKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid license token: %w", err)
	}

	claims, ok := token.Claims.(*LicenseClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid license claims")
	}

	exp := claims.ExpiresAt
	if exp != nil && exp.Time.Before(time.Now()) {
		return nil, fmt.Errorf("license expired at %s", exp.Time.Format(time.RFC3339))
	}

	return claims, nil
}

func (s *Service) ValidateOffline(license *models.License) error {
	if license.OfflineUntil.After(time.Now()) {
		return nil
	}

	if time.Since(license.LastValidated) > time.Duration(s.cfg.OfflineGraceDays)*24*time.Hour {
		return fmt.Errorf("offline grace period expired, please reconnect to license server")
	}

	license.OfflineUntil = time.Now().Add(time.Duration(s.cfg.OfflineGraceDays) * 24 * time.Hour)
	s.db.Save(license)

	return nil
}

func (s *Service) GetActiveLicense() (*models.License, error) {
	var license models.License
	if err := s.db.Where("active = ?", true).First(&license).Error; err != nil {
		return nil, fmt.Errorf("no active license found: %w", err)
	}

	if license.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("license expired on %s", license.ExpiresAt.Format(time.RFC3339))
	}

	return &license, nil
}

func (s *Service) GetTier() Tier {
	license, err := s.GetActiveLicense()
	if err != nil {
		return TierUnknown
	}
	return ParseTier(license.Tier)
}

func (s *Service) GetMaxDomains() int {
	license, err := s.GetActiveLicense()
	if err != nil {
		return 0
	}
	return license.MaxDomains
}

func (s *Service) GetMaxMailboxes() int {
	license, err := s.GetActiveLicense()
	if err != nil {
		return 0
	}
	return license.MaxMailboxes
}

func (s *Service) ActivateLicense(keyHash, tier string, maxDomains, maxMailboxes int, expiresAt time.Time) (*models.License, error) {
	s.db.Where("active = ?", true).Update("active", false)

	license := &models.License{
		KeyHash:       keyHash,
		Tier:          tier,
		IssuedAt:      time.Now(),
		ExpiresAt:     expiresAt,
		MaxDomains:    maxDomains,
		MaxMailboxes:  maxMailboxes,
		Active:        true,
		LastValidated: time.Now(),
		OfflineUntil:  time.Now().Add(time.Duration(s.cfg.OfflineGraceDays) * 24 * time.Hour),
	}

	if err := s.db.Create(license).Error; err != nil {
		return nil, fmt.Errorf("failed to activate license: %w", err)
	}

	return license, nil
}
