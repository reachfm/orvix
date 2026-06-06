package updater

import (
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Auto-Update.
type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
	mgr    *UpdateManager
}

func (m *Module) ID() string { return "auto-update" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.mgr = NewUpdateManager(cfg.Update.CheckURL, cfg.Update.Channel, m.logger)
	m.logger.Info("auto-update module initialized")
	return nil
}

func (m *Module) Start() error {
	m.logger.Info("auto-update module started")
	return nil
}

func (m *Module) Stop() error {
	m.logger.Info("auto-update module stopped")
	return nil
}

func (m *Module) Migrate() error {
	return nil
}

// Mgr returns the update manager for use by handlers.
func (m *Module) Mgr() *UpdateManager { return m.mgr }

var _ modules.Module = (*Module)(nil)
