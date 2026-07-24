package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
)

func (h *Handler) ListAliases(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	rows, err := db.QueryContext(c.Context(),
		`SELECT id, domain_id, from_addr, to_addr, active, created_at FROM coremail_aliases WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY from_addr`, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list aliases"})
	}
	defer rows.Close()
	type Alias struct {
		ID        uint   `json:"id"`
		DomainID  uint   `json:"domain_id"`
		FromAddr  string `json:"from_addr"`
		ToAddr    string `json:"to_addr"`
		Active    bool   `json:"active"`
		CreatedAt string `json:"created_at"`
	}
	var aliases []Alias
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.ID, &a.DomainID, &a.FromAddr, &a.ToAddr, &a.Active, &a.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "scan error"})
		}
		aliases = append(aliases, a)
	}
	return c.JSON(aliases)
}

func requireRBAC(c fiber.Ctx, perm authrbac.Permission) error {
	role, ok := c.Locals("role").(auth.Role)
	if !ok {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "no role on request"})
	}
	if !authrbac.HasPermission(role, perm) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient permissions"})
	}
	return nil
}

func (h *Handler) CreateAlias(c fiber.Ctx) error {
	var req struct {
		DomainID uint   `json:"domain_id"`
		FromAddr string `json:"from_addr"`
		ToAddr   string `json:"to_addr"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.FromAddr == "" || req.ToAddr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "from_addr and to_addr are required"})
	}
	if err := requireRBAC(c, authrbac.PermAliasesWrite); err != nil {
		return err
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(c.Context(),
		`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`,
		req.DomainID, tenantID, req.FromAddr, req.ToAddr, now, now)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "failed to create alias: " + err.Error()})
	}
	h.writeAuditLog(c, "alias.create", "from:"+req.FromAddr+" to:"+req.ToAddr)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "created"})
}

func (h *Handler) DeleteAlias(c fiber.Ctx) error {
	if err := requireRBAC(c, authrbac.PermAliasesWrite); err != nil {
		return err
	}
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid alias id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	now := time.Now().UTC()
	result, err := db.ExecContext(c.Context(),
		"UPDATE coremail_aliases SET deleted_at = "+h.dialect.Placeholder(1)+" WHERE id = "+h.dialect.Placeholder(2)+" AND tenant_id = "+h.dialect.Placeholder(3)+" AND deleted_at IS NULL", now, id, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete alias"})
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "alias not found"})
	}
	h.writeAuditLog(c, "alias.delete", "id:"+idStr)
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListGroups(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	rows, err := db.QueryContext(c.Context(),
		`SELECT id, name, description, created_at FROM coremail_groups WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY name`, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list groups"})
	}
	defer rows.Close()
	type Group struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
	}
	groups := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "scan error"})
		}
		groups = append(groups, g)
	}
	return c.JSON(groups)
}

func (h *Handler) CreateGroup(c fiber.Ctx) error {
	if err := requireRBAC(c, authrbac.PermGroupsWrite); err != nil {
		return err
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(c.Context(),
		`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		tenantID, req.Name, req.Description, now, now)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "failed to create group: " + err.Error()})
	}
	h.writeAuditLog(c, "group.create", "name:"+req.Name)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "created"})
}

func (h *Handler) DeleteGroup(c fiber.Ctx) error {
	if err := requireRBAC(c, authrbac.PermGroupsWrite); err != nil {
		return err
	}
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid group id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	now := time.Now().UTC()
	result, err := db.ExecContext(c.Context(),
		"UPDATE coremail_groups SET deleted_at = "+h.dialect.Placeholder(1)+" WHERE id = "+h.dialect.Placeholder(2)+" AND tenant_id = "+h.dialect.Placeholder(3)+" AND deleted_at IS NULL", now, id, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete group"})
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "group not found"})
	}
	h.writeAuditLog(c, "group.delete", "id:"+idStr)
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) AddGroupMember(c fiber.Ctx) error {
	if err := requireRBAC(c, authrbac.PermGroupsWrite); err != nil {
		return err
	}
	groupIDStr := c.Params("id")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid group id"})
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	var groupTenantID uint
	err = db.QueryRowContext(c.Context(),
		"SELECT tenant_id FROM coremail_groups WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", groupID).Scan(&groupTenantID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "group not found"})
	}
	if groupTenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "group not found"})
	}
	if req.Email != "" {
		var mailboxTenantID uint
		err = db.QueryRowContext(c.Context(),
			"SELECT tenant_id FROM coremail_mailboxes WHERE email = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", req.Email).Scan(&mailboxTenantID)
		if err == nil && mailboxTenantID != tenantID {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
		}
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(c.Context(),
		`INSERT INTO coremail_group_members (group_id, email, added_at) VALUES (`+h.dialect.Placeholder(1)+`, `+h.dialect.Placeholder(2)+`, `+h.dialect.Placeholder(3)+`)`, groupID, req.Email, now)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "failed to add member: " + err.Error()})
	}
	h.writeAuditLog(c, "group.member.add", "group_id:"+groupIDStr+" email:"+req.Email)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "added"})
}

func (h *Handler) RemoveGroupMember(c fiber.Ctx) error {
	if err := requireRBAC(c, authrbac.PermGroupsWrite); err != nil {
		return err
	}
	groupIDStr := c.Params("id")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid group id"})
	}
	memberIDStr := c.Params("memberId")
	memberID, err := strconv.ParseUint(memberIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid member id"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	db, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database unavailable"})
	}
	result, err := db.ExecContext(c.Context(),
		"DELETE FROM coremail_group_members WHERE id = "+h.dialect.Placeholder(1)+" AND group_id IN (SELECT id FROM coremail_groups WHERE id = "+h.dialect.Placeholder(2)+" AND tenant_id = "+h.dialect.Placeholder(3)+" AND deleted_at IS NULL)",
		memberID, groupID, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to remove member"})
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "member not found"})
	}
	h.writeAuditLog(c, "group.member.remove", "group_id:"+groupIDStr+" member_id:"+memberIDStr)
	return c.JSON(fiber.Map{"status": "removed"})
}
