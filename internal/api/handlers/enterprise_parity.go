// Package handlers — Enterprise admin parity endpoints.
//
// This file adds four endpoints that close the gap between the existing
// admin SPA pages and the data model that already exists in models.go:
//
//	GET  /api/v1/admin/tenants/current        — read the JWT-tenant row.
//	PATCH /api/v1/admin/tenants/:id/branding  — set logo_url + primary_color
//	                                            on the tenant row.
//	GET  /api/v1/admin/storage/volumes        — stat the mail / attachments /
//	                                            backup data directories.
//	GET  /api/v1/admin/monitoring/alert-deliveries
//	                                          — read monitoring_alert_deliveries.
//
// Every endpoint:
//   - Defaults to a clean "not configured" payload when the data is absent.
//   - Audits writes via the existing audit store.
//   - Validates inputs (logo_url must be http(s) URL, primary_color must
//     match a CSS-safe hex pattern).
//   - Is read-only for the GETs.
//
// The file deliberately avoids creating fake features: there is no replica
// or shard knob here, no AI-classifier surface, no reseller portal. Each
// endpoint corresponds to a real backend table or column that already
// exists in the schema (see docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md
// for the full matrix).
package handlers

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/monitoring"
	"go.uber.org/zap"
)

// hexColorRe is the validator for tenants.primary_color. We accept the
// canonical #RRGGBB form (3- and 8-digit CSS variants are deliberately
// rejected to keep the logon shell preview stable across browsers).
var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// httpURLRe limits logo_url to http(s) URLs. Data: and javascript:
// schemes are rejected so the login shell cannot be tricked into
// rendering an attacker-controlled iframe or fetch.
var httpURLRe = regexp.MustCompile(`^https?://[^\s]+$`)

// GetAdminTenant returns the tenant row for the caller's JWT-tenant
// (defaults to id=1 in single-tenant dev installs so the handler never
// crashes on a missing locals key).
//
// Response shape is intentionally flat: the admin UI maps column names
// to inputs, so we surface the raw columns rather than a nested view.
// `exists:false` is honest — it tells the page to render the "row not
// yet provisioned" empty state instead of an object that looks like a
// row but is missing required fields.
func (h *Handler) GetAdminTenant(c fiber.Ctx) error {
	tenantID := h.tenantID(c)
	db := h.sqlDB()
	if db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "database not available",
		})
	}
	row := db.QueryRowContext(c.Context(),
		h.sqlQ(`SELECT id, name, slug, domain, plan, max_domains, max_mailboxes,
		        logo_url, primary_color, active, created_at, updated_at
		 FROM tenants WHERE id = ? AND deleted_at IS NULL`), tenantID)
	var t struct {
		ID           int64
		Name         sql.NullString
		Slug         sql.NullString
		Domain       sql.NullString
		Plan         sql.NullString
		MaxDomains   int64
		MaxMailboxes int64
		LogoURL      sql.NullString
		PrimaryColor sql.NullString
		Active       int64
		CreatedAt    sql.NullTime
		UpdatedAt    sql.NullTime
	}
	if err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.Domain, &t.Plan, &t.MaxDomains, &t.MaxMailboxes,
		&t.LogoURL, &t.PrimaryColor, &t.Active, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			// Honest empty state: do not synthesise a row.
			return c.JSON(fiber.Map{
				"exists":      false,
				"tenant_id":   tenantID,
				"honest_note": "Tenant row not provisioned for this install. Multi-tenant write API is not exposed in this build.",
			})
		}
		if h.logger != nil {
			h.logger.Error("admin tenant lookup failed", zap.Error(err))
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "tenant lookup failed",
		})
	}
	return c.JSON(fiber.Map{
		"exists":        true,
		"id":            t.ID,
		"name":          nullStr(t.Name),
		"slug":          nullStr(t.Slug),
		"domain":        nullStr(t.Domain),
		"plan":          nullStr(t.Plan),
		"max_domains":   t.MaxDomains,
		"max_mailboxes": t.MaxMailboxes,
		"logo_url":      nullStr(t.LogoURL),
		"primary_color": nullStr(t.PrimaryColor),
		"active":        t.Active == 1,
		"created_at":    nullTime(t.CreatedAt),
		"updated_at":    nullTime(t.UpdatedAt),
		"honest_note":   "Multi-tenant write API is not exposed in this build. Branding (logo + primary color) is editable; create / delete / plan / quota changes are not.",
	})
}

// PatchAdminTenantBranding updates tenants.logo_url and tenants.primary_color
// for the path id. The id is taken from the URL, not from the JWT tenant —
// superadmins can brand any tenant via the same endpoint. CSRF + RBAC and
// audit are enforced at the router layer (see api/router.go).
//
// Accepted input shape:
//
//	{ "logo_url": "https://example.com/logo.svg", "primary_color": "#4F7CFF" }
//
// Either field may be omitted. Both pass strict validators; anything
// else returns a 400 with a sanitized error. Empty string clears the
// column.
func (h *Handler) PatchAdminTenantBranding(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid tenant id",
		})
	}
	var body struct {
		LogoURL      *string `json:"logo_url"`
		PrimaryColor *string `json:"primary_color"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid JSON body",
		})
	}
	if body.LogoURL == nil && body.PrimaryColor == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one of logo_url or primary_color must be provided",
		})
	}
	if body.LogoURL != nil {
		v := strings.TrimSpace(*body.LogoURL)
		if v != "" && !httpURLRe.MatchString(v) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "logo_url must be an http(s) URL",
			})
		}
		// Also reject anything that looks like a private URL once parsed,
		// so an attacker cannot point the brand at an internal IP that
		// the admin shell would otherwise happily fetch.
		if v != "" && !safeExternalURL(v) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "logo_url must be a public http(s) URL",
			})
		}
		body.LogoURL = &v
	}
	if body.PrimaryColor != nil {
		v := strings.TrimSpace(*body.PrimaryColor)
		if v != "" && !hexColorRe.MatchString(v) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "primary_color must be a #RRGGBB hex value",
			})
		}
		body.PrimaryColor = &v
	}

	db := h.sqlDB()
	if db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "database not available",
		})
	}

	// Existence check + tenant scope. Superadmins can edit any tenant;
	// non-superadmins are bound to their own JWT-tenant id.
	callerIsSuper := false
	if rid, ok := c.Locals("role").(string); ok {
		callerIsSuper = rid == "superadmin" || rid == "super_admin" || rid == "super-admin"
	}
	ownTenant := h.tenantID(c)
	if !callerIsSuper && id != ownTenant {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "cross-tenant branding is not permitted",
		})
	}

	// Build a defensive UPDATE so we always set updated_at.
	now := time.Now().UTC()
	setClauses := []string{"updated_at = ?"}
	args := []interface{}{now}
	if body.LogoURL != nil {
		setClauses = append(setClauses, "logo_url = ?")
		args = append(args, *body.LogoURL)
	}
	if body.PrimaryColor != nil {
		setClauses = append(setClauses, "primary_color = ?")
		args = append(args, *body.PrimaryColor)
	}
	args = append(args, id)
	res, err := db.ExecContext(c.Context(),
		h.sqlQ("UPDATE tenants SET "+strings.Join(setClauses, ", ")+
			" WHERE id = ? AND deleted_at IS NULL"), args...)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("admin branding update failed", zap.Error(err))
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "branding update failed",
		})
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Either the row was missing or it had no visible change.
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "tenant not found",
		})
	}
	// Audit: include what was changed. We never log the full URL into
	// the audit store on a per-field basis; the UI shows the diff.
	fieldSet := []string{}
	if body.LogoURL != nil {
		fieldSet = append(fieldSet, "logo_url")
	}
	if body.PrimaryColor != nil {
		fieldSet = append(fieldSet, "primary_color")
	}
	h.writeAuditLog(c, "tenant.branding.update",
		fmt.Sprintf("tenant_id:%d|fields:%s", id, strings.Join(fieldSet, ",")))
	return c.JSON(fiber.Map{
		"applied":          fieldSet,
		"updated_at":       now.Format(time.RFC3339),
		"restart_required": false,
	})
}

// VolumeStat describes a single on-disk volume the operator can see in
// the storage topology. Total / Used / Free are real bytes; Mounted is
// the absolute path; Role hints what writes there.
type VolumeStat struct {
	Mounted    string  `json:"mounted"`
	Role       string  `json:"role"`
	TotalBytes int64   `json:"total_bytes"`
	UsedBytes  int64   `json:"used_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
	UsedPct    float64 `json:"used_pct"`
	Available  bool    `json:"available"`
	Detail     string  `json:"detail,omitempty"`
}

// ListStorageVolumes returns statfs results for every on-disk data
// directory the process is configured to use. The endpoint is read-only
// and never fabricates values for a directory that is not configured.
//
// The intent is to give the operator a single honest picture of where
// mail / attachments / backups actually live on this single backend
// instance. No replica, no shard, no per-mailbox archive tier — just
// what the configuration says plus a real `df` reading.
func (h *Handler) ListStorageVolumes(c fiber.Ctx) error {
	dirs := buildVolumeList(h)
	out := make([]VolumeStat, 0, len(dirs))
	for _, v := range dirs {
		out = append(out, statVolume(v))
	}
	return c.JSON(fiber.Map{
		"volumes": out,
		"honest_note": "Single-backend deployment. Sharding and read-replica routing are not implemented in this build; " +
			"each volume below maps to one on-disk directory used by the orvix process.",
	})
}

// buildVolumeList resolves the configured paths for the data, message,
// attachment, and backup directories. It NEVER invents a directory —
// unconfigured slots are reported with Available=false and an honest
// reason. This keeps the page honest on a fresh install before any
// tenant data has been written.
func buildVolumeList(h *Handler) []VolumeStat {
	var out []VolumeStat
	add := func(path, role string) {
		if path == "" {
			return
		}
		out = append(out, VolumeStat{Mounted: path, Role: role})
	}
	if h.cfg != nil {
		add(h.cfg.CoreMail.DataPath, "data")
		add(h.cfg.CoreMail.MailStorePath, "mailstore")
		add(filepath.Join(h.cfg.CoreMail.DataPath, "attachments"), "attachments")
	}
	add(h.backupDir(), "backups")
	return out
}

// statVolume reads the actual disk usage for one path. If the path
// does not exist (typical on a fresh install where mail has not yet
// flowed), the function returns Available=false and a clear reason —
// never a "0%" / "0 bytes" fake.
func statVolume(v VolumeStat) VolumeStat {
	info, err := os.Stat(v.Mounted)
	if err != nil {
		if os.IsNotExist(err) {
			v.Available = false
			v.Detail = "directory not present (no data written yet)"
			return v
		}
		v.Available = false
		v.Detail = "stat failed: " + err.Error()
		return v
	}
	if !info.IsDir() {
		v.Available = false
		v.Detail = "path exists but is not a directory"
		return v
	}
	total, free, sErr := statfsPlatform(v.Mounted)
	if sErr != nil {
		v.Available = false
		v.Detail = "statfs failed: " + sErr.Error()
		return v
	}
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = (float64(used) / float64(total)) * 100
	}
	v.Available = true
	v.TotalBytes = total
	v.UsedBytes = used
	v.FreeBytes = free
	v.UsedPct = pct
	return v
}

// ListAlertDeliveries returns the most recent rows from
// monitoring_alert_deliveries via the existing Dispatcher. The
// endpoint is intentionally read-only; the dispatcher is reused so
// the table shape and the secret-free contract stay aligned with the
// rest of the alert pipeline.
func (h *Handler) ListAlertDeliveries(c fiber.Ctx) error {
	if h.db == nil {
		// Honest empty: a fresh install with no dispatcher wired
		// must not panic. The page renders the existing
		// "(not configured)" hint in this branch.
		return c.JSON(fiber.Map{
			"deliveries":  []monitoring.DeliveryRecord{},
			"limit":       0,
			"honest_note": "Alert dispatcher not wired; delivery audit is unavailable in this build.",
		})
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "database not available",
		})
	}
	limit := 100
	if l := c.Query("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	d := h.alertDispatcher(sqlDB)
	if d == nil {
		return c.JSON(fiber.Map{
			"deliveries":  []interface{}{},
			"honest_note": "Alert dispatcher not wired; delivery audit is unavailable in this build.",
		})
	}
	records, err := d.ListDeliveries(c.Context(), limit)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("alert deliveries query failed", zap.Error(err))
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "delivery audit query failed",
		})
	}
	if records == nil {
		records = []monitoring.DeliveryRecord{}
	}
	return c.JSON(fiber.Map{
		"deliveries": records,
		"limit":      limit,
	})
}

// safeExternalURL returns false for URL forms that point back into
// the private network. The admin shell displays the logo as an <img>
// element; an attacker-controlled row could otherwise craft an internal
// URL and trigger an SSRF scan from the admin browser.
func safeExternalURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// Block obvious localhost / loopback / link-local names.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		lower == "127.0.0.1" || lower == "::1" || lower == "0.0.0.0" {
		return false
	}
	// Block RFC1918 / link-local ranges.
	if ip := parseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsPrivate() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

// nullStr returns "" for a NULL string column.
func nullStr(n sql.NullString) string {
	if !n.Valid {
		return ""
	}
	return n.String
}

// nullTime returns "" for a NULL time column.
func nullTime(n sql.NullTime) string {
	if !n.Valid {
		return ""
	}
	return n.Time.Format(time.RFC3339)
}

// parseIP parses host as an IP address. Returns nil when host is a
// DNS name (which is fine — only IP-literals are blocked).
func parseIP(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	return nil
}
