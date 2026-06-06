package compose

import (
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Smart Compose AI.
type Module struct {
	cfg      *config.Config
	db       *gorm.DB
	logger   *zap.Logger
	streamer *Streamer
}

func (m *Module) ID() string { return "smart-compose" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.streamer = NewStreamer(cfg.AI.DeepSeekAPIKey, cfg.AI.DeepSeekModel, m.logger)
	m.logger.Info("smart-compose module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("smart-compose module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("smart-compose module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

// Streamer returns the AI streamer for use by handlers.
func (m *Module) Streamer() *Streamer { return m.streamer }

var _ modules.Module = (*Module)(nil)
