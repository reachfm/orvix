package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Validator struct {
	db       *sql.DB
	tenantID uint
	source   *ParsedSource
	conflict ConflictPolicy
}

func NewValidator(db *sql.DB, tenantID uint, source *ParsedSource, conflict ConflictPolicy) *Validator {
	return &Validator{db: db, tenantID: tenantID, source: source, conflict: conflict}
}

func (v *Validator) ValidateAll(ctx context.Context) ([]ImportRow, error) {
	var results []ImportRow

	for _, entity := range v.source.Entities {
		if len(entity.Errors) > 0 {
			results = append(results, ImportRow{
				Line:   entity.Line,
				Entity: entity.Entity,
				Status: RowInvalid,
				Errors: formatEntityErrors(entity.Errors),
			})
			continue
		}
		row := v.validateEntity(ctx, entity)
		results = append(results, row)
	}
	return results, nil
}

func (v *Validator) validateEntity(ctx context.Context, entity ParsedEntity) ImportRow {
	switch entity.Entity {
	case EntityOrganization:
		return v.validateOrganization(ctx, entity)
	case EntityTenantAdmin:
		return v.validateTenantAdmin(ctx, entity)
	case EntityDomain:
		return v.validateDomain(ctx, entity)
	case EntityMailbox:
		return v.validateMailbox(ctx, entity)
	case EntityAlias:
		return v.validateAlias(ctx, entity)
	case EntityGroup:
		return v.validateGroup(ctx, entity)
	case EntityGroupMembership:
		return v.validateGroupMembership(ctx, entity)
	default:
		return ImportRow{
			Line:   entity.Line,
			Entity: entity.Entity,
			Status: RowInvalid,
			Errors: []RowValidationError{{Code: string(CodeUnsupportedEntity), Message: "unsupported entity type"}},
		}
	}
}

func (v *Validator) validateOrganization(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, RowKey: fmt.Sprintf("org_%d", entity.Line)}

	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")

	if !utf8.ValidString(name) || !utf8.ValidString(domain) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "org fields must be valid UTF-8"})
		return row
	}
	if name == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "name is required"})
		return row
	}
	if domain == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "domain", Message: "domain is required"})
		return row
	}
	if len(name) > MaxFieldLength || len(domain) > MaxFieldLength {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Message: "field exceeds maximum length"})
		return row
	}

	row.RowKey = "org_" + strings.ToLower(domain)
	if exists, _ := v.orgExists(ctx, domain); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeDuplicateRow), Message: "organization already exists: " + domain})
		case ConflictSkip:
			row.Status = RowSkipped
		case ConflictUpdateSafe:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	row.SafeData = entity.Data
	return row
}

func (v *Validator) validateTenantAdmin(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}

	email := fieldStr(entity.Raw, "email")
	role := fieldStr(entity.Raw, "role")
	name := fieldStr(entity.Raw, "name")
	password := fieldStr(entity.Raw, "password")

	if !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "email", Message: "valid email is required"})
		return row
	}
	lowerRole := strings.ToLower(strings.TrimSpace(role))
	if lowerRole == "platform_super_admin" || lowerRole == "superadmin" || lowerRole == "platform_admin" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodePlatformRoleInj), Message: "cannot create platform administrator via tenant import"})
		return row
	}
	if password == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "password", Message: "password is required"})
		return row
	}
	if len(password) < 8 {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "password", Message: "password must be at least 8 characters"})
		return row
	}

	row.RowKey = "admin_" + email
	if exists, _ := v.userExists(ctx, email); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeDuplicateRow), Message: "user already exists: " + email})
		case ConflictUpdateSafe:
			row.Status = RowConflict
		default:
			row.Status = RowSkipped
		}
		return row
	}
	row.Status = RowValid
	safe := map[string]any{"email": email, "name": name, "role": role}
	safeBytes, _ := json.Marshal(safe)
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateDomain(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}

	name := fieldStr(entity.Raw, "name", "domain")

	if !utf8.ValidString(name) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if name == "" || !validDomainName(name) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "valid domain name is required"})
		return row
	}
	if len(name) > MaxFieldLength {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Message: "field exceeds maximum length"})
		return row
	}

	row.RowKey = "domain_" + strings.ToLower(name)
	if exists, existingTenant, _ := v.domainExists(ctx, name); exists {
		if existingTenant != v.tenantID {
			row.Status = RowInvalid
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeCrossTenant), Message: "domain belongs to another tenant: " + name})
			return row
		}
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeDuplicateRow), Message: "domain already exists: " + name})
		case ConflictSkip:
			row.Status = RowSkipped
		case ConflictUpdateSafe:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	safe := map[string]any{"name": name, "status": fieldStr(entity.Raw, "status")}
	safeBytes, _ := json.Marshal(safe)
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateMailbox(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}

	email := fieldStr(entity.Raw, "email")
	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")
	password := fieldStr(entity.Raw, "password")

	if !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "email", Message: "valid email is required"})
		return row
	}
	if password == "" || len(password) < 8 {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "password", Message: "password must be at least 8 characters"})
		return row
	}

	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 && domain == "" {
		domain = parts[1]
	} else if domain == "" && len(parts) < 2 {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "domain", Message: "domain is required"})
		return row
	}

	exists, existingTenant, _ := v.domainExists(ctx, domain)
	if !exists {
		row.Status = RowDeferred
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeMissingParent), Message: "parent domain not found: " + domain})
		return row
	}
	if existingTenant != v.tenantID {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeCrossTenant), Message: "domain belongs to another tenant: " + domain})
		return row
	}

	row.RowKey = "mb_" + strings.ToLower(email)
	if exists, _ := v.mailboxExists(ctx, email); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeDuplicateRow), Message: "mailbox already exists: " + email})
		case ConflictSkip:
			row.Status = RowSkipped
		case ConflictUpdateSafe:
			row.Status = RowConflict
		}
		return row
	}

	row.Status = RowValid
	safe := map[string]any{"email": email, "name": name, "domain": domain}
	safeBytes, _ := json.Marshal(safe)
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateAlias(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, Status: RowValid}
	from := fieldStr(entity.Raw, "from_addr", "from", "alias")
	to := fieldStr(entity.Raw, "to_addr", "to", "forward_to")

	if !utf8.ValidString(from) || !utf8.ValidString(to) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if from == "" || !validEmail(from) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "from", Message: "valid from address required"})
		return row
	}
	if to == "" || !validEmail(to) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "to", Message: "valid to address required"})
		return row
	}

	row.RowKey = "alias_" + strings.ToLower(from)
	return row
}

func (v *Validator) validateGroup(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, Status: RowValid}
	name := fieldStr(entity.Raw, "name")
	email := fieldStr(entity.Raw, "email")

	if !utf8.ValidString(name) || !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if name == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "group name is required"})
		return row
	}

	row.RowKey = "group_" + strings.ToLower(name)
	return row
}

func (v *Validator) validateGroupMembership(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, Status: RowValid}
	group := fieldStr(entity.Raw, "group", "group_name")
	email := fieldStr(entity.Raw, "email", "member_email")

	if !utf8.ValidString(group) || !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if group == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "group", Message: "group name is required"})
		return row
	}
	if email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "email", Message: "valid email is required"})
		return row
	}

	row.RowKey = "mem_" + strings.ToLower(group) + "_" + strings.ToLower(email)
	return row
}

func (v *Validator) orgExists(ctx context.Context, domain string) (bool, error) {
	var c int
	err := v.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE domain=? AND deleted_at IS NULL`, domain).Scan(&c)
	return c > 0, err
}

func (v *Validator) userExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := v.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL`, email).Scan(&c)
	return c > 0, err
}

func (v *Validator) domainExists(ctx context.Context, name string) (bool, uint, error) {
	var id, tenantID uint
	err := v.db.QueryRowContext(ctx, `SELECT id, tenant_id FROM coremail_domains WHERE name=? AND deleted_at IS NULL AND status='active'`, strings.ToLower(name)).Scan(&id, &tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, tenantID, nil
}

func (v *Validator) mailboxExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := v.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL`, email).Scan(&c)
	return c > 0, err
}

func validEmail(email string) bool {
	if len(email) > 254 || !strings.Contains(email, "@") {
		return false
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return false
	}
	if strings.ContainsAny(parts[0], " \t\r\n") || strings.ContainsAny(parts[1], " \t\r\n") {
		return false
	}
	domainParts := strings.Split(parts[1], ".")
	if len(domainParts) < 2 {
		return false
	}
	for _, part := range domainParts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
	}
	return true
}

var domainNameRe = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,}$`)

func validDomainName(name string) bool {
	return domainNameRe.MatchString(strings.ToLower(name))
}

func fieldStr(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func formatEntityErrors(errs []string) []RowValidationError {
	var out []RowValidationError
	for _, e := range errs {
		out = append(out, RowValidationError{Code: string(CodeParseError), Message: e})
	}
	return out
}
