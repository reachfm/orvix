package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api/handlers/settings"
	"github.com/orvix/orvix/internal/buildinfo"
	"go.uber.org/zap"
)

// AdminSettingsGet serves GET /api/v1/admin/settings
//
// Returns settings organized by section. The response merges:
//
//  1. The live config values (cfg.*) so the UI shows the actual
//     runtime state, including build metadata (version, commit, tag,
//     build_time, channel) sourced from internal/buildinfo.
//  2. Any DB-persisted overrides from the admin_settings table.
//     Persisted values are returned with a `db_overridden: true` flag
//     so the UI can render a "modified" badge.
//
// Secrets are never returned. Secret-shaped keys (jwt_secret,
// vapid_private_key, *_api_key, password_hash, license_key, etc.)
// are redacted to "REDACTED" in the response.
func (h *Handler) AdminSettingsGet(c fiber.Ctx) error {
	cfg := h.cfg
	bi := buildinfo.Get()

	// Live config snapshot.
	out := fiber.Map{
		"general": fiber.Map{
			"primary_domain": cfg.CoreMail.Hostname,
			"public_ipv4":    cfg.DNS.PublicIPv4,
			"public_ipv6":    cfg.DNS.PublicIPv6,
			"hostname":       cfg.CoreMail.Hostname,
			"version":        bi.Version,
			"commit":         bi.Commit,
			"tag":            bi.Tag,
			"build_time":     bi.BuildTime,
			"channel":        bi.Channel,
			"go_version":     bi.GoVersion,
			"os":             bi.OS,
			"arch":           bi.Arch,
			"is_dev_build":   bi.IsDev,
		},
		"mail_listeners": fiber.Map{
			"smtp_host":           cfg.CoreMail.SMTPHost,
			"smtp_port":           cfg.CoreMail.SMTPPort,
			"imap_host":           cfg.CoreMail.IMAPHost,
			"imap_port":           cfg.CoreMail.IMAPPort,
			"pop3_host":           cfg.CoreMail.POP3Host,
			"pop3_port":           cfg.CoreMail.POP3Port,
			"jmap_host":           cfg.CoreMail.JMAPHost,
			"jmap_port":           cfg.CoreMail.JMAPPort,
			"submission_enabled":  cfg.CoreMail.SubmissionEnabled,
			"submission_host":     cfg.CoreMail.SubmissionHost,
			"submission_port":     cfg.CoreMail.SubmissionPort,
			"smtps_enabled":       cfg.CoreMail.SMTPsEnabled,
			"smtps_host":          cfg.CoreMail.SMTPsHost,
			"smtps_port":          cfg.CoreMail.SMTPsPort,
			"imaps_enabled":       cfg.CoreMail.IMAPsEnabled,
			"imaps_host":          cfg.CoreMail.IMAPsHost,
			"imaps_port":          cfg.CoreMail.IMAPsPort,
			"pop3s_enabled":       cfg.CoreMail.POP3sEnabled,
			"pop3s_host":          cfg.CoreMail.POP3sHost,
			"pop3s_port":          cfg.CoreMail.POP3sPort,
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
		"build": fiber.Map{
			"version":    bi.Version,
			"commit":     bi.Commit,
			"tag":        bi.Tag,
			"build_time": bi.BuildTime,
			"channel":    bi.Channel,
			"go_version": bi.GoVersion,
			"os":         bi.OS,
			"arch":       bi.Arch,
			"is_dev":     bi.IsDev,
		},
	}

	// Merge DB-persisted overrides. The store may be nil in tests
	// that pre-date the settings completion work; in that case the
	// response is the live config snapshot only.
	if h.settingsStore != nil {
		persisted, err := h.settingsStore.GetAll(c.Context())
		if err != nil {
			h.logger.Warn("settings store GetAll failed", zap.Error(err))
		} else {
			overrides := fiber.Map{}
			for section, entries := range persisted {
				for _, e := range entries {
					// Find the section map in `out` (defaulting to
					// the overrides bucket if the section is not
					// represented in the live config).
					sectionMap, ok := out[string(section)].(fiber.Map)
					if !ok {
						sectionMap = fiber.Map{}
						out[string(section)] = sectionMap
					}
					// The key in admin_settings is the dotted path;
					// the GET response uses nested form, so we
					// expose both forms.
					field := e.Key
					if len(e.Key) > len(string(section))+1 && e.Key[:len(string(section))+1] == string(section)+"." {
						field = e.Key[len(string(section))+1:]
					}
					if e.Redacted {
						sectionMap[field] = fiber.Map{
							"value":         "REDACTED",
							"redacted":      true,
							"db_overridden": true,
						}
					} else {
						sectionMap[field] = fiber.Map{
							"value":             e.Value,
							"requires_restart":  e.RequiresRestart,
							"db_overridden":     true,
							"updated_at":        e.UpdatedAt,
						}
					}
				}
			}
			out["_persisted"] = overrides
		}
	} else {
		out["_settings_persistence"] = fiber.Map{
			"enabled":   false,
			"note":      "settings store not wired in this build; PATCH /admin/settings will return not_implemented",
		}
	}

	return c.JSON(out)
}

// AdminSettingsPatch serves PATCH /api/v1/admin/settings
//
// Persists admin settings to the admin_settings table. The previous
// release returned "not_implemented" with no side effects; this
// version applies a real allowlist, rejects unknown and unsafe
// fields, redacts secrets, and writes an audit log entry on every
// successful patch.
//
// Behavior:
//
//   - Unknown fields are rejected (the whole patch is rolled back).
//   - Unsafe fields (secrets, keys, license keys) are rejected
//     atomically with the same hard-reject behavior.
//   - Type mismatches are reported per-field; the rest of the
//     patch is still applied.
//   - restart_required is set to true when any applied field
//     requires a service restart to take effect.
//   - An audit log entry is written for every patch (applied
//     and rejected alike) so security can review who tried to
//     change what.
func (h *Handler) AdminSettingsPatch(c fiber.Ctx) error {
	if h.settingsStore == nil {
		// Backwards compatibility: the previous endpoint returned
		// a 200 with status="not_implemented". Keep that exact
		// shape so existing admin UI code paths still work when
		// the store is not wired (e.g., older tests).
		return c.JSON(fiber.Map{
			"status":           "not_implemented",
			"message":          "Settings persistence is not yet implemented in this release. Settings are read-only via GET /api/v1/admin/settings. A configuration file reload (orvix.yaml) and service restart are required to apply changes.",
			"restart_required": true,
		})
	}

	// Parse body. We accept both nested and dotted forms; the
	// store's flattenPatch normalizes them.
	var body map[string]map[string]json.RawMessage
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid JSON body: " + err.Error(),
		})
	}

	// Capture user id for the audit log and the row's updated_by
	// column. Falls back to nil if the middleware did not set it
	// (e.g., tests).
	var updatedBy *int64
	if uid, ok := c.Locals("user_id").(uint); ok && uid > 0 {
		v := int64(uid)
		updatedBy = &v
	}

	result, err := h.settingsStore.Patch(c.Context(), settings.Patch{
		Sections: body,
		UpdatedBy: updatedBy,
	})
	if err != nil {
		// Hard reject (unknown or unsafe) — every field is in Rejected.
		if result != nil {
			h.writeAuditLog(c, "settings.patch_rejected",
				fmt.Sprintf("rejected:%d|applied:0|restart_required:false", len(result.Rejected)))
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error":             "patch contained unknown or unsafe fields; nothing applied",
				"rejected":          result.Rejected,
				"applied":           []string{},
				"restart_required":  false,
			})
		}
		h.logger.Error("settings patch failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "settings patch failed: " + err.Error(),
		})
	}

	// Audit: applied and soft-rejected alike, so security can see
	// the full per-field history.
	h.writeAuditLog(c, "settings.patch",
		fmt.Sprintf("applied:%d|rejected:%d|restart_required:%v", len(result.Applied), len(result.Rejected), result.RestartRequired))

	return c.JSON(fiber.Map{
		"applied":          result.Applied,
		"rejected":         result.Rejected,
		"restart_required": result.RestartRequired,
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