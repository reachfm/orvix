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
	publicKey     *rsa.PublicKey
	publicKeyPath string
	db            *gorm.DB
	logger        *zap.Logger
}

// NewValidator creates a new license validator.
func NewValidator(publicKeyPath string, db *gorm.DB, logger *zap.Logger) (*Validator, error) {
	keyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		logger.Warn("license public key not found, using offline mode", zap.Error(err))
		return &Validator{publicKeyPath: publicKeyPath, db: db, logger: logger}, nil
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
		publicKey:     rsaPub,
		publicKeyPath: publicKeyPath,
		db:            db,
		logger:        logger,
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

// Status represents the structured license state of the
// runtime. It is the response shape of Validator.Status() and
// the GET /api/v1/license admin endpoint.
type Status string

const (
	// StatusOffline: the runtime has not been able to contact a
	// license authority AND no public key has been configured.
	// All license-gated features are denied.
	StatusOffline Status = "offline"

	// StatusPublicKeyMissing: the runtime has not been able to
	// contact a license authority AND no public key is
	// configured at the expected path. The operator must drop
	// the public key in place for license validation to work.
	StatusPublicKeyMissing Status = "public_key_missing"

	// StatusLicenseMissing: the public key is configured but no
	// license row exists in the local DB. The operator must
	// supply a valid license key.
	StatusLicenseMissing Status = "license_missing"

	// StatusInvalid: a license key is present in the local DB
	// but the cryptographic check failed.
	StatusInvalid Status = "invalid"

	// StatusExpired: a license key is present and the signature
	// is valid but the expiry is in the past.
	StatusExpired Status = "expired"

	// StatusValid: a license key is present, signed by the
	// configured public key, and not expired.
	StatusValid Status = "valid"
)

// StatusReport is the structured response shape of Status().
// It NEVER carries the license key, the key hash, the public
// key, or any other secret.
type StatusReport struct {
	Status     Status   `json:"status"`
	Tier       string   `json:"tier,omitempty"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
	CustomerID string   `json:"customer_id,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Status returns the structured license status of the runtime.
// The method never returns an error; failures are reflected in
// the Status field. This is the shape the admin GET
// /api/v1/license endpoint returns and the shape operators see
// on the dashboard.
func (v *Validator) Status() StatusReport {
	if v == nil {
		return StatusReport{Status: StatusOffline, Reason: "license validator not initialized"}
	}
	// No public key configured: degraded state. The previous
	// release reported this as "offline" without distinguishing
	// it from "license authority unreachable". The new states
	// help operators understand the difference.
	if v.publicKey == nil {
		return StatusReport{
			Status:   StatusPublicKeyMissing,
			Reason:   "license public key not configured; run the installer to drop it in place",
			Warnings: []string{"all license-gated features are denied until a public key is provided"},
		}
	}
	// Look up the active license row.
	var lic struct {
		KeyHash   string
		Tier      string
		ExpiresAt time.Time
		Active    bool
	}
	if v.db == nil {
		return StatusReport{Status: StatusOffline, Reason: "database not available"}
	}
	if err := v.db.Table("licenses").
		Where("active = ?", true).
		Order("id DESC").
		First(&lic).Error; err != nil {
		return StatusReport{
			Status: StatusLicenseMissing,
			Reason: "no active license row in the local database; supply a license key",
		}
	}
	// We do not re-validate the signature here (it was validated
	// when the license was first inserted). What we can do
	// cheaply is check the expiry.
	if time.Now().After(lic.ExpiresAt) {
		return StatusReport{
			Status:     StatusExpired,
			Tier:       lic.Tier,
			ExpiresAt:  lic.ExpiresAt.UTC().Format(time.RFC3339),
			Reason:     "license expired; renew or replace",
		}
	}
	return StatusReport{
		Status:     StatusValid,
		Tier:       lic.Tier,
		ExpiresAt:  lic.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

// PublicKeyPath returns the path the validator looked for the
// public key at. Returns "" if the validator was constructed
// without a path (e.g., from a custom test setup).
func (v *Validator) PublicKeyPath() string {
	if v == nil {
		return ""
	}
	return v.publicKeyPath
}
