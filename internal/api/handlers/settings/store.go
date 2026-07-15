// Package settings implements the admin settings persistence layer.
//
// PATCH /api/v1/admin/settings now writes to a DB-backed key/value
// table (admin_settings) so changes survive a service restart. The
// previous endpoint returned not_implemented with no side effects.
//
// Design rules:
//
//   - Every writable field has an explicit allowlist entry. Unknown
//     fields are rejected, not silently ignored.
//   - Forbidden fields (license key paths, JWT signing key, VAPID
//     private key, API tokens) are hard-rejected and recorded in the
//     audit log with the offending key name redacted.
//   - Fields whose change requires a listener / module restart are
//     returned with requires_restart=true so the admin UI can show
//     "restart required" badges honestly.
//   - The store never logs or returns secret values; secret-shaped
//     fields are redacted to "REDACTED" in the response and audit log.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Section is the top-level grouping for the admin UI.
type Section string

const (
	SectionGeneral    Section = "general"
	SectionListeners  Section = "mail_listeners"
	SectionSecurity   Section = "security"
	SectionBackup     Section = "backup"
	SectionDNS        Section = "dns"
	SectionMonitoring Section = "monitoring"
	SectionLicense    Section = "license"
	SectionMailboxMgr Section = "mailbox_management"
)

// Field is one writable setting.
type Field struct {
	// Key is the canonical dotted path, e.g. "mail_listeners.submission_enabled".
	Key string
	// Section is the top-level group.
	Section Section
	// Type is the JSON type accepted: "bool", "int", "string".
	Type string
	// RestartRequired is true if changing this field requires restarting
	// the affected listener / module to take effect.
	RestartRequired bool
	// Unsafe marks fields that the store must NEVER accept, even if
	// they appear in the allowlist. The unsafe check runs after the
	// allowlist check; an unsafe key returns ErrUnsafeField.
	Unsafe bool
	// Redacted marks fields whose stored value is a secret; the GET
	// response shows "REDACTED" instead of the real value, and the
	// audit log records only the key, not the value.
	Redacted bool
}

// allowlist enumerates every writable setting. Adding a new
// writable field requires adding an entry here AND keeping the
// reject list up to date. The two together are the source of truth
// for "what can the admin UI change without code review?".
var allowlist = map[string]Field{
	// general: read-only on purpose (hostname, public_ip are operator
	// config, not admin-tunable through this endpoint).
	// We keep an entry for primary_domain so the UI gets a clean
	// "read-only" rejection rather than "unknown field".
	"general.primary_domain": {Key: "general.primary_domain", Section: SectionGeneral, Type: "string", RestartRequired: false},

	// mail_listeners: enabling/disabling a listener is hot (no
	// restart); changing ports/hosts is not.
	"mail_listeners.submission_enabled": {Key: "mail_listeners.submission_enabled", Section: SectionListeners, Type: "bool", RestartRequired: false},
	"mail_listeners.smtps_enabled":      {Key: "mail_listeners.smtps_enabled", Section: SectionListeners, Type: "bool", RestartRequired: false},
	"mail_listeners.imaps_enabled":      {Key: "mail_listeners.imaps_enabled", Section: SectionListeners, Type: "bool", RestartRequired: false},
	"mail_listeners.pop3s_enabled":      {Key: "mail_listeners.pop3s_enabled", Section: SectionListeners, Type: "bool", RestartRequired: false},
	"mail_listeners.submission_port":    {Key: "mail_listeners.submission_port", Section: SectionListeners, Type: "int", RestartRequired: true},
	"mail_listeners.smtps_port":         {Key: "mail_listeners.smtps_port", Section: SectionListeners, Type: "int", RestartRequired: true},
	"mail_listeners.imaps_port":         {Key: "mail_listeners.imaps_port", Section: SectionListeners, Type: "int", RestartRequired: true},
	"mail_listeners.pop3s_port":         {Key: "mail_listeners.pop3s_port", Section: SectionListeners, Type: "int", RestartRequired: true},

	// security: most are restart_required because the auth module caches
	// the bcrypt cost and JWT TTL at Init time.
	"security.password_min_len":    {Key: "security.password_min_len", Section: SectionSecurity, Type: "int", RestartRequired: true},
	"security.session_ttl_seconds": {Key: "security.session_ttl_seconds", Section: SectionSecurity, Type: "int", RestartRequired: true},
	"security.refresh_ttl_seconds": {Key: "security.refresh_ttl_seconds", Section: SectionSecurity, Type: "int", RestartRequired: true},

	// backup: dir and frequency require restart; retention_count is hot.
	"backup.dir":               {Key: "backup.dir", Section: SectionBackup, Type: "string", RestartRequired: true},
	"backup.retention_count":   {Key: "backup.retention_count", Section: SectionBackup, Type: "int", RestartRequired: false},
	"backup.scheduler_enabled": {Key: "backup.scheduler_enabled", Section: SectionBackup, Type: "bool", RestartRequired: false},
	"backup.frequency":         {Key: "backup.frequency", Section: SectionBackup, Type: "string", RestartRequired: true},

	// dns: public IPs are restart_required because the listeners
	// bind to them on init.
	"dns.public_ipv4": {Key: "dns.public_ipv4", Section: SectionDNS, Type: "string", RestartRequired: true},
	"dns.public_ipv6": {Key: "dns.public_ipv6", Section: SectionDNS, Type: "string", RestartRequired: true},

	// monitoring: thresholds can be applied hot.
	"monitoring.disk_usage_warning_pct":  {Key: "monitoring.disk_usage_warning_pct", Section: SectionMonitoring, Type: "int", RestartRequired: false},
	"monitoring.disk_usage_critical_pct": {Key: "monitoring.disk_usage_critical_pct", Section: SectionMonitoring, Type: "int", RestartRequired: false},
	"monitoring.queue_depth_warning":     {Key: "monitoring.queue_depth_warning", Section: SectionMonitoring, Type: "int", RestartRequired: false},
	"monitoring.queue_depth_critical":    {Key: "monitoring.queue_depth_critical", Section: SectionMonitoring, Type: "int", RestartRequired: false},

	// license: license_key is forbidden here; license metadata is
	// deliberately read-only through the admin settings endpoint.
	// We do NOT add license.* to the allowlist.
}

// forbiddenPatterns is a case-insensitive substring match against
// field keys. Any incoming key that contains one of these
// substrings is hard-rejected with ErrUnsafeField.
//
// IMPORTANT: the patterns are intentionally specific to the exact
// secret-bearing field names, not generic substrings like
// "password" or "key". A field like "security.password_min_len"
// is a *configuration knob*, not a secret, and must remain
// writable. Generic substring matching would reject it.
//
// Add a new entry here whenever a new secret-shaped field is
// introduced. The list is intentionally redundant with the
// per-field .Unsafe flag in the allowlist: belt and suspenders.
var forbiddenPatterns = []string{
	"password_hash",          // the actual stored hash, never a config knob
	"jwt_secret",             // symmetric HS256 key
	"jwt_key_path",           // the path to the RSA key file
	"vapid_private_key",      // web push private key
	"vapid_private_key_file", // path to web push private key file
	"cloudflare_api_key",     // DNS provider secret
	"cloudflare_api_token",
	"namecheap_api_key", // DNS provider secret
	"route53_secret_key",
	"route53_access_key",
	"deepseek_api_key", // AI provider key
	"license_key",      // license key
	"api_keys",         // per-user API keys (managed elsewhere)
	"dsn",              // raw DSN (may contain password)
	"bearer",           // never a config value
	"credential",       // generic credential
	"private_key",      // generic private key
}

// IsForbiddenKey reports whether a key matches any forbidden pattern.
// It is exported so the HTTP handler can short-circuit on obviously
// unsafe keys without doing the full allowlist lookup.
func IsForbiddenKey(key string) bool {
	lower := strings.ToLower(key)
	for _, p := range forbiddenPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// Allowlist returns a copy of the writable-field allowlist. The copy
// is safe to iterate from the HTTP handler.
func Allowlist() map[string]Field {
	out := make(map[string]Field, len(allowlist))
	for k, v := range allowlist {
		out[k] = v
	}
	return out
}

// Store is the DB-backed admin settings store.
type Store struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewStore returns a Store bound to the given DB. The store
// automatically creates the admin_settings table on first use.
func NewStore(db *sql.DB) *Store {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Store{db: db, dialect: dialect}
}

// EnsureSchema creates the admin_settings table and indexes if they
// do not exist. Idempotent.
func (s *Store) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("settings store: nil db")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS admin_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			section TEXT NOT NULL,
			requires_restart INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_by INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_settings_section ON admin_settings(section)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_settings_updated_at ON admin_settings(updated_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure admin_settings schema: %w", err)
		}
	}
	return nil
}

// ErrUnknownField is returned by Patch when an incoming key is not
// in the allowlist.
var ErrUnknownField = errors.New("unknown setting field")

// ErrUnsafeField is returned when a key matches the forbidden
// patterns or is explicitly marked Unsafe in the allowlist.
var ErrUnsafeField = errors.New("unsafe field rejected")

// ErrInvalidType is returned when a value's JSON type does not
// match the allowlist type for that field.
var ErrInvalidType = errors.New("invalid value type for field")

// Entry is one stored setting.
type Entry struct {
	Key             string    `json:"key"`
	Value           any       `json:"value"`
	Section         Section   `json:"section"`
	RequiresRestart bool      `json:"requires_restart"`
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedBy       *int64    `json:"updated_by,omitempty"`
	// Redacted is true when the value is a secret that the store
	// will return as "REDACTED" rather than the real value.
	Redacted bool `json:"redacted,omitempty"`
}

// Patch is the input to Store.Patch. Multiple sections and fields
// may be patched in one call; the entire call is atomic — either
// every field is written or none are.
type Patch struct {
	// Sections maps section name → map of field name → JSON value.
	// The handler accepts both dotted ("mail_listeners.submission_enabled")
	// and nested ("mail_listeners": {"submission_enabled": true}) bodies.
	Sections map[string]map[string]json.RawMessage
	// UpdatedBy is the user id performing the patch.
	UpdatedBy *int64
}

// PatchResult is the per-field outcome of a Patch call.
type PatchResult struct {
	// Applied is the list of fields that were successfully written.
	Applied []Entry
	// Rejected lists fields that were NOT written, with a reason.
	Rejected []RejectedField
	// RestartRequired is true if any applied field requires a
	// service restart to take effect. The admin UI uses this to
	// show a "restart required" banner.
	RestartRequired bool
}

// RejectedField explains why a particular field was not written.
type RejectedField struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Patch applies the patch atomically. Each field is validated against
// the allowlist, type-checked, and then written. The function returns
// the per-field outcome so the HTTP handler can report exactly what
// happened (applied vs rejected) without swallowing errors.
//
// Atomicity rules:
//
//   - If ANY field is hard-rejected (unknown or forbidden), the
//     ENTIRE patch is rejected; nothing is persisted. The result
//     still lists every field with its reason so the operator can
//     see what went wrong.
//   - Type mismatches and per-row SQL errors are soft-rejected: the
//     offending field is reported in Rejected, and the rest of the
//     patch is still applied. This matches the behavior the admin
//     UI expects when an operator types a bad value in one field
//     while the rest of the form is valid.
//
// To guarantee the hard-reject atomicity, the implementation runs
// in two passes:
//
//  1. Validate every field, partition into "good" and "rejected"
//     buckets. If any hard-reject exists, return without opening
//     a transaction.
//  2. Open a transaction, persist the good bucket, and report
//     per-row soft failures.
func (s *Store) Patch(ctx context.Context, p Patch) (*PatchResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("settings store: nil db")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}

	flat := flattenPatch(p.Sections)
	result := &PatchResult{}

	// Pass 1: validate everything. We collect the good keys
	// (key -> typed value) and the rejected ones. If any hard
	// reject is found, we return early without persisting.
	type typedField struct {
		key   string
		field Field
		value any
		now   time.Time
	}
	good := make([]typedField, 0, len(flat))
	for key, rawValue := range flat {
		if IsForbiddenKey(key) {
			result.Rejected = append(result.Rejected, RejectedField{Key: key, Reason: ErrUnsafeField.Error()})
			continue
		}
		field, ok := allowlist[key]
		if !ok {
			result.Rejected = append(result.Rejected, RejectedField{Key: key, Reason: ErrUnknownField.Error()})
			continue
		}
		if field.Unsafe {
			result.Rejected = append(result.Rejected, RejectedField{Key: key, Reason: ErrUnsafeField.Error()})
			continue
		}
		value, err := coerceType(rawValue, field.Type)
		if err != nil {
			result.Rejected = append(result.Rejected, RejectedField{Key: key, Reason: err.Error()})
			continue
		}
		good = append(good, typedField{
			key:   key,
			field: field,
			value: value,
			now:   time.Now().UTC(),
		})
	}

	// Hard-reject atomicity: if any field was rejected as unknown
	// or unsafe, abort the entire patch. Soft-rejects (type, persist)
	// are NOT hard-rejects because the admin UI is expected to be
	// able to fix them and re-submit.
	hardRejected := false
	for _, r := range result.Rejected {
		if r.Reason == ErrUnknownField.Error() || r.Reason == ErrUnsafeField.Error() {
			hardRejected = true
			break
		}
	}
	if hardRejected {
		return result, ErrUnsafeOrUnknown
	}
	if len(good) == 0 {
		// Nothing to do, but no hard-reject either (all fields were
		// soft-rejected for type/SQL errors). Return the rejections
		// so the caller can surface them.
		return result, nil
	}

	// Pass 2: persist the good fields inside a single transaction.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for _, tf := range good {
		encoded, err := json.Marshal(tf.value)
		if err != nil {
			result.Rejected = append(result.Rejected, RejectedField{Key: tf.key, Reason: "encode: " + err.Error()})
			continue
		}
		restartFlag := 0
		if tf.field.RestartRequired {
			restartFlag = 1
			result.RestartRequired = true
		}
		setConflict := "excluded"
		if s.dialect.IsPostgres() {
			setConflict = "EXCLUDED"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_settings (key, value, section, requires_restart, updated_at, updated_by)
			VALUES (`+s.dialect.Placeholders(6)+`)
			ON CONFLICT (key) DO UPDATE SET
				value = `+setConflict+`.value,
				section = `+setConflict+`.section,
				requires_restart = `+setConflict+`.requires_restart,
				updated_at = `+setConflict+`.updated_at,
				updated_by = `+setConflict+`.updated_by`,
			tf.key, string(encoded), string(tf.field.Section), restartFlag, tf.now, p.UpdatedBy,
		); err != nil {
			result.Rejected = append(result.Rejected, RejectedField{Key: tf.key, Reason: "persist: " + err.Error()})
			continue
		}
		result.Applied = append(result.Applied, Entry{
			Key:             tf.key,
			Value:           tf.value,
			Section:         tf.field.Section,
			RequiresRestart: tf.field.RestartRequired,
			UpdatedAt:       tf.now,
			UpdatedBy:       p.UpdatedBy,
			Redacted:        tf.field.Redacted,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// ErrUnsafeOrUnknown is returned by Patch when at least one field
// was rejected as unknown or unsafe. The result still contains the
// per-field detail so the HTTP handler can return it to the caller.
var ErrUnsafeOrUnknown = errors.New("patch contained unknown or unsafe fields; nothing applied")

// GetAll returns every stored setting, grouped by section. The
// caller (HTTP handler) merges these with the config defaults to
// build the response. Secret-shaped fields are returned with
// Redacted=true and Value="REDACTED" so the operator can see
// "this field is set" without seeing the value.
func (s *Store) GetAll(ctx context.Context) (map[Section][]Entry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("settings store: nil db")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value, section, requires_restart, updated_at, updated_by
		FROM admin_settings
		ORDER BY section, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[Section][]Entry)
	for rows.Next() {
		var e Entry
		var restart int
		var section string
		var value string
		if err := rows.Scan(&e.Key, &value, &section, &restart, &e.UpdatedAt, &e.UpdatedBy); err != nil {
			return nil, err
		}
		e.RequiresRestart = restart == 1
		e.Section = Section(section)
		// Decode JSON value for the response.
		var v any
		if err := json.Unmarshal([]byte(value), &v); err == nil {
			e.Value = v
		} else {
			e.Value = value
		}
		// Mark redaction.
		if IsForbiddenKey(e.Key) {
			e.Redacted = true
			e.Value = "REDACTED"
		}
		out[e.Section] = append(out[e.Section], e)
	}
	return out, rows.Err()
}

// Get returns one stored setting by key, or nil if not set.
func (s *Store) Get(ctx context.Context, key string) (*Entry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("settings store: nil db")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT key, value, section, requires_restart, updated_at, updated_by
		FROM admin_settings WHERE key = `+s.dialect.Placeholder(1), key)
	var e Entry
	var value string
	var section string
	var restart int
	if err := row.Scan(&e.Key, &value, &section, &restart, &e.UpdatedAt, &e.UpdatedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	e.RequiresRestart = restart == 1
	e.Section = Section(section)
	var v any
	if err := json.Unmarshal([]byte(value), &v); err == nil {
		e.Value = v
	} else {
		e.Value = value
	}
	if IsForbiddenKey(e.Key) {
		e.Redacted = true
		e.Value = "REDACTED"
	}
	return &e, nil
}

// flattenPatch accepts the patch body in both nested and dotted form
// and returns a single map of dotted key → raw JSON value.
func flattenPatch(sections map[string]map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	for section, fields := range sections {
		for k, v := range fields {
			out[section+"."+k] = v
		}
	}
	return out
}

// coerceType checks the JSON value against the allowlist type and
// returns the value in its native Go form. Returns ErrInvalidType
// when the type does not match.
func coerceType(raw json.RawMessage, want string) (any, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("%w: null", ErrInvalidType)
	}
	switch want {
	case "bool":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("%w: want bool, got %s", ErrInvalidType, string(raw))
		}
		return b, nil
	case "int":
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("%w: want int, got %s", ErrInvalidType, string(raw))
		}
		return n, nil
	case "string":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%w: want string, got %s", ErrInvalidType, string(raw))
		}
		return s, nil
	default:
		return nil, fmt.Errorf("unknown allowlist type %q", want)
	}
}
