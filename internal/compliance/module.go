package compliance

import (
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Compliance Center.
type Module struct {
	cfg     *config.Config
	db      *gorm.DB
	logger  *zap.Logger
	zke     *ZeroKnowledgeEncryption
}

func (m *Module) ID() string { return "compliance" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.zke = NewZeroKnowledgeEncryption(db)
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	m.logger.Info("compliance module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("compliance module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("compliance module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// ZKE returns the zero-knowledge encryption service.
func (m *Module) ZKE() *ZeroKnowledgeEncryption { return m.zke }

// LegalHold represents a legal hold placed on a mailbox.
type LegalHold struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	TargetEmail string  `gorm:"not null" json:"target_email"`
	Reason    string    `gorm:"type:text;not null" json:"reason"`
	Active    bool      `gorm:"default:true" json:"active"`
	CreatedBy uint      `gorm:"not null" json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RetentionPolicy defines email retention rules.
type RetentionPolicy struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	RetentionDays int  `gorm:"not null" json:"retention_days"`
	Action      string `gorm:"not null;default:'archive'" json:"action"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

var _ modules.Module = (*Module)(nil)
