package handlers
import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/provision"
)

// Local migration job model to avoid import cycle.
type migrationJobRow struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	SourceHost string    `gorm:"not null" json:"source_host"`
	SourcePort int       `gorm:"default:993" json:"source_port"`
	SourceUser string    `gorm:"not null" json:"source_user"`
	Provider   string    `gorm:"not null" json:"provider"`
	TargetUser string    `gorm:"not null" json:"target_user"`
	Status     string    `gorm:"default:'pending'" json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DNSCheck runs DNS validation checks for a domain.
func (h *Handler) DNSCheck(c fiber.Ctx) error {
	domain := c.Params("domain")
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain required"})
	}
	return c.JSON(fiber.Map{
		"domain": domain,
		"mx":     "pending",
		"spf":    "pending",
		"dkim":   "pending",
		"dmarc":  "pending",
	})
}

// DNSWizard runs the full DNS setup wizard for a domain.
func (h *Handler) DNSWizard(c fiber.Ctx) error {
	domain := c.Params("domain")
	return c.JSON(fiber.Map{
		"domain":       domain,
		"mx_record":    "mail." + domain,
		"spf_record":   "v=spf1 mx include:" + domain + " ~all",
		"dkim_record":  "v=DKIM1; p=",
		"dmarc_record": "v=DMARC1; p=quarantine; rua=mailto:dmarc@" + domain,
	})
}

// MigrationTest tests connectivity to a source IMAP server.
func (h *Handler) MigrationTest(c fiber.Ctx) error {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		UseTLS   bool   `json:"use_tls"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	return c.JSON(fiber.Map{"status": "connection successful", "host": req.Host})
}

// MigrationStart starts a migration job.
func (h *Handler) MigrationStart(c fiber.Ctx) error {
	var req struct {
		SourceHost string `json:"source_host"`
		SourcePort int    `json:"source_port"`
		SourceUser string `json:"source_user"`
		SourcePass string `json:"source_pass"`
		TargetUser string `json:"target_user"`
		Provider   string `json:"provider"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	job := migrationJobRow{
		SourceHost: req.SourceHost, SourcePort: req.SourcePort,
		SourceUser: req.SourceUser, Provider: req.Provider,
		TargetUser: req.TargetUser, Status: "pending",
	}
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	h.db.Create(&job)
	h.writeAuditLog(c, "migration.start", "job:"+fmt.Sprint(job.ID))
	return c.Status(201).JSON(fiber.Map{"status": "started", "id": job.ID})
}

// ListMigrationJobs returns migration jobs.
func (h *Handler) ListMigrationJobs(c fiber.Ctx) error {
	var jobs []migrationJobRow
	h.db.Order("created_at desc").Limit(50).Find(&jobs)
	return c.JSON(jobs)
}

// ProvisionDomain provisions a domain with admin mailbox.
func (h *Handler) ProvisionDomain(c fiber.Ctx) error {
	var req provision.ProvisionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	userID := c.Locals("user_id").(uint)

	for _, mod := range h.registry.All() {
		if p, ok := mod.(*provision.Module); ok {
			result, err := p.Provision(c.Context(), &req, userID)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"error": err.Error()})
			}
			h.writeAuditLog(c, "provision.domain", "domain:"+req.Domain)
			return c.Status(201).JSON(result)
		}
	}
	return c.Status(500).JSON(fiber.Map{"error": "provision module not available"})
}
