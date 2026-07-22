package adapters

import (
	"fmt"

	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CoreMailEngineAdapter defines the interface between the OrvixEM Admin Control Plane
// and the underlying mail engine. Implementations handle all communication with the
// mail engine — whether via internal database, gRPC, REST API, or direct library calls.
//
// This decouples the admin API from any specific mail engine implementation.
type CoreMailEngineAdapter interface {
	// ProvisionMailbox creates a new mailbox (email account) on the mail engine.
	ProvisionMailbox(email string, password string, quota int64) error

	// DeleteMailbox removes a mailbox from the mail engine permanently.
	DeleteMailbox(email string) error

	// AddDomain registers a new domain on the mail engine so it can receive email.
	AddDomain(domain string) error

	// RemoveDomain unregisters a domain from the mail engine.
	RemoveDomain(domain string) error
}

// LocalCoreMailAdapter implements CoreMailEngineAdapter by storing mailbox and domain
// records directly in the shared GORM database. This is the default "local mode"
// adapter — it can be replaced with a gRPC or REST adapter for a standalone mail engine.
type LocalCoreMailAdapter struct {
	db     *gorm.DB
	authSvc *auth.Service
	logger  *zap.SugaredLogger
}

// NewLocalCoreMailAdapter creates a new LocalCoreMailAdapter.
func NewLocalCoreMailAdapter(db *gorm.DB, authSvc *auth.Service, logger *zap.SugaredLogger) *LocalCoreMailAdapter {
	return &LocalCoreMailAdapter{
		db:      db,
		authSvc: authSvc,
		logger:  logger,
	}
}

// ProvisionMailbox creates or updates a user record in the database with a hashed password.
// This is the "local" equivalent of what Stalwart's CLI does — we store the Argon2id hash
// directly so the mail engine can authenticate against it.
func (a *LocalCoreMailAdapter) ProvisionMailbox(email, password string, quota int64) error {
	hash, err := a.authSvc.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash mailbox password: %w", err)
	}

	// Upsert: update if exists, create if not
	var existing models.User
	result := a.db.Where("email = ?", email).First(&existing)
	if result.Error == nil {
		existing.PasswordHash = hash
		if quota > 0 {
			existing.QuotaMB = quota
		}
		return a.db.Save(&existing).Error
	}

	user := models.User{
		Email:        email,
		PasswordHash: hash,
		IsActive:     true,
		Role:         "user",
		QuotaMB:      quota,
	}
	return a.db.Create(&user).Error
}

// DeleteMailbox marks a user as inactive in the database (soft delete).
func (a *LocalCoreMailAdapter) DeleteMailbox(email string) error {
	result := a.db.Model(&models.User{}).Where("email = ?", email).Update("is_active", false)
	if result.Error != nil {
		return fmt.Errorf("failed to delete mailbox: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mailbox not found: %s", email)
	}
	return nil
}

// AddDomain registers a new domain in the database.
func (a *LocalCoreMailAdapter) AddDomain(domain string) error {
	d := models.Domain{
		Name:   domain,
		Status: "active",
	}
	return a.db.Create(&d).Error
}

// RemoveDomain removes a domain from the database.
func (a *LocalCoreMailAdapter) RemoveDomain(domain string) error {
	result := a.db.Where("name = ?", domain).Delete(&models.Domain{})
	if result.Error != nil {
		return fmt.Errorf("failed to remove domain: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("domain not found: %s", domain)
	}
	return nil
}
