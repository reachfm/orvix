package intelligence

import (
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Email Intelligence.
type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
}

func (m *Module) ID() string { return "email-intelligence" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	_ = db.AutoMigrate(&EmailAnalytics{}, &DeliveryReport{})
	m.logger.Info("email-intelligence module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("email-intelligence module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("email-intelligence module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// EmailAnalytics tracks email statistics.
type EmailAnalytics struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Date       time.Time `gorm:"index;not null" json:"date"`
	Domain     string    `gorm:"index" json:"domain"`
	SentCount  int64     `gorm:"default:0" json:"sent_count"`
	RecvCount  int64     `gorm:"default:0" json:"recv_count"`
	BounceCount int64    `gorm:"default:0" json:"bounce_count"`
	SpamCount  int64     `gorm:"default:0" json:"spam_count"`
	CreatedAt  time.Time `json:"created_at"`
}

// DeliveryReport tracks individual email delivery status.
type DeliveryReport struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	MessageID string    `gorm:"index;not null" json:"message_id"`
	Recipient string    `gorm:"not null" json:"recipient"`
	Status    string    `gorm:"not null" json:"status"`
	DurationMs int64    `json:"duration_ms"`
	Error     string    `gorm:"type:text" json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

var _ modules.Module = (*Module)(nil)
