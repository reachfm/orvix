package dns

import (
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for DNS Automation.
type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
	mgr    *Manager
}

func (m *Module) ID() string         { return "dns-automation" }
func (m *Module) Version() string    { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.mgr = NewManager(m.logger)
	if cfg.DNS.CloudflareAPIKey != "" {
		m.mgr.RegisterProvider(NewCloudflareProvider(cfg.DNS.CloudflareAPIKey, m.logger))
	}
	m.logger.Info("dns-automation module initialized")
	return nil
}

func (m *Module) Start() error {
	m.logger.Info("dns-automation module started")
	return nil
}

func (m *Module) Stop() error {
	m.logger.Info("dns-automation module stopped")
	return nil
}

func (m *Module) Migrate() error {
	return nil
}

// Mgr returns the DNS manager for use by handlers.
func (m *Module) Mgr() *Manager { return m.mgr }

var _ modules.Module = (*Module)(nil)
