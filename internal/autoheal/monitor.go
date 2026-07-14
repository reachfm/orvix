package autoheal

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HealHistory represents an auto-heal action record.
type HealHistory struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	CheckName  string    `gorm:"not null" json:"check_name"`
	Severity   string    `gorm:"not null" json:"severity"`
	Issue      string    `gorm:"type:text;not null" json:"issue"`
	FixApplied string    `gorm:"type:text" json:"fix_applied"`
	Success    bool      `gorm:"not null" json:"success"`
	CreatedAt  time.Time `json:"created_at"`
}

// HealthCheck represents a single health check.
type HealthCheck struct {
	Name     string
	Interval time.Duration
	Severity string
	Check    func(ctx context.Context) (string, error)
	Fix      func(ctx context.Context, issue string) (string, bool)
}

// Monitor runs health checks at regular intervals.
type Monitor struct {
	checks []HealthCheck
	logger *zap.Logger
	db     *gorm.DB
	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewMonitor creates a new health monitor.
func NewMonitor(logger *zap.Logger) *Monitor {
	return &Monitor{
		logger: logger,
	}
}

// SetDB sets the database connection for persisting health records.
func (m *Monitor) SetDB(db *gorm.DB) {
	m.db = db
}

// AddCheck adds a health check to the monitor.
func (m *Monitor) AddCheck(check HealthCheck) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks = append(m.checks, check)
	m.logger.Info("health check registered",
		zap.String("name", check.Name),
		zap.String("severity", check.Severity),
	)
}

// Start begins running all health checks.
func (m *Monitor) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)

	for _, check := range m.checks {
		go m.runCheck(ctx, check)
	}

	m.logger.Info("health monitor started", zap.Int("checks", len(m.checks)))
}

// Stop stops all health checks.
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.logger.Info("health monitor stopped")
}

func (m *Monitor) runCheck(ctx context.Context, check HealthCheck) {
	ticker := time.NewTicker(check.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.executeCheck(ctx, check)
		}
	}
}

func (m *Monitor) executeCheck(ctx context.Context, check HealthCheck) {
	issue, err := check.Check(ctx)
	if err != nil {
		m.logger.Error("health check failed",
			zap.String("check", check.Name),
			zap.Error(err),
		)
		m.saveRecord(check.Name, check.Severity, err.Error(), "", false)
		return
	}

	if issue == "" {
		return
	}

	m.logger.Warn("health check detected issue",
		zap.String("check", check.Name),
		zap.String("issue", issue),
		zap.String("severity", check.Severity),
	)

	var fixApplied string
	var success bool
	if check.Fix != nil {
		fixApplied, success = check.Fix(ctx, issue)
		m.logger.Info("auto-heal attempted",
			zap.String("check", check.Name),
			zap.String("fix", fixApplied),
			zap.Bool("success", success),
		)
	}

	m.saveRecord(check.Name, check.Severity, issue, fixApplied, success)
}

func (m *Monitor) saveRecord(checkName, severity, issue, fixApplied string, success bool) {
	if m.db == nil {
		return
	}
	m.db.Create(&HealHistory{
		CheckName:  checkName,
		Severity:   severity,
		Issue:      issue,
		FixApplied: fixApplied,
		Success:    success,
	})
}
