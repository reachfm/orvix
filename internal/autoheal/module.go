package autoheal

import (
	"context"
	"os"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Auto-Heal.
type Module struct {
	cfg     *config.Config
	db      *gorm.DB
	logger  *zap.Logger
	monitor *Monitor
}

func (m *Module) ID() string         { return "auto-heal" }
func (m *Module) Version() string    { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.monitor = NewMonitor(m.logger)
	m.monitor.SetDB(db)
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL in models.MigrateAllRaw
	m.registerChecks()
	m.logger.Info("auto-heal module initialized")
	return nil
}

func (m *Module) Start() error {
	m.monitor.Start(context.Background())
	m.logger.Info("auto-heal module started")
	return nil
}

func (m *Module) Stop() error {
	m.monitor.Stop()
	m.logger.Info("auto-heal module stopped")
	return nil
}

func (m *Module) Migrate() error { return nil }

func (m *Module) Monitor() *Monitor { return m.monitor }

func (m *Module) registerChecks() {
	m.monitor.AddCheck(HealthCheck{
		Name:     "database",
		Interval: 60 * time.Second,
		Severity: "high",
		Check:    m.checkDatabase,
	})

	m.monitor.AddCheck(HealthCheck{
		Name:     "disk",
		Interval: 300 * time.Second,
		Severity: "medium",
		Check:    m.checkDisk,
	})
}

func (m *Module) checkDatabase(ctx context.Context) (string, error) {
	sqlDB, err := m.db.DB()
	if err != nil {
		return "database connection unavailable", err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return "database ping failed", err
	}
	return "", nil
}

func (m *Module) checkDisk(ctx context.Context) (string, error) {
	path := m.cfg.Database.SQLitePath
	if path == "" {
		path = "/var/lib/orvix"
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	return "", nil
}

var _ modules.Module = (*Module)(nil)
