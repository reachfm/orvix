package handlers

import (
	"database/sql"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

type adminDomainReport struct {
	Domain            string `json:"domain"`
	TenantID          int64  `json:"tenant_id,omitempty"`
	MailboxCount      int64  `json:"mailbox_count"`
	StorageBytes      int64  `json:"storage_bytes"`
	SentCount         int64  `json:"sent_count"`
	ReceivedCount     int64  `json:"received_count"`
	DeferredCount     int64  `json:"deferred_count"`
	BouncedCount      int64  `json:"bounced_count"`
	RejectedCount     int64  `json:"rejected_count"`
	LoginFailures     int64  `json:"login_failures"`
	SuspiciousLogins  int64  `json:"suspicious_login_count"`
	DNSHealth         string `json:"dns_health"`
	SPFStatus         string `json:"spf_status"`
	DKIMStatus        string `json:"dkim_status"`
	DMARCStatus       string `json:"dmarc_status"`
}

type queueCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type tenantOpsRow struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Domain          string `json:"domain"`
	Plan            string `json:"plan"`
	Active          bool   `json:"active"`
	Domains         int64  `json:"domains"`
	Mailboxes       int64  `json:"mailboxes"`
	StorageBytes    int64  `json:"storage_bytes"`
	LoginFailures   int64  `json:"login_failures"`
	DeferredCount   int64  `json:"deferred_count"`
	RejectedCount   int64  `json:"rejected_count"`
}

func isSuperRole(c fiber.Ctx) bool {
	role, _ := c.Locals("role").(auth.Role)
	return role == auth.RoleSuperAdmin
}

func (h *Handler) scopedTenantID(c fiber.Ctx) int64 {
	if v, ok := c.Locals("tenant_id").(uint); ok && v > 0 {
		return int64(v)
	}
	return h.tenantID(c)
}

func sqlInt(db *sql.DB, query string, args ...any) int64 {
	if db == nil {
		return 0
	}
	var n sql.NullInt64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil || !n.Valid {
		return 0
	}
	return n.Int64
}

func sqlString(db *sql.DB, query string, args ...any) string {
	if db == nil {
		return ""
	}
	var s sql.NullString
	if err := db.QueryRow(query, args...).Scan(&s); err != nil || !s.Valid {
		return ""
	}
	return s.String
}

func domainList(db *sql.DB, tenantID int64, crossTenant bool) []adminDomainReport {
	out := []adminDomainReport{}
	if db == nil {
		return out
	}
	query := "SELECT id, name, tenant_id, COALESCE(dkim_enabled,0), COALESCE(dmarc_enabled,0) FROM coremail_domains WHERE deleted_at IS NULL"
	args := []any{}
	if !crossTenant {
		query += " AND tenant_id = ?"
		args = append(args, tenantID)
	}
	query += " ORDER BY name ASC"
	rows, err := db.Query(query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, tid int64
		var name string
		var dkim, dmarc int
		if err := rows.Scan(&id, &name, &tid, &dkim, &dmarc); err != nil {
			continue
		}
		like := "%@" + name
		report := adminDomainReport{
			Domain:           name,
			TenantID:         tid,
			MailboxCount:     sqlInt(db, "SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", id),
			StorageBytes:     sqlInt(db, "SELECT COALESCE(SUM(used_bytes),0) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", id),
			SentCount:        sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND from_address LIKE ?", like),
			ReceivedCount:    sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND to_address LIKE ?", like),
			DeferredCount:    sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND (from_address LIKE ? OR to_address LIKE ?) AND status = 'deferred'", like, like),
			BouncedCount:     sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND (from_address LIKE ? OR to_address LIKE ?) AND status IN ('bounced','failed')", like, like),
			RejectedCount:    sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND (from_address LIKE ? OR to_address LIKE ?) AND status = 'rejected'", like, like),
			LoginFailures:    sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE email LIKE ? AND event_type = 'failed_login'", like),
			SuspiciousLogins: sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE email LIKE ? AND event_type IN ('security_alert','suspicious_login')", like),
			DNSHealth:        "not_reported",
			SPFStatus:        "not_reported",
			DKIMStatus:       "not_configured",
			DMARCStatus:      "not_configured",
		}
		if dkim != 0 {
			report.DKIMStatus = "enabled"
		}
		if dmarc != 0 {
			report.DMARCStatus = "enabled"
		}
		out = append(out, report)
	}
	return out
}

func totalDomainMetric(rows []adminDomainReport, pick func(adminDomainReport) int64) int64 {
	var n int64
	for _, r := range rows {
		n += pick(r)
	}
	return n
}

func topSenders(db *sql.DB, domains []adminDomainReport) []fiber.Map {
	out := []fiber.Map{}
	if db == nil || len(domains) == 0 {
		return out
	}
	for _, d := range domains {
		rows, err := db.Query("SELECT from_address, COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND from_address LIKE ? GROUP BY from_address ORDER BY COUNT(*) DESC LIMIT 5", "%@"+d.Domain)
		if err != nil {
			continue
		}
		for rows.Next() {
			var sender string
			var count int64
			if err := rows.Scan(&sender, &count); err == nil && sender != "" {
				out = append(out, fiber.Map{"sender": sender, "count": count})
			}
		}
		rows.Close()
	}
	if len(out) > 10 {
		return out[:10]
	}
	return out
}

func topRecipientDomains(db *sql.DB, domains []adminDomainReport) []fiber.Map {
	out := []fiber.Map{}
	if db == nil || len(domains) == 0 {
		return out
	}
	seen := map[string]int64{}
	for _, d := range domains {
		rows, err := db.Query("SELECT to_address FROM coremail_queue WHERE deleted_at IS NULL AND from_address LIKE ? LIMIT 500", "%@"+d.Domain)
		if err != nil {
			continue
		}
		for rows.Next() {
			var recipient string
			if err := rows.Scan(&recipient); err == nil {
				if rd := loginDomain(recipient); rd != "" {
					seen[rd]++
				}
			}
		}
		rows.Close()
	}
	for domain, count := range seen {
		out = append(out, fiber.Map{"domain": domain, "count": count})
	}
	if len(out) > 10 {
		return out[:10]
	}
	return out
}

// AdminReports returns tenant-scoped reporting from existing mail/domain/auth data.
func (h *Handler) AdminReports(c fiber.Ctx) error {
	db := h.sqlDB()
	crossTenant := isSuperRole(c) && strings.EqualFold(c.Query("scope"), "all")
	tenantID := h.scopedTenantID(c)
	domains := domainList(db, tenantID, crossTenant)
	return c.JSON(fiber.Map{
		"scope":                    map[string]any{"tenant_id": tenantID, "cross_tenant": crossTenant},
		"generated_at":             time.Now().UTC(),
		"domains":                  domains,
		"sent_count":               totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.SentCount }),
		"received_count":           totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.ReceivedCount }),
		"deferred_count":           totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.DeferredCount }),
		"bounced_count":            totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.BouncedCount }),
		"rejected_count":           totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.RejectedCount }),
		"login_failures":           totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.LoginFailures }),
		"suspicious_login_count":   totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.SuspiciousLogins }),
		"storage_usage_bytes":      totalDomainMetric(domains, func(r adminDomainReport) int64 { return r.StorageBytes }),
		"top_senders":              topSenders(db, domains),
		"top_recipient_domains":    topRecipientDomains(db, domains),
		"csv_export_available":     false,
		"csv_export_unavailable_reason": "CSV export is not enabled for this report endpoint yet.",
	})
}

// InternalOverview returns cross-tenant platform health for Orvix staff only.
func (h *Handler) InternalOverview(c fiber.Ctx) error {
	db := h.sqlDB()
	return c.JSON(fiber.Map{
		"platform": fiber.Map{
			"status": "ok",
			"generated_at": time.Now().UTC(),
		},
		"tenant_count": sqlInt(db, "SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL"),
		"domains_count": sqlInt(db, "SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL"),
		"mailbox_count": sqlInt(db, "SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL"),
		"queue": fiber.Map{
			"pending": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status IN ('pending','queued')"),
			"deferred": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'deferred'"),
			"failed": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status IN ('failed','bounced','rejected')"),
		},
		"security": fiber.Map{
			"failed_logins": sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE event_type = 'failed_login'"),
			"suspicious": sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE event_type IN ('security_alert','suspicious_login')"),
		},
		"recent_alerts": []fiber.Map{},
		"recent_audit_events": recentAudit(db, 8),
	})
}

func (h *Handler) InternalTenants(c fiber.Ctx) error {
	db := h.sqlDB()
	out := []tenantOpsRow{}
	if db == nil {
		return c.JSON(fiber.Map{"tenants": out})
	}
	rows, err := db.Query("SELECT id, name, slug, domain, COALESCE(plan,''), COALESCE(active,0) FROM tenants WHERE deleted_at IS NULL ORDER BY id ASC")
	if err != nil {
		return c.JSON(fiber.Map{"tenants": out})
	}
	defer rows.Close()
	for rows.Next() {
		var r tenantOpsRow
		var active int
		if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.Domain, &r.Plan, &active); err != nil {
			continue
		}
		r.Active = active != 0
		r.Domains = sqlInt(db, "SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = ? AND deleted_at IS NULL", r.ID)
		r.Mailboxes = sqlInt(db, "SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id = ? AND deleted_at IS NULL", r.ID)
		r.StorageBytes = sqlInt(db, "SELECT COALESCE(SUM(used_bytes),0) FROM coremail_mailboxes WHERE tenant_id = ? AND deleted_at IS NULL", r.ID)
		r.LoginFailures = sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE email LIKE ? AND event_type = 'failed_login'", "%@"+r.Domain)
		r.DeferredCount = sqlInt(db, "SELECT COUNT(*) FROM coremail_queue q JOIN coremail_domains d ON d.name = substr(q.from_address, instr(q.from_address, '@') + 1) WHERE d.tenant_id = ? AND q.deleted_at IS NULL AND q.status = 'deferred'", r.ID)
		r.RejectedCount = sqlInt(db, "SELECT COUNT(*) FROM coremail_queue q JOIN coremail_domains d ON d.name = substr(q.from_address, instr(q.from_address, '@') + 1) WHERE d.tenant_id = ? AND q.deleted_at IS NULL AND q.status IN ('rejected','failed','bounced')", r.ID)
		out = append(out, r)
	}
	return c.JSON(fiber.Map{"tenants": out})
}

func (h *Handler) InternalDomainIntelligence(c fiber.Ctx) error {
	domains := domainList(h.sqlDB(), 0, true)
	return c.JSON(fiber.Map{"domains": domains, "generated_at": time.Now().UTC()})
}

func (h *Handler) InternalSecurityOps(c fiber.Ctx) error {
	db := h.sqlDB()
	lockouts := 0
	if h.trustService != nil {
		lockouts = len(h.trustService.ListLockouts(c.Context()))
	}
	return c.JSON(fiber.Map{
		"failed_auth_total": sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE event_type = 'failed_login'"),
		"suspicious_total": sqlInt(db, "SELECT COUNT(*) FROM security_events WHERE event_type IN ('security_alert','suspicious_login')"),
		"lockouts": lockouts,
		"failed_auth_domains": groupedSecurity(db, "domain"),
		"attacked_accounts": groupedSecurity(db, "account"),
		"timeline": recentSecurity(db, 25),
	})
}

func (h *Handler) InternalMailFlowOps(c fiber.Ctx) error {
	db := h.sqlDB()
	return c.JSON(fiber.Map{
		"queue_depth": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL"),
		"deferred": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'deferred'"),
		"bounces": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status IN ('bounced','failed')"),
		"outbound_errors": sqlInt(db, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND direction = 'outbound' AND status IN ('deferred','failed','bounced','rejected')"),
		"top_queued_domains": topQueuedDomains(db),
		"status_counts": queueStatusCounts(db),
	})
}

func recentAudit(db *sql.DB, limit int) []fiber.Map {
	out := []fiber.Map{}
	if db == nil {
		return out
	}
	rows, err := db.Query("SELECT actor, action, target, result, timestamp FROM coremail_audit ORDER BY timestamp DESC LIMIT ?", limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var actor, action, target, result, ts string
		if err := rows.Scan(&actor, &action, &target, &result, &ts); err == nil {
			out = append(out, fiber.Map{"actor": actor, "action": action, "target": target, "result": result, "timestamp": ts})
		}
	}
	return out
}

func groupedSecurity(db *sql.DB, mode string) []fiber.Map {
	out := []fiber.Map{}
	if db == nil {
		return out
	}
	rows, err := db.Query("SELECT email, COUNT(*) FROM security_events WHERE event_type = 'failed_login' GROUP BY email ORDER BY COUNT(*) DESC LIMIT 20")
	if err != nil {
		return out
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var email string
		var count int64
		if err := rows.Scan(&email, &count); err != nil {
			continue
		}
		key := email
		if mode == "domain" {
			key = loginDomain(email)
		}
		if key == "" {
			key = "unknown"
		}
		counts[key] += count
	}
	for k, count := range counts {
		out = append(out, fiber.Map{"key": k, "count": count})
	}
	return out
}

func recentSecurity(db *sql.DB, limit int) []fiber.Map {
	out := []fiber.Map{}
	if db == nil {
		return out
	}
	rows, err := db.Query("SELECT ip, email, event_type, count, created_at FROM security_events ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ip, email, typ string
		var count int64
		var created string
		if err := rows.Scan(&ip, &email, &typ, &count, &created); err == nil {
			out = append(out, fiber.Map{"ip": ip, "email": email, "event_type": typ, "count": count, "created_at": created})
		}
	}
	return out
}

func queueStatusCounts(db *sql.DB) []queueCount {
	out := []queueCount{}
	if db == nil {
		return out
	}
	rows, err := db.Query("SELECT status, COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL GROUP BY status ORDER BY COUNT(*) DESC")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r queueCount
		if err := rows.Scan(&r.Status, &r.Count); err == nil {
			out = append(out, r)
		}
	}
	return out
}

func topQueuedDomains(db *sql.DB) []fiber.Map {
	out := []fiber.Map{}
	if db == nil {
		return out
	}
	rows, err := db.Query("SELECT recipient_domain, COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND recipient_domain <> '' GROUP BY recipient_domain ORDER BY COUNT(*) DESC LIMIT 10")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var domain string
		var count int64
		if err := rows.Scan(&domain, &count); err == nil {
			out = append(out, fiber.Map{"domain": domain, "count": count})
		}
	}
	return out
}
