package license

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	ErrLicenseExpired = errors.New("license has expired")
	ErrInvalidLicense = errors.New("invalid license key")
	ErrHardwareMismatch = errors.New("hardware fingerprint mismatch")
)

// Claims represents the JWT claims in a license key.
type Claims struct {
	jwt.RegisteredClaims
	Tier         string `json:"tier"`
	MaxDomains   int    `json:"max_domains"`
	MaxMailboxes int    `json:"max_mailboxes"`
	HardwareID   string `json:"hardware_id"`
	CustomerID   string `json:"customer_id"`
}

// Validator verifies license keys.
type Validator struct {
	publicKey *rsa.PublicKey
	db        *gorm.DB
	logger    *zap.Logger
}

// NewValidator creates a new license validator.
func NewValidator(publicKeyPath string, db *gorm.DB, logger *zap.Logger) (*Validator, error) {
	keyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		logger.Warn("license public key not found, using offline mode", zap.Error(err))
		return &Validator{db: db, logger: logger}, nil
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, errors.New("failed to decode public key PEM")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}

	return &Validator{
		publicKey: rsaPub,
		db:        db,
		logger:    logger,
	}, nil
}

// Validate checks if a license key is valid and returns its claims.
func (v *Validator) Validate(tokenString string) (*Claims, Tier, error) {
	if v.publicKey == nil {
		return v.offlineValidate(tokenString)
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return v.publicKey, nil
	})
	if err != nil {
		return nil, "", ErrInvalidLicense
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, "", ErrInvalidLicense
	}

	if time.Now().After(claims.ExpiresAt.Time) {
		return nil, "", ErrLicenseExpired
	}

	tier := Tier(claims.Tier)
	switch tier {
	case TierSMB, TierISP, TierEnterprise:
	default:
		tier = TierSMB
	}

	v.logger.Info("license validated",
		zap.String("tier", string(tier)),
		zap.String("customer", claims.CustomerID),
	)

	return claims, tier, nil
}

func (v *Validator) offlineValidate(tokenString string) (*Claims, Tier, error) {
	v.logger.Warn("offline license validation - no public key configured")
	tier := TierSMB

	var lic struct {
		KeyHash string
		Tier    string
		ExpiresAt time.Time
		Active  bool
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenString)))

	if err := v.db.Model(&struct {
		KeyHash   string
		Tier      string
		ExpiresAt time.Time
		Active    bool
	}{}).Table("licenses").Where("key_hash = ? AND active = ?", hash, true).First(&lic).Error; err != nil {
		return nil, "", ErrInvalidLicense
	}

	if time.Now().After(lic.ExpiresAt) {
		return nil, "", ErrLicenseExpired
	}

	tier = Tier(lic.Tier)
	switch tier {
	case TierSMB, TierISP, TierEnterprise:
	default:
		tier = TierSMB
	}

	return &Claims{
		Tier: string(tier),
	}, tier, nil
}
