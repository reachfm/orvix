package firewall

import (
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for the Mail Firewall.
type Module struct {
	cfg      *config.Config
	db       *gorm.DB
	logger   *zap.Logger
	pipeline *Pipeline
	engine   *RuleEngine
}

func (m *Module) ID() string { return "mail-firewall" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.pipeline = NewPipeline(m.logger)
	m.engine = NewRuleEngine(db, m.logger)
	m.logger.Info("firewall module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("firewall module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("firewall module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// Pipeline returns the firewall pipeline.
func (m *Module) Pipeline() *Pipeline { return m.pipeline }
// Engine returns the rule engine.
func (m *Module) Engine() *RuleEngine { return m.engine }

var _ modules.Module = (*Module)(nil)
