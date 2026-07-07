package handlers

// Enterprise admin v2 — accounts / groups / lists / public
// folders / RBAC / quarantine / audit / log rules / ACL.
//
// All endpoints are mounted under /api/v1/admin/* in
// internal/api/router.go (see setupRoutes / setupAdmin).
// Every mutation goes through the CSRF middleware (the
// `men` group). Every read requires admin role. Every
// mutation writes a row to coremail_audit through
// h.appendAudit(...).
//
// The handlers in this file are intentionally narrow: they
// own one table each and do not call into other handlers.
// They DO NOT touch DKIM private keys, password material,
// session tokens, or backup secrets — those are guarded by
// dedicated handlers in dns_ops.go / mailbox_bulk_import.go /
// backups.go and never appear here.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------
// Account Classes
// ---------------------------------------------------------------------

// ListAccountClasses returns all non-deleted account classes
// for the current tenant. The admin UI uses this to populate the
// "Service Class" dropdown when creating a mailbox, and the
// "Account Classes" admin page.
func (h *Handler) ListAccountClasses(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, name, description, default_quota_mb, max_quota_mb,
		       max_send_per_hour, max_recv_per_hour,
		       allow_external_forwarding, allow_imap, allow_pop3,
		       allow_jmap, allow_webmail, is_admin_class,
		       created_at, updated_at
		FROM coremail_account_classes
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list account classes: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                            int64
			name, desc                                     string
			dq, mq, msh, mrh                              int
			aef, aim, apo, ajm, awe, isAdm                int
			created, updated                              time.Time
		)
		if err := rows.Scan(&id, &name, &desc, &dq, &mq, &msh, &mrh, &aef, &aim, &apo, &ajm, &awe, &isAdm, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan account class: %v", err))
		}
		out = append(out, map[string]any{
			"id":                       id,
			"name":                     name,
			"description":              desc,
			"default_quota_mb":         dq,
			"max_quota_mb":             mq,
			"max_send_per_hour":        msh,
			"max_recv_per_hour":        mrh,
			"allow_external_forwarding": aef == 1,
			"allow_imap":               aim == 1,
			"allow_pop3":               apo == 1,
			"allow_jmap":               ajm == 1,
			"allow_webmail":            awe == 1,
			"is_admin_class":           isAdm == 1,
			"created_at":               created.UTC().Format(time.RFC3339),
			"updated_at":               updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"classes": out})
}

// CreateAccountClass creates a new account class. The CSRF +
// admin middleware in router.go enforces that the caller is an
// admin and that a CSRF token was supplied. The new row is
// audited under action=account_class.create.
func (h *Handler) CreateAccountClass(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Name                   string `json:"name"`
		Description            string `json:"description"`
		DefaultQuotaMB         int    `json:"default_quota_mb"`
		MaxQuotaMB             int    `json:"max_quota_mb"`
		MaxSendPerHour         int    `json:"max_send_per_hour"`
		MaxRecvPerHour         int    `json:"max_recv_per_hour"`
		AllowExternalForward   *bool  `json:"allow_external_forwarding"`
		AllowIMAP              *bool  `json:"allow_imap"`
		AllowPOP3              *bool  `json:"allow_pop3"`
		AllowJMAP              *bool  `json:"allow_jmap"`
		AllowWebmail           *bool  `json:"allow_webmail"`
		IsAdminClass           *bool  `json:"is_admin_class"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if body.DefaultQuotaMB <= 0 {
		body.DefaultQuotaMB = 1024
	}
	if body.MaxQuotaMB <= 0 {
		body.MaxQuotaMB = 5120
	}
	if body.MaxSendPerHour <= 0 {
		body.MaxSendPerHour = 500
	}
	if body.MaxRecvPerHour <= 0 {
		body.MaxRecvPerHour = 5000
	}
	aef := 1
	if body.AllowExternalForward != nil && !*body.AllowExternalForward {
		aef = 0
	}
	aim := 1
	if body.AllowIMAP != nil && !*body.AllowIMAP {
		aim = 0
	}
	apo := 1
	if body.AllowPOP3 != nil && !*body.AllowPOP3 {
		apo = 0
	}
	ajm := 1
	if body.AllowJMAP != nil && !*body.AllowJMAP {
		ajm = 0
	}
	awe := 1
	if body.AllowWebmail != nil && !*body.AllowWebmail {
		awe = 0
	}
	isAdm := 0
	if body.IsAdminClass != nil && *body.IsAdminClass {
		isAdm = 1
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_account_classes
		(tenant_id, name, description, default_quota_mb, max_quota_mb,
		 max_send_per_hour, max_recv_per_hour, allow_external_forwarding,
		 allow_imap, allow_pop3, allow_jmap, allow_webmail, is_admin_class,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, body.Name, body.Description, body.DefaultQuotaMB, body.MaxQuotaMB,
		body.MaxSendPerHour, body.MaxRecvPerHour, aef, aim, apo, ajm, awe, isAdm,
		now, now,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "account class name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create account class: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "account_class.create", body.Name, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateAccountClass mutates a single account class row by id.
// The handler refuses to touch protected built-in classes
// (is_admin_class = 1) so the bootstrap admin class cannot
// be weakened by mistake.
func (h *Handler) UpdateAccountClass(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	// Refuse to weaken the admin class — it is the class
	// that admin users get assigned to, so changing its
	// quotas / feature gates silently would let an attacker
	// who gained write access to a class lock themselves
	// into an admin mailbox.
	var isAdminClass int
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT is_admin_class FROM coremail_account_classes
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		id, tenantID).Scan(&isAdminClass); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "account class not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup class: %v", err))
	}
	if isAdminClass == 1 {
		return fiber.NewError(fiber.StatusForbidden, "built-in admin class is read-only")
	}
	var body map[string]any
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	allowed := []string{
		"description", "default_quota_mb", "max_quota_mb",
		"max_send_per_hour", "max_recv_per_hour",
		"allow_external_forwarding", "allow_imap", "allow_pop3",
		"allow_jmap", "allow_webmail",
	}
	sets := []string{}
	args := []any{}
	for _, k := range allowed {
		if v, ok := body[k]; ok {
			sets = append(sets, k+" = ?")
			if b, ok := v.(bool); ok {
				if b {
					args = append(args, 1)
				} else {
					args = append(args, 0)
				}
			} else {
				args = append(args, v)
			}
		}
	}
	if len(sets) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "no mutable fields supplied")
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, id, tenantID)
	q := "UPDATE coremail_account_classes SET " + strings.Join(sets, ", ") +
		" WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL"
	res, err := h.sqlDB().ExecContext(c.Context(), q, args...)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update account class: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "account class not found")
	}
	h.appendAudit(c, "account_class.update", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteAccountClass soft-deletes a class by id. Built-in
// admin classes and any class still referenced by at
// least one active mailbox are refused.
func (h *Handler) DeleteAccountClass(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var isAdminClass int
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT is_admin_class FROM coremail_account_classes
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		id, tenantID).Scan(&isAdminClass); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "account class not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup class: %v", err))
	}
	if isAdminClass == 1 {
		return fiber.NewError(fiber.StatusForbidden, "built-in admin class cannot be deleted")
	}
	var n int
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT COUNT(*) FROM coremail_mailboxes WHERE class_id = ? AND deleted_at IS NULL`,
		id).Scan(&n); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("count mailboxes: %v", err))
	}
	if n > 0 {
		return fiber.NewError(fiber.StatusConflict, fmt.Sprintf("class still in use by %d mailbox(es)", n))
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_account_classes SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete account class: %v", err))
	}
	rn, _ := res.RowsAffected()
	if rn == 0 {
		return fiber.NewError(fiber.StatusNotFound, "account class not found")
	}
	h.appendAudit(c, "account_class.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// EnsureDefaultAccountClasses inserts the three built-in
// classes (standard / shared / admin) if they don't exist
// for the tenant. Idempotent; called once from the bootstrap
// path in api.NewRouter. The router does NOT call this
// directly — it is exposed so tests can wire the same
// defaults. Real bootstrap goes through models.MigrateAllRaw
// (see internal/models/models.go).
func (h *Handler) EnsureDefaultAccountClasses(tenantID int64) error {
	now := time.Now().UTC()
	defaults := []struct {
		name string
		desc string
		dq   int
		mq   int
		msh  int
		mrh  int
		adm  int
	}{
		{"standard", "Default user mailbox", 1024, 5120, 500, 5000, 0},
		{"shared", "Shared mailbox accessed by multiple users", 5120, 51200, 1000, 10000, 0},
		{"admin", "Administrative mailbox with elevated privileges", 2048, 10240, 1000, 10000, 1},
	}
	for _, d := range defaults {
		_, err := h.sqlDB().ExecContext(context.Background(), `
			INSERT OR IGNORE INTO coremail_account_classes
			(tenant_id, name, description, default_quota_mb, max_quota_mb,
			 max_send_per_hour, max_recv_per_hour, allow_external_forwarding,
			 allow_imap, allow_pop3, allow_jmap, allow_webmail, is_admin_class,
			 created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, 1, 1, 1, ?, ?, ?)`,
			tenantID, d.name, d.desc, d.dq, d.mq, d.msh, d.mrh, d.adm, now, now)
		if err != nil {
			return fmt.Errorf("seed default class %q: %w", d.name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Domain Groups
// ---------------------------------------------------------------------

// ListDomainGroups returns all domain groups with their
// member domain names.
func (h *Handler) ListDomainGroups(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, name, description, color, created_at, updated_at
		FROM coremail_domain_groups
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list domain groups: %v", err))
	}
	defer rows.Close()
	groups := []map[string]any{}
	for rows.Next() {
		var (
			id         int64
			name, desc string
			color      string
			created    time.Time
			updated    time.Time
		)
		if err := rows.Scan(&id, &name, &desc, &color, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan domain group: %v", err))
		}
		members, _ := h.fetchDomainGroupMembers(c.Context(), id)
		groups = append(groups, map[string]any{
			"id":          id,
			"name":        name,
			"description": desc,
			"color":       color,
			"members":     members,
			"created_at":  created.UTC().Format(time.RFC3339),
			"updated_at":  updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"groups": groups})
}

func (h *Handler) fetchDomainGroupMembers(ctx context.Context, groupID int64) ([]map[string]any, error) {
	rows, err := h.sqlDB().QueryContext(ctx, `
		SELECT m.domain_id, d.name
		FROM coremail_domain_group_members m
		JOIN coremail_domains d ON d.id = m.domain_id
		WHERE m.group_id = ? AND d.deleted_at IS NULL
		ORDER BY d.name ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var did int64
		var name string
		if err := rows.Scan(&did, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"domain_id": did, "name": name})
	}
	return out, nil
}

// CreateDomainGroup creates a new domain group. The body's
// `domain_ids` array becomes the membership list.
func (h *Handler) CreateDomainGroup(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Color       string  `json:"color"`
		DomainIDs   []int64 `json:"domain_ids"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if body.Color == "" {
		body.Color = "#1f6feb"
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_domain_groups
		(tenant_id, name, description, color, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tenantID, body.Name, body.Description, body.Color, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "domain group already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create domain group: %v", err))
	}
	id, _ := res.LastInsertId()
	for _, did := range body.DomainIDs {
		_, _ = h.sqlDB().ExecContext(c.Context(),
			`INSERT OR IGNORE INTO coremail_domain_group_members (group_id, domain_id, created_at) VALUES (?, ?, ?)`,
			id, did, now)
	}
	h.appendAudit(c, "domain_group.create", body.Name, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateDomainGroupMembers replaces the membership list of a
// domain group. The group itself is read-only here (rename
// happens via PUT /api/v1/admin/domain-groups/:id).
func (h *Handler) UpdateDomainGroupMembers(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body struct {
		DomainIDs []int64 `json:"domain_ids"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	// confirm group belongs to tenant
	var owner int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_domain_groups WHERE id = ? AND deleted_at IS NULL`, id).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "domain group not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup group: %v", err))
	}
	if owner != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant access denied")
	}
	tx, err := h.sqlDB().BeginTx(c.Context(), nil)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("begin tx: %v", err))
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(c.Context(),
		`DELETE FROM coremail_domain_group_members WHERE group_id = ?`, id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete members: %v", err))
	}
	now := time.Now().UTC()
	for _, did := range body.DomainIDs {
		if _, err := tx.ExecContext(c.Context(),
			`INSERT INTO coremail_domain_group_members (group_id, domain_id, created_at) VALUES (?, ?, ?)`,
			id, did, now); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("insert member: %v", err))
		}
	}
	if _, err := tx.ExecContext(c.Context(),
		`UPDATE coremail_domain_groups SET updated_at = ? WHERE id = ?`, now, id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("touch group: %v", err))
	}
	if err := tx.Commit(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("commit: %v", err))
	}
	h.appendAudit(c, "domain_group.update_members", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "members": len(body.DomainIDs)})
}

// DeleteDomainGroup soft-deletes a group and its members.
func (h *Handler) DeleteDomainGroup(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_domain_groups SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete domain group: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "domain group not found")
	}
	_, _ = h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_domain_group_members WHERE group_id = ?`, id)
	h.appendAudit(c, "domain_group.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// ---------------------------------------------------------------------
// Mailing Lists
// ---------------------------------------------------------------------

// ListMailingLists returns all non-deleted mailing lists for
// the caller's tenant, with member counts.
func (h *Handler) ListMailingLists(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT l.id, l.domain_id, l.address, l.display_name, l.description,
		       l.moderation_required, l.archive_enabled, l.subscription_policy,
		       l.max_members, l.status,
		       (SELECT COUNT(*) FROM coremail_mailing_list_members m WHERE m.list_id = l.id) AS member_count,
		       l.created_at, l.updated_at
		FROM coremail_mailing_lists l
		WHERE l.tenant_id = ? AND l.deleted_at IS NULL
		ORDER BY l.address ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list mailing lists: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, domID, memberCount, maxMembers                                            int64
			addr, displayName, desc, subPolicy, status                                    string
			modReq, archive                                                               int
			created, updated                                                              time.Time
		)
		if err := rows.Scan(&id, &domID, &addr, &displayName, &desc, &modReq, &archive, &subPolicy, &maxMembers, &status, &memberCount, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan mailing list: %v", err))
		}
		out = append(out, map[string]any{
			"id":                  id,
			"domain_id":           domID,
			"address":             addr,
			"display_name":        displayName,
			"description":         desc,
			"moderation_required": modReq == 1,
			"archive_enabled":     archive == 1,
			"subscription_policy": subPolicy,
			"max_members":         maxMembers,
			"member_count":        memberCount,
			"status":              status,
			"created_at":          created.UTC().Format(time.RFC3339),
			"updated_at":          updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"lists": out})
}

// CreateMailingList creates a list + an empty membership set.
// The address is validated; the local part is required.
func (h *Handler) CreateMailingList(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		DomainID           int64  `json:"domain_id"`
		Address            string `json:"address"`
		DisplayName        string `json:"display_name"`
		Description        string `json:"description"`
		ModerationRequired bool   `json:"moderation_required"`
		ArchiveEnabled     bool   `json:"archive_enabled"`
		SubscriptionPolicy string `json:"subscription_policy"`
		MaxMembers         int64  `json:"max_members"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Address = strings.TrimSpace(body.Address)
	if body.Address == "" {
		return fiber.NewError(fiber.StatusBadRequest, "address is required")
	}
	if body.DomainID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "domain_id is required")
	}
	if body.SubscriptionPolicy == "" {
		body.SubscriptionPolicy = "closed"
	}
	// confirm domain ownership
	var domTenant int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_domains WHERE id = ? AND deleted_at IS NULL`, body.DomainID).Scan(&domTenant); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "domain not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup domain: %v", err))
	}
	if domTenant != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant domain access denied")
	}
	now := time.Now().UTC()
	mod := 0
	if body.ModerationRequired {
		mod = 1
	}
	arch := 0
	if body.ArchiveEnabled {
		arch = 1
	}
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_mailing_lists
		(tenant_id, domain_id, address, display_name, description,
		 moderation_required, archive_enabled, subscription_policy, max_members,
		 status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`,
		tenantID, body.DomainID, body.Address, body.DisplayName, body.Description,
		mod, arch, body.SubscriptionPolicy, body.MaxMembers, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "mailing list already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create mailing list: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "mailing_list.create", body.Address, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "address": body.Address})
}

// DeleteMailingList soft-deletes a list and drops its members.
func (h *Handler) DeleteMailingList(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_mailing_lists SET deleted_at = ?, updated_at = ?, status = 'deleted'
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete mailing list: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "mailing list not found")
	}
	_, _ = h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_mailing_list_members WHERE list_id = ?`, id)
	h.appendAudit(c, "mailing_list.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// PatchMailingList updates editable fields on a mailing list.
// The address / domain_id are immutable after creation (moving
// a mailing list to another domain would orphan subscribers
// and violate DKIM signing alignment); the editable surface
// is display_name, description, moderation_required,
// archive_enabled, subscription_policy, max_members, status.
// Unknown keys are rejected so a renamed or future field never
// silently drops user input.
func (h *Handler) PatchMailingList(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	allowed := map[string]struct{}{
		"display_name":        {},
		"description":         {},
		"moderation_required": {},
		"archive_enabled":     {},
		"subscription_policy": {},
		"max_members":         {},
		"status":              {},
	}
	rejected := []string{}
	for k := range body {
		if _, ok := allowed[k]; !ok {
			rejected = append(rejected, k)
		}
	}
	if len(rejected) > 0 {
		return fiber.NewError(fiber.StatusBadRequest, "patch contained unknown fields; nothing applied: " + strings.Join(rejected, ","))
	}
	type update struct {
		set  []string
		args []interface{}
	}
	var u update
	for k, raw := range body {
		switch k {
		case "display_name":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(v)
			if len(v) > 256 {
				return fiber.NewError(fiber.StatusBadRequest, "display_name too long (max 256)")
			}
			u.set = append(u.set, "display_name = ?")
			u.args = append(u.args, v)
		case "description":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(v)
			if len(v) > 1024 {
				return fiber.NewError(fiber.StatusBadRequest, "description too long (max 1024)")
			}
			u.set = append(u.set, "description = ?")
			u.args = append(u.args, v)
		case "moderation_required", "archive_enabled":
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid bool")
			}
			val := 0
			if b {
				val = 1
			}
			u.set = append(u.set, k+" = ?")
			u.args = append(u.args, val)
		case "subscription_policy":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(strings.ToLower(v))
			switch v {
			case "open", "closed", "moderated", "announce":
			default:
				return fiber.NewError(fiber.StatusBadRequest, "subscription_policy must be open|closed|moderated|announce")
			}
			u.set = append(u.set, "subscription_policy = ?")
			u.args = append(u.args, v)
		case "max_members":
			var n int64
			if err := json.Unmarshal(raw, &n); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid integer")
			}
			if n < 0 {
				return fiber.NewError(fiber.StatusBadRequest, k+" cannot be negative")
			}
			u.set = append(u.set, "max_members = ?")
			u.args = append(u.args, n)
		case "status":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(strings.ToLower(v))
			switch v {
			case "active", "suspended", "archived":
			default:
				return fiber.NewError(fiber.StatusBadRequest, "status must be active|suspended|archived")
			}
			u.set = append(u.set, "status = ?")
			u.args = append(u.args, v)
		}
	}
	if len(u.set) == 0 {
		return c.JSON(fiber.Map{"applied": []string{}, "id": id})
	}
	u.set = append(u.set, "updated_at = ?")
	u.args = append(u.args, time.Now().UTC())
	u.args = append(u.args, id, tenantID)
	res, err := h.sqlDB().ExecContext(c.Context(),
		"UPDATE coremail_mailing_lists SET "+strings.Join(u.set, ", ")+
			" WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		u.args...)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("failed to update mailing list", zap.Error(err))
		}
		return fiber.NewError(fiber.StatusInternalServerError, "update mailing list failed")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "mailing list not found")
	}
	applied := make([]string, 0, len(body))
	for k := range body {
		applied = append(applied, k)
	}
	h.appendAudit(c, "mailing_list.update", fmt.Sprintf("%d|applied:%d", id, len(applied)), "ok")
	return c.JSON(fiber.Map{"applied": applied, "id": id})
}

// ListMailingListMembers returns the subscriber list of a
// mailing list. The list must belong to the caller's tenant.
func (h *Handler) ListMailingListMembers(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var owner int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_mailing_lists WHERE id = ? AND deleted_at IS NULL`, id).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "mailing list not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup list: %v", err))
	}
	if owner != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant access denied")
	}
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, address, display_name, role, created_at
		FROM coremail_mailing_list_members WHERE list_id = ?
		ORDER BY address ASC`, id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list members: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			mid                  int64
			addr, displayName, rl string
			created              time.Time
		)
		if err := rows.Scan(&mid, &addr, &displayName, &rl, &created); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan member: %v", err))
		}
		out = append(out, map[string]any{
			"id":           mid,
			"address":      addr,
			"display_name": displayName,
			"role":         rl,
			"created_at":   created.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"members": out})
}

// AddMailingListMember appends a subscriber to a list.
func (h *Handler) AddMailingListMember(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	tenantID := h.tenantID(c)
	var owner int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_mailing_lists WHERE id = ? AND deleted_at IS NULL`, id).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "mailing list not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup list: %v", err))
	}
	if owner != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant access denied")
	}
	var body struct {
		Address     string `json:"address"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Address = strings.TrimSpace(body.Address)
	if body.Address == "" {
		return fiber.NewError(fiber.StatusBadRequest, "address is required")
	}
	if body.Role == "" {
		body.Role = "subscriber"
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`INSERT INTO coremail_mailing_list_members (list_id, address, display_name, role, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, body.Address, body.DisplayName, body.Role, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "member already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("insert member: %v", err))
	}
	mid, _ := res.LastInsertId()
	h.appendAudit(c, "mailing_list.member.add", body.Address, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": mid, "address": body.Address})
}

// RemoveMailingListMember removes a single subscriber by id.
func (h *Handler) RemoveMailingListMember(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	listID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid list id")
	}
	memberID, err := strconv.ParseInt(c.Params("memberId"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid member id")
	}
	tenantID := h.tenantID(c)
	var owner int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_mailing_lists WHERE id = ? AND deleted_at IS NULL`, listID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "mailing list not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup list: %v", err))
	}
	if owner != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant access denied")
	}
	res, err := h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_mailing_list_members WHERE id = ? AND list_id = ?`,
		memberID, listID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete member: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "member not found")
	}
	h.appendAudit(c, "mailing_list.member.remove", fmt.Sprintf("%d", memberID), "ok")
	return c.JSON(fiber.Map{"id": memberID, "deleted": true})
}

// ---------------------------------------------------------------------
// Public Folders
// ---------------------------------------------------------------------

// ListPublicFolders returns all public folders with member
// counts.
func (h *Handler) ListPublicFolders(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT p.id, p.owner_mailbox_id, p.folder_path, p.display_name,
		       p.description, p.read_only,
		       (SELECT email FROM coremail_mailboxes m WHERE m.id = p.owner_mailbox_id) AS owner_email,
		       (SELECT COUNT(*) FROM coremail_public_folder_members mem WHERE mem.folder_id = p.id) AS member_count,
		       p.created_at, p.updated_at
		FROM coremail_public_folders p
		WHERE p.tenant_id = ? AND p.deleted_at IS NULL
		ORDER BY p.folder_path ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list public folders: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, ownerMB, memberCount int64
			path, name, desc, owner  string
			readonly                 int
			created, updated         time.Time
		)
		if err := rows.Scan(&id, &ownerMB, &path, &name, &desc, &readonly, &owner, &memberCount, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan public folder: %v", err))
		}
		out = append(out, map[string]any{
			"id":                id,
			"owner_mailbox_id":  ownerMB,
			"owner_email":       owner,
			"folder_path":       path,
			"display_name":      name,
			"description":       desc,
			"read_only":         readonly == 1,
			"member_count":      memberCount,
			"created_at":        created.UTC().Format(time.RFC3339),
			"updated_at":        updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"folders": out})
}

// CreatePublicFolder registers a folder on an existing
// mailbox as a public folder.
func (h *Handler) CreatePublicFolder(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		OwnerMailboxID int64  `json:"owner_mailbox_id"`
		FolderPath     string `json:"folder_path"`
		DisplayName    string `json:"display_name"`
		Description    string `json:"description"`
		ReadOnly       bool   `json:"read_only"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.FolderPath = strings.TrimSpace(body.FolderPath)
	if body.OwnerMailboxID == 0 || body.FolderPath == "" {
		return fiber.NewError(fiber.StatusBadRequest, "owner_mailbox_id and folder_path are required")
	}
	var ownerTenant int64
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT tenant_id FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL`, body.OwnerMailboxID).Scan(&ownerTenant); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "owner mailbox not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup mailbox: %v", err))
	}
	if ownerTenant != tenantID {
		return fiber.NewError(fiber.StatusForbidden, "cross-tenant mailbox access denied")
	}
	now := time.Now().UTC()
	ro := 0
	if body.ReadOnly {
		ro = 1
	}
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_public_folders
		(tenant_id, owner_mailbox_id, folder_path, display_name, description,
		 read_only, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, body.OwnerMailboxID, body.FolderPath, body.DisplayName, body.Description,
		ro, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "public folder already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create public folder: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "public_folder.create", body.FolderPath, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "folder_path": body.FolderPath})
}

// DeletePublicFolder soft-deletes a public folder and drops
// its member grants.
func (h *Handler) DeletePublicFolder(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_public_folders SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete public folder: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "public folder not found")
	}
	_, _ = h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_public_folder_members WHERE folder_id = ?`, id)
	h.appendAudit(c, "public_folder.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// PatchPublicFolder updates editable fields on a public folder.
// The owner_mailbox_id and folder_path are immutable (changing
// the owner would orphan subscriptions; renaming the path would
// break every IMAP client subscribed to it). Editable: display_name,
// description, read_only.
func (h *Handler) PatchPublicFolder(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	allowed := map[string]struct{}{
		"display_name": {},
		"description":  {},
		"read_only":    {},
	}
	rejected := []string{}
	for k := range body {
		if _, ok := allowed[k]; !ok {
			rejected = append(rejected, k)
		}
	}
	if len(rejected) > 0 {
		return fiber.NewError(fiber.StatusBadRequest, "patch contained unknown fields; nothing applied: "+strings.Join(rejected, ","))
	}
	type update struct {
		set  []string
		args []interface{}
	}
	var u update
	for k, raw := range body {
		switch k {
		case "display_name":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(v)
			if len(v) > 256 {
				return fiber.NewError(fiber.StatusBadRequest, "display_name too long (max 256)")
			}
			u.set = append(u.set, "display_name = ?")
			u.args = append(u.args, v)
		case "description":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid string")
			}
			v = strings.TrimSpace(v)
			if len(v) > 1024 {
				return fiber.NewError(fiber.StatusBadRequest, "description too long (max 1024)")
			}
			u.set = append(u.set, "description = ?")
			u.args = append(u.args, v)
		case "read_only":
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, k+": invalid bool")
			}
			val := 0
			if b {
				val = 1
			}
			u.set = append(u.set, "read_only = ?")
			u.args = append(u.args, val)
		}
	}
	if len(u.set) == 0 {
		return c.JSON(fiber.Map{"applied": []string{}, "id": id})
	}
	u.set = append(u.set, "updated_at = ?")
	u.args = append(u.args, time.Now().UTC())
	u.args = append(u.args, id, tenantID)
	res, err := h.sqlDB().ExecContext(c.Context(),
		"UPDATE coremail_public_folders SET "+strings.Join(u.set, ", ")+
			" WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		u.args...)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("failed to update public folder", zap.Error(err))
		}
		return fiber.NewError(fiber.StatusInternalServerError, "update public folder failed")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "public folder not found")
	}
	applied := make([]string, 0, len(body))
	for k := range body {
		applied = append(applied, k)
	}
	h.appendAudit(c, "public_folder.update", fmt.Sprintf("%d|applied:%d", id, len(applied)), "ok")
	return c.JSON(fiber.Map{"applied": applied, "id": id})
}

// ---------------------------------------------------------------------
// Admin Groups (RBAC)
// ---------------------------------------------------------------------

// ListAdminGroups returns all RBAC groups with their grants
// and member counts.
func (h *Handler) ListAdminGroups(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, name, description, grants,
		       (SELECT COUNT(*) FROM coremail_admin_group_members m WHERE m.group_id = g.id) AS member_count,
		       created_at, updated_at
		FROM coremail_admin_groups g
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list admin groups: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, memberCount       int64
			name, desc, grantsRaw  string
			created, updated      time.Time
		)
		if err := rows.Scan(&id, &name, &desc, &grantsRaw, &memberCount, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan admin group: %v", err))
		}
		grants := splitGrants(grantsRaw)
		out = append(out, map[string]any{
			"id":           id,
			"name":         name,
			"description":  desc,
			"grants":       grants,
			"grants_raw":   grantsRaw,
			"member_count": memberCount,
			"created_at":   created.UTC().Format(time.RFC3339),
			"updated_at":   updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"groups": out})
}

// splitGrants splits a comma-separated grants string,
// trimming whitespace and dropping empty entries.
func splitGrants(s string) []string {
	out := []string{}
	for _, g := range strings.Split(s, ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		out = append(out, g)
	}
	return out
}

// joinGrants is the inverse of splitGrants.
func joinGrants(gs []string) string {
	clean := make([]string, 0, len(gs))
	for _, g := range gs {
		g = strings.TrimSpace(g)
		if g != "" {
			clean = append(clean, g)
		}
	}
	return strings.Join(clean, ",")
}

// CreateAdminGroup creates a new RBAC group.
func (h *Handler) CreateAdminGroup(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Grants      []string `json:"grants"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_admin_groups (tenant_id, name, description, grants, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tenantID, body.Name, body.Description, joinGrants(body.Grants), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "admin group already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create admin group: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "admin_group.create", body.Name, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateAdminGroup updates a group's grants / description.
// Renaming is intentionally not supported here to keep audit
// trails stable. Built-in groups (super_admin / domain_admin /
// read_only) are flagged with name prefix `builtin.` and are
// read-only.
func (h *Handler) UpdateAdminGroup(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var name string
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT name FROM coremail_admin_groups
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		id, tenantID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "admin group not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup group: %v", err))
	}
	if strings.HasPrefix(name, "builtin.") {
		return fiber.NewError(fiber.StatusForbidden, "built-in admin groups are read-only")
	}
	var body map[string]any
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	sets := []string{}
	args := []any{}
	if v, ok := body["description"].(string); ok {
		sets = append(sets, "description = ?")
		args = append(args, v)
	}
	if v, ok := body["grants"]; ok {
		if arr, ok := v.([]any); ok {
			gs := make([]string, 0, len(arr))
			for _, e := range arr {
				if s, ok := e.(string); ok {
					gs = append(gs, s)
				}
			}
			sets = append(sets, "grants = ?")
			args = append(args, joinGrants(gs))
		}
	}
	if len(sets) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "no mutable fields supplied")
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, id, tenantID)
	q := "UPDATE coremail_admin_groups SET " + strings.Join(sets, ", ") +
		" WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL"
	res, err := h.sqlDB().ExecContext(c.Context(), q, args...)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update admin group: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin group not found")
	}
	h.appendAudit(c, "admin_group.update", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteAdminGroup soft-deletes an admin group. Built-in
// groups (name prefix `builtin.`) cannot be deleted.
func (h *Handler) DeleteAdminGroup(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var name string
	if err := h.sqlDB().QueryRowContext(c.Context(),
		`SELECT name FROM coremail_admin_groups
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		id, tenantID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "admin group not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup group: %v", err))
	}
	if strings.HasPrefix(name, "builtin.") {
		return fiber.NewError(fiber.StatusForbidden, "built-in admin groups cannot be deleted")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_admin_groups SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete admin group: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin group not found")
	}
	_, _ = h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_admin_group_members WHERE group_id = ?`, id)
	h.appendAudit(c, "admin_group.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// ListAdminGroupMembers returns the user ids / emails that
// are members of the given admin group.
func (h *Handler) ListAdminGroupMembers(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT m.user_id, u.email, u.role, m.created_at
		FROM coremail_admin_group_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.group_id = ? AND u.deleted_at IS NULL
		ORDER BY u.email ASC`, id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list members: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			uid                                  int64
			email, role                          string
			created                              time.Time
		)
		if err := rows.Scan(&uid, &email, &role, &created); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan member: %v", err))
		}
		out = append(out, map[string]any{
			"user_id":    uid,
			"email":      email,
			"role":       role,
			"created_at": created.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"members": out})
}

// AddAdminGroupMember adds a user to an admin group.
func (h *Handler) AddAdminGroupMember(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if body.UserID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "user_id is required")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`INSERT OR IGNORE INTO coremail_admin_group_members (group_id, user_id, created_at) VALUES (?, ?, ?)`,
		id, body.UserID, now)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("insert member: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusConflict, "user is already a member of this group")
	}
	h.appendAudit(c, "admin_group.member.add", fmt.Sprintf("%d/%d", id, body.UserID), "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"group_id": id, "user_id": body.UserID})
}

// RemoveAdminGroupMember removes a user from an admin group.
func (h *Handler) RemoveAdminGroupMember(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid group id")
	}
	uid, err := strconv.ParseInt(c.Params("userId"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}
	res, err := h.sqlDB().ExecContext(c.Context(),
		`DELETE FROM coremail_admin_group_members WHERE group_id = ? AND user_id = ?`,
		id, uid)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete member: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "member not found")
	}
	h.appendAudit(c, "admin_group.member.remove", fmt.Sprintf("%d/%d", id, uid), "ok")
	return c.JSON(fiber.Map{"group_id": id, "user_id": uid, "deleted": true})
}

// ---------------------------------------------------------------------
// Audit Log
// ---------------------------------------------------------------------

// ListAuditLogs returns the most recent rows from
// coremail_audit. Filterable by actor and action.
func (h *Handler) ListAdminAuditLogs(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	limit := int64(200)
	if v, err := strconv.ParseInt(c.Query("limit"), 10, 64); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	actor := strings.TrimSpace(c.Query("actor"))
	action := strings.TrimSpace(c.Query("action"))
	where := []string{"1=1"}
	args := []any{}
	if actor != "" {
		where = append(where, "actor LIKE ?")
		args = append(args, actor+"%")
	}
	if action != "" {
		where = append(where, "action = ?")
		args = append(args, action)
	}
	args = append(args, limit)
	q := `SELECT id, actor, role, action, target, result, ip, user_agent, timestamp
	      FROM coremail_audit WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY id DESC LIMIT ?`
	rows, err := h.sqlDB().QueryContext(c.Context(), q, args...)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list audit logs: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                              int64
			actor, role, action, target, result, ip, ua     string
			ts                                              time.Time
		)
		if err := rows.Scan(&id, &actor, &role, &action, &target, &result, &ip, &ua, &ts); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan audit row: %v", err))
		}
		out = append(out, map[string]any{
			"id":         id,
			"actor":      actor,
			"role":       role,
			"action":     action,
			"target":     target,
			"result":     result,
			"ip":         ip,
			"user_agent": ua,
			"timestamp":  ts.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"logs": out})
}

// ---------------------------------------------------------------------
// Quarantine
// ---------------------------------------------------------------------

// ListQuarantine returns quarantined messages with optional
// status / severity filters. Status values: held / released /
// deleted.
func (h *Handler) ListQuarantine(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = "held"
	}
	where := []string{"tenant_id = ?", "status = ?"}
	args := []any{tenantID, status}
	if sev := strings.TrimSpace(c.Query("severity")); sev != "" {
		where = append(where, "severity = ?")
		args = append(args, sev)
	}
	limit := int64(200)
	if v, err := strconv.ParseInt(c.Query("limit"), 10, 64); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	args = append(args, limit)
	q := `SELECT id, message_id, recipient, sender, subject, reason, severity, status,
	             resolved_at, resolved_by, created_at
	      FROM coremail_quarantine_index WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY id DESC LIMIT ?`
	rows, err := h.sqlDB().QueryContext(c.Context(), q, args...)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list quarantine: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                              int64
			mid                                             string
			recipient, sender, subject, reason, severity    string
			status, resolvedBy                              string
			resolvedAt                                      sql.NullTime
			created                                         time.Time
		)
		if err := rows.Scan(&id, &mid, &recipient, &sender, &subject, &reason, &severity, &status, &resolvedAt, &resolvedBy, &created); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan quarantine: %v", err))
		}
		resolved := ""
		if resolvedAt.Valid {
			resolved = resolvedAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"id":          id,
			"message_id":  mid,
			"recipient":   recipient,
			"sender":      sender,
			"subject":     subject,
			"reason":      reason,
			"severity":    severity,
			"status":      status,
			"resolved_at": resolved,
			"resolved_by": resolvedBy,
			"created_at":  created.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"quarantine": out})
}

// ResolveQuarantine updates the status of a quarantined
// message (release / delete). The action is audited.
func (h *Handler) ResolveQuarantine(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body struct {
		Action string `json:"action"` // "release" or "delete"
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	newStatus := ""
	switch body.Action {
	case "release":
		newStatus = "released"
	case "delete":
		newStatus = "deleted"
	default:
		return fiber.NewError(fiber.StatusBadRequest, "action must be 'release' or 'delete'")
	}
	actor, _ := c.Locals("user_email").(string)
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_quarantine_index
		 SET status = ?, resolved_at = ?, resolved_by = ?
		 WHERE id = ? AND tenant_id = ? AND status = 'held'`,
		newStatus, now, actor, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("resolve quarantine: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "quarantined message not found or already resolved")
	}
	h.appendAudit(c, "quarantine."+body.Action, fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "status": newStatus})
}

// ---------------------------------------------------------------------
// ACL Rules (Global Access Control)
// ---------------------------------------------------------------------

// ListACLRules returns the per-tenant ACL rules. The ACL
// layer enforces these in the SMTP / IMAP / POP3 listeners
// via internal/auth/acl.go (added in this commit).
func (h *Handler) ListACLRules(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, scope, scope_target, action, protocol, source, priority, note,
		       created_at, updated_at
		FROM coremail_acl_rules
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY priority ASC, id ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list acl: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                  int64
			scope, target, action, protocol, src, note string
			priority                            int
			created, updated                    time.Time
		)
		if err := rows.Scan(&id, &scope, &target, &action, &protocol, &src, &priority, &note, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan acl: %v", err))
		}
		out = append(out, map[string]any{
			"id":           id,
			"scope":        scope,
			"scope_target": target,
			"action":       action,
			"protocol":     protocol,
			"source":       src,
			"priority":     priority,
			"note":         note,
			"created_at":   created.UTC().Format(time.RFC3339),
			"updated_at":   updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"rules": out})
}

// CreateACLRule creates a new ACL rule. The source is a CIDR
// or single IP; the action is allow/deny; the protocol is
// smtp/imap/pop3/jmap/webmail/all.
func (h *Handler) CreateACLRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Scope       string `json:"scope"`
		ScopeTarget string `json:"scope_target"`
		Action      string `json:"action"`
		Protocol    string `json:"protocol"`
		Source      string `json:"source"`
		Priority    int    `json:"priority"`
		Note        string `json:"note"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Action = strings.ToLower(strings.TrimSpace(body.Action))
	body.Protocol = strings.ToLower(strings.TrimSpace(body.Protocol))
	body.Scope = strings.ToLower(strings.TrimSpace(body.Scope))
	body.Source = strings.TrimSpace(body.Source)
	if body.Action != "allow" && body.Action != "deny" {
		return fiber.NewError(fiber.StatusBadRequest, "action must be 'allow' or 'deny'")
	}
	if body.Protocol == "" {
		body.Protocol = "all"
	}
	if body.Source == "" {
		return fiber.NewError(fiber.StatusBadRequest, "source is required")
	}
	if body.Priority == 0 {
		body.Priority = 100
	}
	if body.Scope == "" {
		body.Scope = "global"
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_acl_rules
		(tenant_id, scope, scope_target, action, protocol, source, priority, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, body.Scope, body.ScopeTarget, body.Action, body.Protocol, body.Source, body.Priority, body.Note, now, now)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create acl: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "acl.create", body.Source+" "+body.Action, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

// DeleteACLRule soft-deletes an ACL rule by id.
func (h *Handler) DeleteACLRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_acl_rules SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete acl: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "acl rule not found")
	}
	h.appendAudit(c, "acl.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// ---------------------------------------------------------------------
// Log Collection Rules
// ---------------------------------------------------------------------

// ListLogRules returns the per-tenant log collection rules.
// The collector reads them on a 30s tick and ships matching
// lines to the configured destination.
func (h *Handler) ListLogRules(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), `
		SELECT id, name, source, severity, match_pattern, destination, enabled,
		       created_at, updated_at
		FROM coremail_log_rules
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list log rules: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                  int64
			name, source, severity, pattern, dest string
			enabled                             int
			created, updated                    time.Time
		)
		if err := rows.Scan(&id, &name, &source, &severity, &pattern, &dest, &enabled, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan log rule: %v", err))
		}
		out = append(out, map[string]any{
			"id":            id,
			"name":          name,
			"source":        source,
			"severity":      severity,
			"match_pattern": pattern,
			"destination":   dest,
			"enabled":       enabled == 1,
			"created_at":    created.UTC().Format(time.RFC3339),
			"updated_at":    updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"rules": out})
}

// CreateLogRule creates a log collection rule.
func (h *Handler) CreateLogRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Name         string `json:"name"`
		Source       string `json:"source"`
		Severity     string `json:"severity"`
		MatchPattern string `json:"match_pattern"`
		Destination  string `json:"destination"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if body.Source == "" {
		body.Source = "journald"
	}
	if body.Severity == "" {
		body.Severity = "info"
	}
	en := 0
	if body.Enabled {
		en = 1
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), `
		INSERT INTO coremail_log_rules
		(tenant_id, name, source, severity, match_pattern, destination, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, body.Name, body.Source, body.Severity, body.MatchPattern, body.Destination, en, now, now)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create log rule: %v", err))
	}
	id, _ := res.LastInsertId()
	h.appendAudit(c, "log_rule.create", body.Name, "ok")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

// DeleteLogRule soft-deletes a log rule.
func (h *Handler) DeleteLogRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_log_rules SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete log rule: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "log rule not found")
	}
	h.appendAudit(c, "log_rule.delete", fmt.Sprintf("%d", id), "ok")
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// requireDB is a tiny guard that returns 503 if the handler
// was constructed without a DB. Used by every handler in this
// file so we don't need to repeat the same nil check.
func (h *Handler) requireDB(c fiber.Ctx) error {
	if h.db == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "database not available")
	}
	return nil
}

// sqlDB returns the underlying *sql.DB. We accept the gorm.DB
// for compatibility with the rest of the codebase but every
// handler in this file speaks SQL directly so we don't pull in
// gorm model types for tables that don't have gorm models yet.
func (h *Handler) sqlDB() *sql.DB {
	if h.db == nil {
		return nil
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil
	}
	return sqlDB
}

// tenantID reads the caller's tenant from the auth locals
// populated by the tenant middleware. Defaults to 1 in
// single-tenant dev installs so the handlers never panic on
// a missing locals key.
func (h *Handler) tenantID(c fiber.Ctx) int64 {
	if v, ok := c.Locals("tenant_id").(uint); ok && v > 0 {
		return int64(v)
	}
	return 1
}

// appendAudit records an admin mutation to coremail_audit. The
// caller passes a verb-noun action ("acl.create") and a
// target string. The handler reads actor / role / ip from the
// auth locals. Errors are logged but never returned to the
// caller — auditing must not block business writes.
//
// Actor format matches the existing handlers.go
// writeAuditLog convention: "user:<id>" so admin / user rows
// are indistinguishable in queries.
func (h *Handler) appendAudit(c fiber.Ctx, action, target, result string) {
	if h.auditStore == nil {
		return
	}
	uid, _ := c.Locals("user_id").(uint)
	actor := fmt.Sprintf("user:%d", uid)
	ip := c.IP()
	ua := string(c.Request().Header.UserAgent())
	if err := h.auditStore.Record(c.Context(), &audit.Entry{
		Actor:     actor,
		Action:    action,
		Target:    target,
		Result:    result,
		IP:        ip,
		UserAgent: ua,
	}); err != nil {
		if h.logger != nil {
			h.logger.Error("failed to write audit log", zap.Error(err))
		}
	}
}
