package admin_handlers

import (
	"github.com/orvixemail/orvix/internal/adapters"
	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HandlerConfig holds dependencies for Admin Control Plane handlers.
type HandlerConfig struct {
	DB          *gorm.DB
	Logger      *zap.SugaredLogger
	Auth        *auth.Service
	MailAdapter adapters.CoreMailEngineAdapter
}

// Handler is the receiver for all Admin Control Plane HTTP handlers.
type Handler struct {
	cfg HandlerConfig
}

// New creates a new Admin Control Plane Handler.
func New(cfg HandlerConfig) *Handler {
	return &Handler{cfg: cfg}
}

// planToTier maps a plan name to a Tenant tier and limits.
type planMapping struct {
	Tier         string
	MaxDomains   int
	MaxMailboxes int
}

var planDefaults = map[string]planMapping{
	"trial":     {"smb", 1, 10},
	"starter":   {"smb", 3, 50},
	"pro":       {"isp", 50, 5000},
	"enterprise": {"enterprise", 1000, 100000},
}

func resolvePlan(plan string) planMapping {
	if p, ok := planDefaults[plan]; ok {
		return p
	}
	return planDefaults["trial"]
}

// Helper to build the Tenant model from a plan.
func newTenantFromPlan(name, plan string) models.Tenant {
	p := resolvePlan(plan)
	slug := name
	return models.Tenant{
		Name:         name,
		Slug:         slug,
		Tier:         p.Tier,
		MaxDomains:   p.MaxDomains,
		MaxMailboxes: p.MaxMailboxes,
		Active:       true,
	}
}
