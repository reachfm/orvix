package provision

import (
	"context"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/stalwart"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Module struct {
	cfg    *config.Config
	db     *gorm.DB
	logger *zap.Logger
	client *stalwart.Client
}

func (m *Module) ID() string { return "provision-api" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()
	m.logger.Info("provision-api module initialized")
	return nil
}

func (m *Module) Start() error { m.logger.Info("provision-api module started"); return nil }
func (m *Module) Stop() error { m.logger.Info("provision-api module stopped"); return nil }
func (m *Module) Migrate() error { return nil }

func (m *Module) SetStalwartClient(client *stalwart.Client) { m.client = client }

type ProvisionRequest struct {
	Domain    string `json:"domain"`
	Plan      string `json:"plan"`
	AdminUser string `json:"admin_user"`
	AdminPass string `json:"admin_pass"`
	QuotaMB   int    `json:"quota_mb"`
}

type ProvisionResponse struct {
	Status        string `json:"status"`
	Domain        string `json:"domain"`
	AdminEmail    string `json:"admin_email"`
	WebmailURL    string `json:"webmail_url"`
	ProvisionedMs int64  `json:"provisioned_ms"`
}

func (m *Module) Provision(ctx context.Context, req *ProvisionRequest, userID uint) (*ProvisionResponse, error) {
	start := time.Now()
	if m.client == nil {
		return nil, fmt.Errorf("Stalwart client not configured")
	}
	if err := m.client.CreateDomain(ctx, req.Domain); err != nil {
		return nil, fmt.Errorf("failed to create domain: %w", err)
	}
	adminEmail := req.AdminUser + "@" + req.Domain
	principal := stalwart.Principal{
		Name: adminEmail, Type: "individual",
		Quota: int64(req.QuotaMB) * 1024 * 1024,
		Emails: []string{adminEmail}, Enabled: true,
	}
	if err := m.client.CreatePrincipal(ctx, principal); err != nil {
		m.logger.Error("provision failed, rolling back domain", zap.Error(err))
		m.client.DeleteDomain(ctx, req.Domain)
		return nil, fmt.Errorf("failed to create mailbox: %w", err)
	}
	m.db.Create(&models.ProvisionedDomain{
		Domain: req.Domain, Plan: req.Plan, Status: "active", ProvisionedBy: userID,
	})
	elapsed := time.Since(start).Milliseconds()
	m.logger.Info("domain provisioned", zap.String("domain", req.Domain), zap.Int64("ms", elapsed))
	return &ProvisionResponse{
		Status: "active", Domain: req.Domain, AdminEmail: adminEmail,
		WebmailURL: fmt.Sprintf("https://mail.%s", req.Domain), ProvisionedMs: elapsed,
	}, nil
}

var _ modules.Module = (*Module)(nil)
