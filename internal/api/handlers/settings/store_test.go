package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB returns a fresh sqlite DB for tests. The tests avoid
// importing internal/config to keep the dependency graph one-way.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStore_EnsureSchemaIdempotent(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("first EnsureSchema: %v", err)
	}
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
	// Verify table exists by querying.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_settings`).Scan(&n); err != nil {
		t.Fatalf("admin_settings table missing: %v", err)
	}
}

func TestStore_NilSafety(t *testing.T) {
	var s *Store
	if err := s.EnsureSchema(context.Background()); err == nil {
		t.Error("nil store should error on EnsureSchema")
	}
	db := openTestDB(t)
	s = NewStore(db)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	s.db = nil
	if _, err := s.Patch(context.Background(), Patch{}); err == nil {
		t.Error("nil db should error on Patch")
	}
	if _, err := s.GetAll(context.Background()); err == nil {
		t.Error("nil db should error on GetAll")
	}
}

func TestStore_Patch_AcceptsBool(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	raw, _ := json.Marshal(map[string]map[string]json.RawMessage{
		"mail_listeners": {
			"submission_enabled": json.RawMessage(`true`),
			"smtps_enabled":      json.RawMessage(`true`),
		},
	})
	var sections map[string]map[string]json.RawMessage
	_ = json.Unmarshal(raw, &sections)

	result, err := s.Patch(ctx, Patch{Sections: sections})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Errorf("Applied = %d, want 2", len(result.Applied))
	}
	if result.RestartRequired {
		t.Error("submission_enabled / smtps_enabled should NOT require restart")
	}
}

func TestStore_Patch_AcceptsInt(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]map[string]json.RawMessage{
		"security": {
			"password_min_len": json.RawMessage(`12`),
		},
	})
	var sections map[string]map[string]json.RawMessage
	_ = json.Unmarshal(raw, &sections)
	result, err := s.Patch(ctx, Patch{Sections: sections})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Errorf("Applied = %d, want 1", len(result.Applied))
	}
	if !result.RestartRequired {
		t.Error("password_min_len should require restart")
	}
}

func TestStore_Patch_RejectsUnknownField(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]map[string]json.RawMessage{
		"general": {
			"host_name_typo": json.RawMessage(`"evil.example.com"`),
		},
	})
	var sections map[string]map[string]json.RawMessage
	_ = json.Unmarshal(raw, &sections)
	result, err := s.Patch(ctx, Patch{Sections: sections})
	if err == nil {
		t.Error("expected ErrUnsafeOrUnknown for unknown field")
	}
	if len(result.Rejected) != 1 {
		t.Errorf("Rejected = %d, want 1", len(result.Rejected))
	}
	if !strings.Contains(result.Rejected[0].Reason, "unknown") {
		t.Errorf("reason = %q, want 'unknown'", result.Rejected[0].Reason)
	}
	if len(result.Applied) != 0 {
		t.Errorf("Applied = %d, want 0 (hard reject should roll back)", len(result.Applied))
	}
}

func TestStore_Patch_RejectsForbiddenKeys(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	// These are NOT in the allowlist so they would be rejected as
	// "unknown" anyway, but the forbidden check runs FIRST and
	// produces a clearer "unsafe" reason.
	cases := []string{
		"security.jwt_secret",
		"security.jwt_key_path",
		"license.license_key",
		"vapid.vapid_private_key",
		"api.cloudflare_api_key",
		"admin.admin_password",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			body := map[string]map[string]json.RawMessage{}
			parts := strings.SplitN(key, ".", 2)
			body[parts[0]] = map[string]json.RawMessage{parts[1]: json.RawMessage(`"something"`)}
			_, err := s.Patch(ctx, Patch{Sections: body})
			if err == nil {
				t.Errorf("forbidden key %q should be rejected", key)
			}
		})
	}
}

func TestStore_Patch_RejectsWrongType(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()
	body := map[string]map[string]json.RawMessage{
		"security": {
			"password_min_len": json.RawMessage(`"not-a-number"`),
		},
	}
	result, err := s.Patch(ctx, Patch{Sections: body})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(result.Rejected) != 1 {
		t.Errorf("Rejected = %d, want 1", len(result.Rejected))
	}
	if !strings.Contains(result.Rejected[0].Reason, "int") {
		t.Errorf("reason = %q, want 'int'", result.Rejected[0].Reason)
	}
	// Type mismatch is a soft reject; the transaction should still
	// commit (we want other valid fields in the same patch to
	// succeed). With only one field, Applied should be 0.
	if len(result.Applied) != 0 {
		t.Errorf("Applied = %d, want 0 (type-mismatch field not stored)", len(result.Applied))
	}
}

func TestStore_Patch_PersistsAndReloads(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	body := map[string]map[string]json.RawMessage{
		"mail_listeners": {
			"submission_enabled": json.RawMessage(`true`),
		},
		"backup": {
			"retention_count": json.RawMessage(`30`),
		},
	}
	result, err := s.Patch(ctx, Patch{Sections: body})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Errorf("Applied = %d, want 2", len(result.Applied))
	}

	// Now reload via GetAll.
	all, err := s.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	listeners := all[SectionListeners]
	if len(listeners) != 1 {
		t.Fatalf("listeners section: got %d entries, want 1", len(listeners))
	}
	got := listeners[0]
	if got.Key != "mail_listeners.submission_enabled" {
		t.Errorf("Key = %q, want mail_listeners.submission_enabled", got.Key)
	}
	if got.Value != true {
		t.Errorf("Value = %v, want true", got.Value)
	}
}

func TestStore_Patch_AtomicHardReject(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	// First patch: a valid one.
	first, err := s.Patch(ctx, Patch{Sections: map[string]map[string]json.RawMessage{
		"mail_listeners": {"submission_enabled": json.RawMessage(`true`)},
	}})
	if err != nil || len(first.Applied) != 1 {
		t.Fatalf("first patch failed: %v / applied=%d", err, len(first.Applied))
	}

	// Second patch: one good, one unknown. The whole patch must
	// roll back; the good field must NOT be persisted as a second
	// update.
	second, err := s.Patch(ctx, Patch{Sections: map[string]map[string]json.RawMessage{
		"mail_listeners": {"smtps_enabled": json.RawMessage(`true`)},
		"security":       {"host_name_typo": json.RawMessage(`"evil"`)},
	}})
	if err == nil {
		t.Fatal("hard reject should return an error")
	}
	if !errors.Is(err, ErrUnsafeOrUnknown) {
		t.Errorf("err should be ErrUnsafeOrUnknown, got %v", err)
	}
	if len(second.Applied) != 0 {
		t.Errorf("Applied = %d, want 0 (atomic hard reject)", len(second.Applied))
	}

	// Verify smtps_enabled was NOT written.
	e, err := s.Get(ctx, "mail_listeners.smtps_enabled")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e != nil {
		t.Errorf("smtps_enabled should not be stored after hard reject, got %+v", e)
	}
}

func TestStore_Patch_UpdatedByRecorded(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()
	uid := int64(42)
	body := map[string]map[string]json.RawMessage{
		"security": {"password_min_len": json.RawMessage(`14`)},
	}
	if _, err := s.Patch(ctx, Patch{Sections: body, UpdatedBy: &uid}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	e, err := s.Get(ctx, "security.password_min_len")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e == nil || e.UpdatedBy == nil || *e.UpdatedBy != 42 {
		t.Errorf("UpdatedBy = %v, want 42", e.UpdatedBy)
	}
}

func TestStore_IsForbiddenKey(t *testing.T) {
	cases := []struct {
		key    string
		forbid bool
	}{
		// Writable configuration knobs must NOT be flagged.
		{"mail_listeners.submission_enabled", false},
		{"security.password_min_len", false}, // config knob, not a secret
		{"backup.dir", false},
		{"security.session_ttl_seconds", false},

		// Secret-shaped keys must be rejected.
		{"security.jwt_secret", true},
		{"security.jwt_key_path", true},
		{"license.license_key", true},
		{"vapid.vapid_private_key", true},
		{"vapid.vapid_private_key_file", true},
		{"dns.cloudflare_api_key", true},
		{"api.deepseek_api_key", true},
		{"database.dsn", true},
		{"auth.password_hash", true},
	}
	for _, c := range cases {
		if got := IsForbiddenKey(c.key); got != c.forbid {
			t.Errorf("IsForbiddenKey(%q) = %v, want %v", c.key, got, c.forbid)
		}
	}
}

func TestStore_AllowlistContents(t *testing.T) {
	list := Allowlist()
	// Sanity: must include a few anchor fields.
	for _, k := range []string{
		"mail_listeners.submission_enabled",
		"security.password_min_len",
		"backup.retention_count",
		"dns.public_ipv4",
		"monitoring.disk_usage_warning_pct",
	} {
		if _, ok := list[k]; !ok {
			t.Errorf("allowlist must include %q", k)
		}
	}
	// And must NOT include any of the obvious unsafe keys.
	for _, k := range []string{
		"security.jwt_secret",
		"security.jwt_key_path",
		"license.license_key",
		"vapid.vapid_private_key",
	} {
		if _, ok := list[k]; ok {
			t.Errorf("allowlist must NOT include unsafe %q", k)
		}
	}
}

func TestStore_CoerceType(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		errWant bool
	}{
		{`true`, "bool", false},
		{`false`, "bool", false},
		{`123`, "int", false},
		{`"hello"`, "string", false},
		{`null`, "bool", true},
		{`"abc"`, "int", true},
		{`true`, "int", true},
		{`123`, "string", true},
	}
	for _, c := range cases {
		_, err := coerceType(json.RawMessage(c.raw), c.want)
		if (err != nil) != c.errWant {
			t.Errorf("coerceType(%q, %q) err = %v, wantErr %v", c.raw, c.want, err, c.errWant)
		}
	}
}

func TestStore_FlattenPatch(t *testing.T) {
	in := map[string]map[string]json.RawMessage{
		"a": {"b": json.RawMessage(`1`), "c": json.RawMessage(`"x"`)},
		"d": {"e": json.RawMessage(`true`)},
	}
	out := flattenPatch(in)
	if out["a.b"] == nil || out["a.c"] == nil || out["d.e"] == nil {
		t.Errorf("flatten lost keys: %+v", out)
	}
}
