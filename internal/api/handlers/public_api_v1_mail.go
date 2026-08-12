package handlers

import (
	"database/sql"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api/publicv1"
)

func (h *Handler) PublicListAliases(c fiber.Ctx) error {
	db, err := h.publicSQLDB()
	if err != nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Alias service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	p, ok := publicPage(c)
	if !ok {
		return nil
	}
	where := "tenant_id=" + h.sqlDialect().Placeholder(1) + " AND deleted_at IS NULL"
	args := []any{tenantID}
	if p.Search != "" {
		search := "%" + strings.ToLower(p.Search) + "%"
		args = append(args, search)
		fromPlaceholder := h.sqlDialect().Placeholder(len(args))
		args = append(args, search)
		where += " AND (LOWER(from_addr) LIKE " + fromPlaceholder + " OR LOWER(to_addr) LIKE " + h.sqlDialect().Placeholder(len(args)) + ")"
	}
	var total int64
	if err = db.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM coremail_aliases WHERE "+where, args...).Scan(&total); err != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	args = append(args, p.Limit(), p.Offset())
	q := "SELECT id,domain_id,from_addr,to_addr,active,created_at,updated_at FROM coremail_aliases WHERE " + where + " ORDER BY from_addr ASC,id ASC LIMIT " + h.sqlDialect().Placeholder(len(args)-1) + " OFFSET " + h.sqlDialect().Placeholder(len(args))
	rows, err := db.QueryContext(c.Context(), q, args...)
	if err != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer rows.Close()
	data := []publicv1.Alias{}
	for rows.Next() {
		var a publicv1.Alias
		if err = rows.Scan(&a.ID, &a.DomainID, &a.Source, &a.Destination, &a.Active, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
		}
		a.CreatedAt = a.CreatedAt.UTC()
		a.UpdatedAt = a.UpdatedAt.UTC()
		data = append(data, a)
	}
	return c.JSON(publicv1.AliasList{Data: data, Page: pageMeta(p, total), Meta: publicMeta(c)})
}

func (h *Handler) PublicGetAlias(c fiber.Ctx) error {
	db, e := h.publicSQLDB()
	if e != nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Alias service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid alias id.")
	}
	var a publicv1.Alias
	e = db.QueryRowContext(c.Context(), "SELECT id,domain_id,from_addr,to_addr,active,created_at,updated_at FROM coremail_aliases WHERE id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", id, tenantID).Scan(&a.ID, &a.DomainID, &a.Source, &a.Destination, &a.Active, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return publicv1.WriteError(c, 404, "ALIAS_NOT_FOUND", "Alias not found.")
	}
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.UpdatedAt = a.UpdatedAt.UTC()
	return c.JSON(publicv1.AliasResponse{Data: a, Meta: publicMeta(c)})
}

func validateAliasRequest(req publicv1.AliasRequest) bool {
	if req.DomainID == 0 {
		return false
	}
	from, e1 := mail.ParseAddress(req.Source)
	to, e2 := mail.ParseAddress(req.Destination)
	return e1 == nil && e2 == nil && from.Address == req.Source && to.Address == req.Destination
}

func (h *Handler) PublicCreateAlias(c fiber.Ctx) error {
	var req publicv1.AliasRequest
	if e := c.Bind().JSON(&req); e != nil || !validateAliasRequest(req) {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "A valid domain_id, source, and destination are required.")
	}
	db, e := h.publicSQLDB()
	if e != nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Alias service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	tx, e := db.BeginTx(c.Context(), nil)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer tx.Rollback()
	var max int
	q := "SELECT max_aliases FROM coremail_domains WHERE id=" + h.sqlDialect().Placeholder(1) + " AND tenant_id=" + h.sqlDialect().Placeholder(2) + " AND deleted_at IS NULL"
	if h.sqlDialect().IsPostgres() {
		q += " FOR UPDATE"
	}
	if e = tx.QueryRowContext(c.Context(), q, req.DomainID, tenantID).Scan(&max); errors.Is(e, sql.ErrNoRows) {
		return publicv1.WriteError(c, 404, "DOMAIN_NOT_FOUND", "Domain not found.")
	}
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	if max > 0 {
		var used int
		if e = tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM coremail_aliases WHERE domain_id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", req.DomainID, tenantID).Scan(&used); e != nil {
			return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
		}
		if used >= max {
			return publicv1.WriteError(c, 409, "ALIAS_LIMIT_REACHED", "The domain alias limit has been reached.")
		}
	}
	now := time.Now().UTC()
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id uint
	if h.sqlDialect().IsPostgres() {
		e = tx.QueryRowContext(c.Context(), "INSERT INTO coremail_aliases (domain_id,tenant_id,from_addr,to_addr,active,created_at,updated_at) VALUES ("+h.sqlDialect().Placeholders(7)+") RETURNING id", req.DomainID, tenantID, req.Source, req.Destination, active, now, now).Scan(&id)
	} else {
		var res sql.Result
		res, e = tx.ExecContext(c.Context(), "INSERT INTO coremail_aliases (domain_id,tenant_id,from_addr,to_addr,active,created_at,updated_at) VALUES ("+h.sqlDialect().Placeholders(7)+")", req.DomainID, tenantID, req.Source, req.Destination, active, now, now)
		if e == nil {
			v, _ := res.LastInsertId()
			id = uint(v)
		}
	}
	if e != nil {
		return publicv1.WriteError(c, 409, "ALIAS_CREATE_FAILED", "The alias could not be created.")
	}
	if e = tx.Commit(); e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	h.writeAuditLog(c, "public.alias.create", req.Source)
	a := publicv1.Alias{ID: id, DomainID: req.DomainID, Source: req.Source, Destination: req.Destination, Active: active, CreatedAt: now, UpdatedAt: now}
	return c.Status(201).JSON(publicv1.AliasResponse{Data: a, Meta: publicMeta(c)})
}

func (h *Handler) PublicUpdateAlias(c fiber.Ctx) error {
	var req publicv1.AliasRequest
	if e := c.Bind().JSON(&req); e != nil || !validateAliasRequest(req) {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "A valid domain_id, source, and destination are required.")
	}
	db, e := h.publicSQLDB()
	if e != nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Alias service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid alias id.")
	}
	tx, e := db.BeginTx(c.Context(), nil)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer tx.Rollback()
	var currentDomainID uint
	if e = tx.QueryRowContext(c.Context(), "SELECT domain_id FROM coremail_aliases WHERE id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", id, tenantID).Scan(&currentDomainID); errors.Is(e, sql.ErrNoRows) {
		return publicv1.WriteError(c, 404, "ALIAS_NOT_FOUND", "Alias not found.")
	} else if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	domainQuery := "SELECT max_aliases FROM coremail_domains WHERE id=" + h.sqlDialect().Placeholder(1) + " AND tenant_id=" + h.sqlDialect().Placeholder(2) + " AND deleted_at IS NULL"
	if h.sqlDialect().IsPostgres() {
		domainQuery += " FOR UPDATE"
	}
	var maxAliases int
	if e = tx.QueryRowContext(c.Context(), domainQuery, req.DomainID, tenantID).Scan(&maxAliases); errors.Is(e, sql.ErrNoRows) {
		return publicv1.WriteError(c, 404, "DOMAIN_NOT_FOUND", "Domain not found.")
	} else if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	if req.DomainID != currentDomainID && maxAliases > 0 {
		var used int
		if e = tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM coremail_aliases WHERE domain_id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", req.DomainID, tenantID).Scan(&used); e != nil {
			return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
		}
		if used >= maxAliases {
			return publicv1.WriteError(c, 409, "ALIAS_LIMIT_REACHED", "The target domain alias limit has been reached.")
		}
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	now := time.Now().UTC()
	res, e := tx.ExecContext(c.Context(), "UPDATE coremail_aliases SET domain_id="+h.sqlDialect().Placeholder(1)+",from_addr="+h.sqlDialect().Placeholder(2)+",to_addr="+h.sqlDialect().Placeholder(3)+",active="+h.sqlDialect().Placeholder(4)+",updated_at="+h.sqlDialect().Placeholder(5)+" WHERE id="+h.sqlDialect().Placeholder(6)+" AND tenant_id="+h.sqlDialect().Placeholder(7)+" AND deleted_at IS NULL", req.DomainID, req.Source, req.Destination, active, now, id, tenantID)
	if e != nil {
		return publicv1.WriteError(c, 409, "ALIAS_UPDATE_FAILED", "The alias could not be updated.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return publicv1.WriteError(c, 404, "ALIAS_NOT_FOUND", "Alias not found.")
	}
	if e = tx.Commit(); e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	h.writeAuditLog(c, "public.alias.update", strconvID(id))
	return h.PublicGetAlias(c)
}

func (h *Handler) PublicDeleteAlias(c fiber.Ctx) error {
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid alias id.")
	}
	res, e := db.ExecContext(c.Context(), "UPDATE coremail_aliases SET deleted_at="+h.sqlDialect().Placeholder(1)+" WHERE id="+h.sqlDialect().Placeholder(2)+" AND tenant_id="+h.sqlDialect().Placeholder(3)+" AND deleted_at IS NULL", time.Now().UTC(), id, tenantID)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return publicv1.WriteError(c, 404, "ALIAS_NOT_FOUND", "Alias not found.")
	}
	h.writeAuditLog(c, "public.alias.delete", strconvID(id))
	return c.JSON(publicv1.DeleteResponse{Deleted: true, Meta: publicMeta(c)})
}

func strconvID(id uint) string { return strings.TrimSpace(strconv.FormatUint(uint64(id), 10)) }

func (h *Handler) PublicListGroups(c fiber.Ctx) error {
	db, e := h.publicSQLDB()
	if e != nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Group service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	p, ok := publicPage(c)
	if !ok {
		return nil
	}
	where := "tenant_id=" + h.sqlDialect().Placeholder(1) + " AND deleted_at IS NULL"
	args := []any{tenantID}
	if p.Search != "" {
		search := "%" + strings.ToLower(p.Search) + "%"
		args = append(args, search, search)
		where += " AND (LOWER(name) LIKE " + h.sqlDialect().Placeholder(2) + " OR LOWER(description) LIKE " + h.sqlDialect().Placeholder(3) + ")"
	}
	var total int64
	if e = db.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM coremail_groups WHERE "+where, args...).Scan(&total); e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	args = append(args, p.Limit(), p.Offset())
	rows, e := db.QueryContext(c.Context(), "SELECT id,name,description,created_at,updated_at FROM coremail_groups WHERE "+where+" ORDER BY name ASC,id ASC LIMIT "+h.sqlDialect().Placeholder(len(args)-1)+" OFFSET "+h.sqlDialect().Placeholder(len(args)), args...)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer rows.Close()
	data := []publicv1.Group{}
	for rows.Next() {
		var g publicv1.Group
		if e = rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt); e != nil {
			return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
		}
		g.CreatedAt = g.CreatedAt.UTC()
		g.UpdatedAt = g.UpdatedAt.UTC()
		data = append(data, g)
	}
	return c.JSON(publicv1.GroupList{Data: data, Page: pageMeta(p, total), Meta: publicMeta(c)})
}

func (h *Handler) PublicGetGroup(c fiber.Ctx) error {
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	var g publicv1.Group
	e = db.QueryRowContext(c.Context(), "SELECT id,name,description,created_at,updated_at FROM coremail_groups WHERE id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", id, tenantID).Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return publicv1.WriteError(c, 404, "GROUP_NOT_FOUND", "Group not found.")
	}
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	g.CreatedAt = g.CreatedAt.UTC()
	g.UpdatedAt = g.UpdatedAt.UTC()
	return c.JSON(publicv1.GroupResponse{Data: g, Meta: publicMeta(c)})
}

func (h *Handler) PublicCreateGroup(c fiber.Ctx) error {
	var req publicv1.GroupRequest
	if e := c.Bind().JSON(&req); e != nil || strings.TrimSpace(req.Name) == "" {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "Group name is required.")
	}
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	now := time.Now().UTC()
	var id uint
	if h.sqlDialect().IsPostgres() {
		e := db.QueryRowContext(c.Context(), "INSERT INTO coremail_groups (tenant_id,name,description,created_at,updated_at) VALUES ("+h.sqlDialect().Placeholders(5)+") RETURNING id", tenantID, strings.TrimSpace(req.Name), req.Description, now, now).Scan(&id)
		if e != nil {
			return publicv1.WriteError(c, 409, "GROUP_CREATE_FAILED", "The group could not be created.")
		}
	} else {
		res, e := db.ExecContext(c.Context(), "INSERT INTO coremail_groups (tenant_id,name,description,created_at,updated_at) VALUES ("+h.sqlDialect().Placeholders(5)+")", tenantID, strings.TrimSpace(req.Name), req.Description, now, now)
		if e != nil {
			return publicv1.WriteError(c, 409, "GROUP_CREATE_FAILED", "The group could not be created.")
		}
		v, _ := res.LastInsertId()
		id = uint(v)
	}
	h.writeAuditLog(c, "public.group.create", strings.TrimSpace(req.Name))
	return c.Status(201).JSON(publicv1.GroupResponse{Data: publicv1.Group{ID: id, Name: strings.TrimSpace(req.Name), Description: req.Description, CreatedAt: now, UpdatedAt: now}, Meta: publicMeta(c)})
}

func (h *Handler) PublicUpdateGroup(c fiber.Ctx) error {
	var req publicv1.GroupRequest
	if e := c.Bind().JSON(&req); e != nil || strings.TrimSpace(req.Name) == "" {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "Group name is required.")
	}
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	res, e := db.ExecContext(c.Context(), "UPDATE coremail_groups SET name="+h.sqlDialect().Placeholder(1)+",description="+h.sqlDialect().Placeholder(2)+",updated_at="+h.sqlDialect().Placeholder(3)+" WHERE id="+h.sqlDialect().Placeholder(4)+" AND tenant_id="+h.sqlDialect().Placeholder(5)+" AND deleted_at IS NULL", strings.TrimSpace(req.Name), req.Description, time.Now().UTC(), id, tenantID)
	if e != nil {
		return publicv1.WriteError(c, 409, "GROUP_UPDATE_FAILED", "The group could not be updated.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return publicv1.WriteError(c, 404, "GROUP_NOT_FOUND", "Group not found.")
	}
	h.writeAuditLog(c, "public.group.update", strconvID(id))
	return h.PublicGetGroup(c)
}

func (h *Handler) PublicDeleteGroup(c fiber.Ctx) error {
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	tx, e := db.BeginTx(c.Context(), nil)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(c.Context(), "UPDATE coremail_groups SET deleted_at="+h.sqlDialect().Placeholder(1)+" WHERE id="+h.sqlDialect().Placeholder(2)+" AND tenant_id="+h.sqlDialect().Placeholder(3)+" AND deleted_at IS NULL", time.Now().UTC(), id, tenantID)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return publicv1.WriteError(c, 404, "GROUP_NOT_FOUND", "Group not found.")
	}
	if _, e = tx.ExecContext(c.Context(), "DELETE FROM coremail_group_members WHERE group_id="+h.sqlDialect().Placeholder(1), id); e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	if e = tx.Commit(); e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	h.writeAuditLog(c, "public.group.delete", strconvID(id))
	return c.JSON(publicv1.DeleteResponse{Deleted: true, Meta: publicMeta(c)})
}

func (h *Handler) PublicListGroupMembers(c fiber.Ctx) error {
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	p, ok := publicPage(c)
	if !ok {
		return nil
	}
	var exists int
	if e = db.QueryRowContext(c.Context(), "SELECT 1 FROM coremail_groups WHERE id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", id, tenantID).Scan(&exists); e != nil {
		return publicv1.WriteError(c, 404, "GROUP_NOT_FOUND", "Group not found.")
	}
	var total int64
	_ = db.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM coremail_group_members WHERE group_id="+h.sqlDialect().Placeholder(1), id).Scan(&total)
	rows, e := db.QueryContext(c.Context(), "SELECT id,email,added_at FROM coremail_group_members WHERE group_id="+h.sqlDialect().Placeholder(1)+" ORDER BY email ASC,id ASC LIMIT "+h.sqlDialect().Placeholder(2)+" OFFSET "+h.sqlDialect().Placeholder(3), id, p.Limit(), p.Offset())
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	defer rows.Close()
	data := []publicv1.GroupMember{}
	for rows.Next() {
		var m publicv1.GroupMember
		if e = rows.Scan(&m.ID, &m.Email, &m.AddedAt); e != nil {
			return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
		}
		m.AddedAt = m.AddedAt.UTC()
		data = append(data, m)
	}
	return c.JSON(publicv1.GroupMemberList{Data: data, Page: pageMeta(p, total), Meta: publicMeta(c)})
}

func (h *Handler) PublicAddGroupMember(c fiber.Ctx) error {
	var req publicv1.GroupMemberRequest
	if e := c.Bind().JSON(&req); e != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	a, e := mail.ParseAddress(req.Email)
	if e != nil || a.Address != req.Email {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "A valid member email is required.")
	}
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	var exists int
	if e = db.QueryRowContext(c.Context(), "SELECT 1 FROM coremail_groups WHERE id="+h.sqlDialect().Placeholder(1)+" AND tenant_id="+h.sqlDialect().Placeholder(2)+" AND deleted_at IS NULL", id, tenantID).Scan(&exists); e != nil {
		return publicv1.WriteError(c, 404, "GROUP_NOT_FOUND", "Group not found.")
	}
	now := time.Now().UTC()
	var memberID uint
	if h.sqlDialect().IsPostgres() {
		e = db.QueryRowContext(c.Context(), "INSERT INTO coremail_group_members (group_id,email,added_at) VALUES ("+h.sqlDialect().Placeholders(3)+") RETURNING id", id, req.Email, now).Scan(&memberID)
	} else {
		var res sql.Result
		res, e = db.ExecContext(c.Context(), "INSERT INTO coremail_group_members (group_id,email,added_at) VALUES ("+h.sqlDialect().Placeholders(3)+")", id, req.Email, now)
		if e == nil {
			v, _ := res.LastInsertId()
			memberID = uint(v)
		}
	}
	if e != nil {
		return publicv1.WriteError(c, 409, "GROUP_MEMBER_EXISTS", "The member could not be added.")
	}
	h.writeAuditLog(c, "public.group.member.create", req.Email)
	return c.Status(201).JSON(struct {
		Data publicv1.GroupMember `json:"data"`
		Meta publicv1.Metadata    `json:"meta"`
	}{publicv1.GroupMember{ID: memberID, Email: req.Email, AddedAt: now}, publicMeta(c)})
}

func (h *Handler) PublicDeleteGroupMember(c fiber.Ctx) error {
	db, _ := h.publicSQLDB()
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid group id.")
	}
	memberID, e := parsePublicID(c, "memberId")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid member id.")
	}
	res, e := db.ExecContext(c.Context(), "DELETE FROM coremail_group_members WHERE id="+h.sqlDialect().Placeholder(1)+" AND group_id IN (SELECT id FROM coremail_groups WHERE id="+h.sqlDialect().Placeholder(2)+" AND tenant_id="+h.sqlDialect().Placeholder(3)+" AND deleted_at IS NULL)", memberID, id, tenantID)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return publicv1.WriteError(c, 404, "GROUP_MEMBER_NOT_FOUND", "Group member not found.")
	}
	h.writeAuditLog(c, "public.group.member.delete", strconvID(memberID))
	return c.JSON(publicv1.DeleteResponse{Deleted: true, Meta: publicMeta(c)})
}
