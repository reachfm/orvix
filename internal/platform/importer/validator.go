package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Validator struct {
	lookup   EntityLookup
	tenantID uint
	source   *ParsedSource
	conflict ConflictPolicy
}

func NewValidator(lookup EntityLookup, tenantID uint, source *ParsedSource, conflict ConflictPolicy) *Validator {
	return &Validator{lookup: lookup, tenantID: tenantID, source: source, conflict: conflict}
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
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}
	name := fieldStr(entity.Raw, "name")
	domain := fieldStr(entity.Raw, "domain")
	if !utf8.ValidString(name) || !utf8.ValidString(domain) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if name == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "name required"})
		return row
	}
	if domain == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "domain", Message: "domain required"})
		return row
	}
	row.RowKey = "org_" + strings.ToLower(domain)
	if exists, _ := v.lookup.OrgExists(ctx, domain, v.tenantID); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
		case ConflictSkip:
			row.Status = RowSkipped
		default:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	safeBytes, _ := json.Marshal(map[string]any{"name": name, "domain": domain})
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateTenantAdmin(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}
	email := fieldStr(entity.Raw, "email")
	role := fieldStr(entity.Raw, "role")
	password := fieldStr(entity.Raw, "password")
	if !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "email", Message: "valid email required"})
		return row
	}
	lowerRole := strings.ToLower(strings.TrimSpace(role))
	if lowerRole == "platform_super_admin" || lowerRole == "superadmin" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodePlatformRoleInj), Message: "cannot create platform admin through tenant import"})
		return row
	}
	if len(password) < 8 {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "password", Message: "password must be >= 8 chars"})
		return row
	}
	row.RowKey = "admin_" + email
	if exists, _ := v.lookup.UserExists(ctx, email); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
		case ConflictSkip:
			row.Status = RowSkipped
		default:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	safeBytes, _ := json.Marshal(map[string]any{"email": email, "name": fieldStr(entity.Raw, "name"), "role": role})
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
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "valid domain name required"})
		return row
	}
	row.RowKey = "domain_" + strings.ToLower(name)
	if exists, existingTenant, _ := v.lookup.DomainExists(ctx, name); exists {
		if existingTenant != v.tenantID {
			row.Status = RowInvalid
			row.Errors = append(row.Errors, RowValidationError{Code: string(CodeCrossTenant), Message: "domain belongs to another tenant"})
			return row
		}
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
		case ConflictSkip:
			row.Status = RowSkipped
		default:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	safeBytes, _ := json.Marshal(map[string]any{"name": name})
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateMailbox(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity}
	email := fieldStr(entity.Raw, "email")
	domain := fieldStr(entity.Raw, "domain")
	password := fieldStr(entity.Raw, "password")
	if !utf8.ValidString(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidUTF8), Message: "invalid UTF-8"})
		return row
	}
	if email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "email", Message: "valid email required"})
		return row
	}
	if len(password) < 8 {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "password", Message: "password must be >= 8 chars"})
		return row
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 && domain == "" {
		domain = parts[1]
	}
	if exists, existingTenant, _ := v.lookup.DomainExists(ctx, domain); !exists {
		row.Status = RowDeferred
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeMissingParent), Message: "parent domain not found: " + domain})
		return row
	} else if existingTenant != v.tenantID {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeCrossTenant), Message: "cross-tenant domain"})
		return row
	}
	row.RowKey = "mb_" + strings.ToLower(email)
	if exists, _ := v.lookup.MailboxExists(ctx, email); exists {
		switch v.conflict {
		case ConflictFail:
			row.Status = RowConflict
		case ConflictSkip:
			row.Status = RowSkipped
		default:
			row.Status = RowConflict
		}
		return row
	}
	row.Status = RowValid
	safeBytes, _ := json.Marshal(map[string]any{"email": email, "name": fieldStr(entity.Raw, "name"), "domain": domain})
	row.SafeData = json.RawMessage(safeBytes)
	return row
}

func (v *Validator) validateAlias(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, Status: RowValid}
	from := fieldStr(entity.Raw, "from_addr", "from", "alias")
	to := fieldStr(entity.Raw, "to_addr", "to", "forward_to")
	if !utf8.ValidString(from) || !validEmail(from) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "from", Message: "valid from address required"})
		return row
	}
	if !utf8.ValidString(to) || !validEmail(to) {
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
	if !utf8.ValidString(name) || name == "" {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Field: "name", Message: "group name required"})
		return row
	}
	row.RowKey = "group_" + strings.ToLower(name)
	return row
}

func (v *Validator) validateGroupMembership(ctx context.Context, entity ParsedEntity) ImportRow {
	row := ImportRow{Line: entity.Line, Entity: entity.Entity, Status: RowValid}
	group := fieldStr(entity.Raw, "group", "group_name")
	email := fieldStr(entity.Raw, "email", "member_email")
	if group == "" || email == "" || !validEmail(email) {
		row.Status = RowInvalid
		row.Errors = append(row.Errors, RowValidationError{Code: string(CodeInvalidField), Message: "group name and valid email required"})
		return row
	}
	row.RowKey = "mem_" + strings.ToLower(group) + "_" + strings.ToLower(email)
	return row
}

var domainNameRe = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,}$`)

func validDomainName(name string) bool { return domainNameRe.MatchString(strings.ToLower(name)) }

func validEmail(email string) bool {
	if len(email) > 254 || !strings.Contains(email, "@") {
		return false
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return false
	}
	return true
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

// Ensure fmt import used
var _ = fmt.Sprintf
