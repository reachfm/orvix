package importer

import (
	"encoding/json"
	"regexp"
	"sort"
)

// SafeFieldSet maps field names to their safeness for a given entity type.
// Only fields in this set may be updated when conflict_policy=update_safe_fields.
type SafeFieldSet map[string]bool

var (
	safeFieldsOrg     = SafeFieldSet{"name": true, "logo_url": true, "primary_color": true}
	safeFieldsAdmin   = SafeFieldSet{"name": true}
	safeFieldsDomain  = SafeFieldSet{"description": true}
	safeFieldsMailbox = SafeFieldSet{"name": true}
	safeFieldsAlias   = SafeFieldSet{}
	safeFieldsGroup   = SafeFieldSet{"name": true, "description": true}
)

// SafeFields returns the safe-field allowlist for the given entity type.
// An empty set means NO fields are safe — every update attempt is forbidden.
func SafeFields(entity ImportEntityType) SafeFieldSet {
	switch entity {
	case EntityOrganization:
		return safeFieldsOrg
	case EntityTenantAdmin:
		return safeFieldsAdmin
	case EntityDomain:
		return safeFieldsDomain
	case EntityMailbox:
		return safeFieldsMailbox
	case EntityAlias:
		return safeFieldsAlias
	case EntityGroup:
		return safeFieldsGroup
	default:
		return SafeFieldSet{}
	}
}

// IsAllowed reports whether the given field is in the safe allowlist.
func (s SafeFieldSet) IsAllowed(field string) bool {
	return s[field]
}

// HasAny reports whether ANY field is safe to update.
func (s SafeFieldSet) HasAny() bool {
	return len(s) > 0
}

// canonicalFieldName maps common CSV column aliases to the canonical field
// name used in allowlists and service updates.
func canonicalFieldName(field string, entity ImportEntityType) string {
	switch entity {
	case EntityOrganization:
		return canonicalOrgField(field)
	case EntityTenantAdmin:
		return canonicalAdminField(field)
	case EntityDomain:
		return canonicalDomainField(field)
	case EntityMailbox:
		return canonicalMailboxField(field)
	case EntityAlias:
		return canonicalAliasField(field)
	case EntityGroup:
		return canonicalGroupField(field)
	default:
		return field
	}
}

func canonicalOrgField(f string) string {
	switch f {
	case "name", "org_name", "organization_name":
		return "name"
	case "logo_url", "logo", "branding_logo":
		return "logo_url"
	case "primary_color", "color", "brand_color":
		return "primary_color"
	default:
		return f
	}
}

func canonicalAdminField(f string) string {
	switch f {
	case "name", "full_name", "display_name":
		return "name"
	default:
		return f
	}
}

func canonicalDomainField(f string) string {
	switch f {
	case "description":
		return "description"
	default:
		return f
	}
}

func canonicalMailboxField(f string) string {
	switch f {
	case "name", "display_name", "full_name":
		return "name"
	default:
		return f
	}
}

func canonicalAliasField(f string) string {
	return f
}

func canonicalGroupField(f string) string {
	switch f {
	case "name", "group_name":
		return "name"
	case "description":
		return "description"
	default:
		return f
	}
}

// EntityInfo holds the bare-minimum information needed by the validator and
// executor to decide whether an existing entity can be safely updated.
type EntityInfo struct {
	ID          uint
	EntityType  ImportEntityType
	CurrentName string
	Fields      map[string]any
}

// identityKeys returns the field names used to locate an existing entity.
// These are permitted in a conflict-resolution row even though they are not
// updatable: they identify WHICH entity the row targets, and by construction
// their CSV value equals the stored value (the lookup succeeded), so they are
// never a forbidden change.
func identityKeys(entity ImportEntityType) []string {
	switch entity {
	case EntityOrganization:
		return []string{"domain"}
	case EntityTenantAdmin:
		return []string{"email"}
	case EntityDomain:
		return []string{"name", "domain"}
	case EntityMailbox:
		return []string{"email"}
	case EntityAlias:
		return []string{"from_addr", "from", "alias"}
	case EntityGroup:
		return []string{"name", "group_name"}
	default:
		return nil
	}
}

// isIdentityKey reports whether the field is an entity's lookup key.
func isIdentityKey(entity ImportEntityType, field string) bool {
	for _, k := range identityKeys(entity) {
		if k == field {
			return true
		}
	}
	return false
}

// ExtractSafeFields separates raw CSV/JSON fields into two buckets:
//   - safe:      fields that appear in the allowlist for this entity
//   - forbidden: fields that appear in CSV/JSON but are NOT in the allowlist
//     and are not the entity's identity/lookup key
//
// Fields NOT present in the source (nil/absent) are omitted from both.
func ExtractSafeFields(raw map[string]any, entity ImportEntityType) (safe map[string]any, forbidden []string) {
	allow := SafeFields(entity)
	safe = make(map[string]any)
	for k, v := range raw {
		if k == "entity" || k == "entity_type" {
			continue
		}
		canonical := canonicalFieldName(k, entity)
		if allow.IsAllowed(canonical) {
			safe[canonical] = v
		} else if !isIdentityKey(entity, canonical) {
			forbidden = append(forbidden, canonical)
		}
	}
	sort.Strings(forbidden)
	return safe, forbidden
}

// SafeFieldsChanged returns the subset of candidate fields whose values
// differ from the current state. Empty map means nothing changed.
func SafeFieldsChanged(current, candidate map[string]any, entity ImportEntityType) map[string]any {
	allow := SafeFields(entity)
	changed := make(map[string]any)
	for k, v := range candidate {
		if !allow.IsAllowed(k) {
			continue
		}
		cur, ok := current[k]
		if !ok || !equalFieldValues(cur, v) {
			changed[k] = v
		}
	}
	return changed
}

func equalFieldValues(a, b any) bool {
	as, aOk := a.(string)
	bs, bOk := b.(string)
	if aOk && bOk {
		return as == bs
	}
	return false
}

// safeFieldMismatch compares the current safe-field values against an
// expected snapshot (the import's recorded after-image). It returns the
// sorted list of fields whose current value differs from the snapshot —
// the fields a human modified after the import. An empty result means the
// current state exactly matches the expected snapshot.
func safeFieldMismatch(current, expected map[string]any) []string {
	var diff []string
	for k, want := range expected {
		cur, ok := current[k]
		if !ok || !equalFieldValues(cur, want) {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	return diff
}

// sensitiveFieldRe matches field names that carry secrets so the dry-run
// report can redact them before surfacing before/after diffs.
var sensitiveFieldRe = regexp.MustCompile(`(?i)password|secret|token|api[_-]?key|mfa|otp|credential|hash`)

// RedactSensitive scrubs any secret-looking field values from a serialized
// image (BeforeImage/AfterImage/SafeData) before it is included in a report.
// Non-matching values pass through unchanged.
func RedactSensitive(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	changed := false
	for k := range m {
		if sensitiveFieldRe.MatchString(k) {
			m[k] = "[REDACTED]"
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
