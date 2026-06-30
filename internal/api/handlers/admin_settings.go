package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
)

// AdminSettingsGet serves GET /api/v1/admin/settings
// Returns settings organized by section. Never returns secrets.
func (h *Handler) AdminSettingsGet(c fiber.Ctx) error {
	cfg := h.cfg
	settings := fiber.Map{
		"general": fiber.Map{
			"primary_domain": cfg.CoreMail.Hostname,
			"public_ipv4":    cfg.DNS.PublicIPv4,
			"public_ipv6":    cfg.DNS.PublicIPv6,
			"hostname":       cfg.CoreMail.Hostname,
			"version":        config.GetWatermark().Version,
		},
		"mail_listeners": fiber.Map{
			"smtp_host":  cfg.CoreMail.SMTPHost,
			"smtp_port":  cfg.CoreMail.SMTPPort,
			"imap_host":  cfg.CoreMail.IMAPHost,
			"imap_port":  cfg.CoreMail.IMAPPort,
			"pop3_host":  cfg.CoreMail.POP3Host,
			"pop3_port":  cfg.CoreMail.POP3Port,
			"jmap_host":  cfg.CoreMail.JMAPHost,
			"jmap_port":  cfg.CoreMail.JMAPPort,
			"submission_enabled": cfg.CoreMail.SubmissionEnabled,
			"submission_host":    cfg.CoreMail.SubmissionHost,
			"submission_port":    cfg.CoreMail.SubmissionPort,
			"smtps_enabled":      cfg.CoreMail.SMTPsEnabled,
			"smtps_host":         cfg.CoreMail.SMTPsHost,
			"smtps_port":         cfg.CoreMail.SMTPsPort,
			"imaps_enabled":      cfg.CoreMail.IMAPsEnabled,
			"imaps_host":         cfg.CoreMail.IMAPsHost,
			"imaps_port":         cfg.CoreMail.IMAPsPort,
			"pop3s_enabled":      cfg.CoreMail.POP3sEnabled,
			"pop3s_host":         cfg.CoreMail.POP3sHost,
			"pop3s_port":         cfg.CoreMail.POP3sPort,
		},
		"security": fiber.Map{
			"password_min_len":    cfg.Auth.PasswordMinLen,
			"session_ttl_seconds": int(cfg.Auth.JWTAccessTTL.Seconds()),
			"refresh_ttl_seconds": int(cfg.Auth.JWTRefreshTTL.Seconds()),
		},
		"backup": fiber.Map{
			"dir":             cfg.Backup.Dir,
			"retention_count": cfg.Backup.RetentionCount,
		},
		"dns": fiber.Map{
			"public_ipv4":               cfg.DNS.PublicIPv4,
			"public_ipv6":               cfg.DNS.PublicIPv6,
			"cloudflare_zone_configured": cfg.DNS.CloudflareZoneID != "",
			"namecheap_configured":       cfg.DNS.NamecheapAPIKey != "",
		},
	}
	return c.JSON(settings)
}

// AdminSettingsPatch serves PATCH /api/v1/admin/settings
// Durability: settings mutations are in-memory only and are not
// persisted to orvix.yaml or the database. PATCH is intentionally
// disabled for this release. The API returns an honest status so
// the UI never shows fake save buttons.
// The GET endpoint returns real read-only settings.
func (h *Handler) AdminSettingsPatch(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":           "not_implemented",
		"message":          "Settings persistence is not yet implemented in this release. Settings are read-only via GET /api/v1/admin/settings. A configuration file reload (orvix.yaml) and service restart are required to apply changes.",
		"restart_required": true,
	})
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}
