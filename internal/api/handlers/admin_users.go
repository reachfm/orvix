package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// ListAdminUsers returns admin/staff users for the current tenant.
func (h *Handler) ListAdminUsers(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	rows, err := sqlDB.QueryContext(c.Context(), `
		SELECT id, email, role, active, email_verified, created_at, updated_at
		FROM users
		WHERE tenant_id = ? AND role IN ('admin','superadmin') AND deleted_at IS NULL
		ORDER BY email ASC`, tenantID)
	if err != nil {
		h.logger.Error("list admin users: DB query failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "admin users temporarily unavailable")
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id uint64
		var email, role string
		var active, emailVerified bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &email, &role, &active, &emailVerified, &createdAt, &updatedAt); err != nil {
			h.logger.Error("list admin users: scan failed", zap.Error(err))
			return fiber.NewError(fiber.StatusInternalServerError, "admin users temporarily unavailable")
		}
		entry := map[string]any{
			"id":             id,
			"email":          email,
			"role":           role,
			"active":         active,
			"email_verified": emailVerified,
			"created_at":     createdAt.UTC().Format(time.RFC3339),
			"updated_at":     updatedAt.UTC().Format(time.RFC3339),
		}
		out = append(out, entry)
	}
	return c.JSON(fiber.Map{"users": out})
}

// CreateAdminUser creates a new admin/staff user.
func (h *Handler) CreateAdminUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Role = strings.TrimSpace(strings.ToLower(body.Role))

	if body.Email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email is required")
	}
	if body.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "password is required")
	}
	if len(body.Password) < 8 {
		return fiber.NewError(fiber.StatusBadRequest, "password must be at least 8 characters")
	}
	if body.Role == "" {
		body.Role = "admin"
	}
	if body.Role != "admin" && body.Role != "superadmin" {
		return fiber.NewError(fiber.StatusBadRequest, "role must be 'admin' or 'superadmin'")
	}

	hash, err := h.auth.HashPassword(body.Password)
	if err != nil {
		h.logger.Error("hash password", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}

	now := time.Now().UTC()
	res, err := sqlDB.ExecContext(c.Context(), `
		INSERT INTO users (tenant_id, email, password_hash, role, active, email_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 1, ?, ?)`,
		tenantID, body.Email, hash, body.Role, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "admin user with this email already exists")
		}
		h.logger.Error("create admin user: DB insert failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	id, _ := res.LastInsertId()
	h.writeAuditLog(c, "admin_user.create", body.Email)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "email": body.Email, "role": body.Role})
}

// GetAdminUser returns a single admin/staff user.
func (h *Handler) GetAdminUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var id uint64
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	var email, role string
	var active, emailVerified bool
	var createdAt, updatedAt time.Time
	err := sqlDB.QueryRowContext(c.Context(), `
		SELECT id, email, role, active, email_verified, created_at, updated_at
		FROM users WHERE id = ? AND tenant_id = ? AND role IN ('admin','superadmin') AND deleted_at IS NULL`,
		id, tenantID).Scan(&id, &email, &role, &active, &emailVerified, &createdAt, &updatedAt)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}

	return c.JSON(fiber.Map{
		"id":             id,
		"email":          email,
		"role":           role,
		"active":         active,
		"email_verified": emailVerified,
		"created_at":     createdAt.UTC().Format(time.RFC3339),
		"updated_at":     updatedAt.UTC().Format(time.RFC3339),
	})
}

// UpdateAdminUser updates an admin user's role/email.
func (h *Handler) UpdateAdminUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var id uint64
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}

	if body.Role != "" && body.Role != "admin" && body.Role != "superadmin" {
		return fiber.NewError(fiber.StatusBadRequest, "role must be 'admin' or 'superadmin'")
	}

	now := time.Now().UTC()
	var sets []string
	var args []any
	if body.Email != "" {
		sets = append(sets, "email = ?")
		args = append(args, strings.TrimSpace(strings.ToLower(body.Email)))
	}
	if body.Role != "" {
		sets = append(sets, "role = ?")
		args = append(args, body.Role)
	}
	if len(sets) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "no fields to update")
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, now, id, tenantID)

	res, err := sqlDB.ExecContext(c.Context(), fmt.Sprintf(
		`UPDATE users SET %s WHERE id = ? AND tenant_id = ? AND role IN ('admin','superadmin') AND deleted_at IS NULL`,
		strings.Join(sets, ", ")), args...)
	if err != nil {
		h.logger.Error("update admin user: DB update failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}
	h.writeAuditLog(c, "admin_user.update", fmt.Sprintf("user_id:%d", id))
	return c.JSON(fiber.Map{"result": "updated", "id": id})
}

// UpdateAdminUserPassword resets an admin user's password.
func (h *Handler) UpdateAdminUserPassword(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var id uint64
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if body.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "password is required")
	}
	if len(body.Password) < 8 {
		return fiber.NewError(fiber.StatusBadRequest, "password must be at least 8 characters")
	}

	hash, err := h.auth.HashPassword(body.Password)
	if err != nil {
		h.logger.Error("hash password", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}

	now := time.Now().UTC()
	res, err := sqlDB.ExecContext(c.Context(),
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND tenant_id = ? AND role IN ('admin','superadmin') AND deleted_at IS NULL`,
		hash, now, id, tenantID)
	if err != nil {
		h.logger.Error("reset password: DB update failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}
	h.writeAuditLog(c, "admin_user.password_reset", fmt.Sprintf("user_id:%d", id))
	return c.JSON(fiber.Map{"result": "password_updated", "id": id})
}

// UpdateAdminUserStatus enables or disables an admin user.
func (h *Handler) UpdateAdminUserStatus(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var id uint64
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	// Prevent disabling self
	currentUserID, _ := c.Locals("user_id").(uint)
	if uint(id) == currentUserID {
		return fiber.NewError(fiber.StatusConflict, "cannot disable your own admin account")
	}

	var body struct {
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}

	// Prevent disabling the last active superadmin
	if !body.Active {
		var superadminCount int
		sqlDB.QueryRowContext(c.Context(),
			`SELECT COUNT(*) FROM users WHERE tenant_id = `+h.dialect.Placeholder(1)+` AND role = 'superadmin' AND active = `+h.dialect.TrueLiteral()+` AND deleted_at IS NULL`,
			tenantID).Scan(&superadminCount)
		if superadminCount <= 1 {
			var isSuperadmin bool
			sqlDB.QueryRowContext(c.Context(),
				`SELECT role = 'superadmin' FROM users WHERE id = `+h.dialect.Placeholder(1)+` AND tenant_id = `+h.dialect.Placeholder(2)+` AND deleted_at IS NULL`,
				id, tenantID).Scan(&isSuperadmin)
			if isSuperadmin {
				return fiber.NewError(fiber.StatusForbidden, "cannot disable the last active superadmin")
			}
		}
	}

	now := time.Now().UTC()
	res, err := sqlDB.ExecContext(c.Context(),
		`UPDATE users SET active = `+h.dialect.Placeholder(1)+`, updated_at = `+h.dialect.Placeholder(2)+` WHERE id = `+h.dialect.Placeholder(3)+` AND tenant_id = `+h.dialect.Placeholder(4)+` AND role IN ('admin','superadmin') AND deleted_at IS NULL`,
		body.Active, now, id, tenantID)
	if err != nil {
		h.logger.Error("update admin user status: DB update failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}
	h.writeAuditLog(c, "admin_user.status_update", fmt.Sprintf("user_id:%d|active:%v", id, body.Active))
	return c.JSON(fiber.Map{"result": "updated", "id": id, "active": body.Active})
}

// UpdateAdminUserGroups updates the admin groups for a user.
// Uses the admin_group_members table to set group membership.
func (h *Handler) UpdateAdminUserGroups(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var userID uint64
	fmt.Sscanf(idStr, "%d", &userID)
	if userID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	var body struct {
		GroupIDs []uint64 `json:"group_ids"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}

	// Verify user exists and is an admin
	var exists bool
	sqlDB.QueryRowContext(c.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = `+h.dialect.Placeholder(1)+` AND tenant_id = `+h.dialect.Placeholder(2)+` AND role IN ('admin','superadmin') AND deleted_at IS NULL)`,
		userID, tenantID).Scan(&exists)
	if !exists {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}

	// Replace all group memberships for this user
	tx, err := sqlDB.BeginTx(c.Context(), nil)
	if err != nil {
		h.logger.Error("admin user groups: begin tx failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(c.Context(),
		`DELETE FROM coremail_admin_group_members WHERE user_id = `+h.dialect.Placeholder(1), userID); err != nil {
		h.logger.Error("admin user groups: clear groups failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}

	for _, gid := range body.GroupIDs {
		_, err = tx.ExecContext(c.Context(),
			h.dialect.Upsert("coremail_admin_group_members",
				[]string{"group_id", "user_id"},
				[]string{"group_id", "user_id"},
				nil,
			),
			gid, userID,
		)
		if err != nil {
			h.logger.Error("admin user groups: add group failed", zap.Error(err))
			return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
		}
	}

	if err := tx.Commit(); err != nil {
		h.logger.Error("admin user groups: commit failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}

	h.writeAuditLog(c, "admin_user.groups_update", fmt.Sprintf("user_id:%d", userID))
	return c.JSON(fiber.Map{"result": "updated", "id": userID, "group_ids": body.GroupIDs})
}

// DeleteAdminUser soft-deletes an admin user. Prevents deleting the last active superadmin.
func (h *Handler) DeleteAdminUser(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	sqlDB := h.sqlDB()
	tenantID := h.tenantID(c)

	idStr := c.Params("id")
	var id uint64
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	// Prevent deleting self
	currentUserID, _ := c.Locals("user_id").(uint)
	if uint(id) == currentUserID {
		return fiber.NewError(fiber.StatusConflict, "cannot delete your own admin account")
	}

	// Check if this is the last active superadmin
	var isSuperadmin bool
	sqlDB.QueryRowContext(c.Context(),
		`SELECT role = 'superadmin' FROM users WHERE id = `+h.dialect.Placeholder(1)+` AND tenant_id = `+h.dialect.Placeholder(2)+` AND deleted_at IS NULL`,
		id, tenantID).Scan(&isSuperadmin)
	if isSuperadmin {
		var count int
		sqlDB.QueryRowContext(c.Context(),
			`SELECT COUNT(*) FROM users WHERE tenant_id = `+h.dialect.Placeholder(1)+` AND role = 'superadmin' AND active = `+h.dialect.TrueLiteral()+` AND deleted_at IS NULL`,
			tenantID).Scan(&count)
		if count <= 1 {
			return fiber.NewError(fiber.StatusForbidden, "cannot delete the last active superadmin")
		}
	}

	now := time.Now().UTC()
	res, err := sqlDB.ExecContext(c.Context(),
		`UPDATE users SET active = `+h.dialect.FalseLiteral()+`, deleted_at = `+h.dialect.Placeholder(1)+`, updated_at = `+h.dialect.Placeholder(2)+` WHERE id = `+h.dialect.Placeholder(3)+` AND tenant_id = `+h.dialect.Placeholder(4)+` AND role IN ('admin','superadmin') AND deleted_at IS NULL`,
		now, now, id, tenantID)
	if err != nil {
		h.logger.Error("delete admin user: DB update failed", zap.Error(err))
		return fiber.NewError(fiber.StatusInternalServerError, "failed to process admin user request")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "admin user not found")
	}
	h.writeAuditLog(c, "admin_user.delete", fmt.Sprintf("user_id:%d", id))
	return c.JSON(fiber.Map{"result": "deleted", "id": id})
}
