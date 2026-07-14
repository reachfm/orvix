package collaboration

import (
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Collaboration.
type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
}

func (m *Module) ID() string         { return "collaboration" }
func (m *Module) Version() string    { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core", "calendar"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	m.logger.Info("collaboration module initialized")
	return nil
}

func (m *Module) Start() error   { m.logger.Info("collaboration module started"); return nil }
func (m *Module) Stop() error    { m.logger.Info("collaboration module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// SharedMailbox represents a shared mailbox accessible by multiple users.
type SharedMailbox struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	TenantID    uint      `gorm:"index;not null" json:"tenant_id"`
	Name        string    `gorm:"not null" json:"name"`
	Email       string    `gorm:"not null" json:"email"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// SharedCalendar represents a shared calendar.
type SharedCalendar struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	TenantID    uint      `gorm:"index;not null" json:"tenant_id"`
	Name        string    `gorm:"not null" json:"name"`
	Color       string    `json:"color"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

var _ modules.Module = (*Module)(nil)
