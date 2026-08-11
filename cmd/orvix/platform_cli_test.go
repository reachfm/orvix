package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) (*sql.DB, *dbdialect.Info, func() error) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	dial := dbdialect.FromDriver("sqlite")
	return db, dial, func() error { return db.Close() }
}

func platformTestDeps(t *testing.T, db *sql.DB, dial *dbdialect.Info) (platformCLIDeps, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return platformCLIDeps{
		openDB: func() (*sql.DB, *dbdialect.Info, func() error, error) {
			return db, dial, func() error { return nil }, nil
		},
		now:    func() time.Time { return time.Now().UTC() },
		stdout: stdout,
		stderr: stderr,
	}, stdout, stderr
}

func TestOrgsList(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, deleted_at DATETIME)`)
	db.Exec(`INSERT INTO tenants (id, name, domain, plan, active) VALUES (1, 'test', 't.com', 'enterprise', 1)`)

	if code := runPlatform([]string{"orgs", "list", "--json"}, deps); code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	var list []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 org, got %d", len(list))
	}
}

func TestOrgsGet(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, deleted_at DATETIME)`)
	db.Exec(`INSERT INTO tenants (id, name, domain, plan, active) VALUES (1, 'test', 't.com', 'enterprise', 1)`)

	if code := runPlatform([]string{"orgs", "get", "--id", "1"}, deps); code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	if !strings.Contains(stdout.String(), "test") {
		t.Fatalf("want test in output: %s", stdout.String())
	}
}

func TestOrgsNotFound(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, deleted_at DATETIME)`)

	code := runPlatform([]string{"orgs", "get", "--id", "999"}, deps)
	if code != exitNotFound {
		t.Fatalf("want not_found(3), got %d", code)
	}
}

func TestOrgsConfirmRefused(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, deleted_at DATETIME)`)
	db.Exec(`INSERT INTO tenants (id, name, domain, plan, active) VALUES (1, 'test', 't.com', 'enterprise', 1)`)

	code := runPlatform([]string{"orgs", "suspend", "--id", "1", "--reason", "billing", "--confirm", "WRONG"}, deps)
	if code != exitConfirmRefused {
		t.Fatalf("want confirm_refused(5), got %d", code)
	}
}

func TestJobsList(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)
	// Canonical platform_jobs DDL (from internal/platform/jobs/repository.go)
	db.Exec(`CREATE TABLE IF NOT EXISTS platform_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		scope TEXT NOT NULL DEFAULT 'platform',
		actor TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL,
		payload_version INTEGER NOT NULL DEFAULT 1,
		payload TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'queued',
		progress INTEGER NOT NULL DEFAULT 0,
		attempt_count INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 3,
		run_after DATETIME NOT NULL,
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_token TEXT NOT NULL DEFAULT '',
		lease_version INTEGER NOT NULL DEFAULT 0,
		lease_expires_at DATETIME,
		heartbeat_at DATETIME,
		cancellation_requested_at DATETIME,
		created_at DATETIME NOT NULL,
		started_at DATETIME,
		completed_at DATETIME,
		cancelled_at DATETIME,
		result TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '',
		idempotency_scope TEXT NOT NULL DEFAULT '',
		request_hash TEXT NOT NULL DEFAULT '',
		manual_retry_key TEXT NOT NULL DEFAULT '',
		correlation_id TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1,
		updated_at DATETIME NOT NULL
	)`)
	now := time.Now().UTC()
	db.Exec(`INSERT INTO platform_jobs (id, type, status, run_after, created_at, updated_at) VALUES (1, 'bulk-import', 'queued', ?, ?, ?)`, now, now, now)

	if code := runPlatform([]string{"jobs", "list", "--json"}, deps); code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	var list []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("want at least 1 job")
	}
}

func TestIncidentsCreate(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)

	if code := runPlatform([]string{"incidents", "create", "--title", "Queue Issue", "--severity", "minor"}, deps); code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	// Output should contain the incident ID
	if !strings.Contains(stdout.String(), "created") {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestIncidentsInvalidTransition(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)

	runPlatform([]string{"incidents", "create", "--title", "Test", "--severity", "minor"}, deps)
	// resolve the incident
	runPlatform([]string{"incidents", "resolve", "--id", "1", "--message", "done"}, deps)
	// try to update a resolved incident (must fail)
	code := runPlatform([]string{"incidents", "update", "--id", "1", "--status", "identified"}, deps)
	if code != exitForbidden {
		t.Fatalf("want forbidden(4), got %d", code)
	}
}

func TestSupportRevoke(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)
	// Canonical support access grants DDL (from internal/supportaccess/repository.go)
	db.Exec(`CREATE TABLE IF NOT EXISTS platform_support_access_grants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ticket_ref TEXT NOT NULL,
		reason TEXT NOT NULL,
		target_tenant_id INTEGER NOT NULL,
		granted_by_id INTEGER NOT NULL,
		permission_scope TEXT NOT NULL DEFAULT 'read_only',
		status TEXT NOT NULL DEFAULT 'requested',
		activated_at DATETIME,
		expires_at DATETIME NOT NULL,
		revoked_at DATETIME,
		revoke_reason TEXT NOT NULL DEFAULT '',
		emergency_break_glass INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	expires := time.Now().UTC().Add(1 * time.Hour)
	created := time.Now().UTC()
	db.Exec(`INSERT INTO platform_support_access_grants (id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, expires_at, created_at, updated_at) VALUES (1, 'T-1', 'investigation', 1, 1, 'read_only', 'active', ?, ?, ?)`, expires, created, created)

	code := runPlatform([]string{"support", "revoke", "--id", "1", "--reason", "done", "--confirm", "REVOKE-1"}, deps)
	if code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	_ = stdout
}

func TestConfigJSON(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)

	if code := runPlatform([]string{"config", "list", "--json"}, deps); code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	var list []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("json: %v", err)
	}
	// Check secret redaction
	for _, s := range list {
		if key, _ := s["key"].(string); key == "jwt.secret" {
			if s["value"] != "REDACTED" {
				t.Fatalf("want REDACTED, got %v", s["value"])
			}
		}
	}
}

func TestBadArgs(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)

	if code := runPlatform([]string{"nonexistent"}, deps); code != exitBadArgs {
		t.Fatalf("want bad_args(2), got %d", code)
	}
	if code := runPlatform([]string{}, deps); code != exitBadArgs {
		t.Fatalf("want bad_args(2), got %d", code)
	}
}

func TestJSONOutputValid(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)

	code := runPlatform([]string{"capabilities", "--json"}, deps)
	if code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("want valid json: %v", err)
	}
}

func TestExitCodes(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, deleted_at DATETIME)`)

	tests := []struct {
		args []string
		want int
	}{
		{[]string{"orgs", "get", "--id", "999"}, exitNotFound},
		{[]string{"orgs", "get", "--id", "abc"}, exitBadArgs},
		{[]string{"orgs", "suspend", "--id", "1", "--reason", "t", "--confirm", "WRONG"}, exitConfirmRefused},
		{[]string{}, exitBadArgs},
	}
	for _, tt := range tests {
		code := runPlatform(tt.args, deps)
		if code != tt.want {
			t.Errorf("args=%v: want %d, got %d", tt.args, tt.want, code)
		}
	}
}

func TestAPIKeysCreate(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, stdout, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		name TEXT NOT NULL, user_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		role TEXT NOT NULL DEFAULT 'user', key_hash TEXT NOT NULL,
		key_prefix TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1, last_used_at DATETIME,
		expires_at DATETIME, deleted_at DATETIME, allowed_ips TEXT NOT NULL DEFAULT ''
	)`)

	code := runPlatform([]string{"apikeys", "create", "--name", "ci-key", "--user-id", "1", "--tenant-id", "1", "--scopes", "read,write", "--confirm", "CREATE-KEY"}, deps)
	if code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "orv_") {
		t.Fatalf("want plaintext key in output: %s", out)
	}
	var hash string
	if err := db.QueryRow("SELECT key_hash FROM api_keys WHERE name='ci-key'").Scan(&hash); err != nil {
		t.Fatalf("key not stored: %v", err)
	}
	if hash == "" {
		t.Fatal("key hash is empty")
	}
}

func TestAPIKeysCreateConfirmRefused(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)

	code := runPlatform([]string{"apikeys", "create", "--name", "k", "--user-id", "1", "--tenant-id", "1", "--confirm", "WRONG"}, deps)
	if code != exitConfirmRefused {
		t.Fatalf("want confirm_refused(5), got %d", code)
	}
}

func TestAPIKeysRevoke(t *testing.T) {
	db, dial, closeFn := testDB(t)
	defer closeFn()
	deps, _, _ := platformTestDeps(t, db, dial)
	db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		name TEXT NOT NULL, user_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		role TEXT NOT NULL DEFAULT 'user', key_hash TEXT NOT NULL,
		key_prefix TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1, last_used_at DATETIME,
		expires_at DATETIME, deleted_at DATETIME, allowed_ips TEXT NOT NULL DEFAULT ''
	)`)
	db.Exec(`INSERT INTO api_keys (name, user_id, tenant_id, role, key_hash, key_prefix, scopes, active, created_at, updated_at) VALUES ('k', 1, 1, 'user', 'h', 'orv_abc', 'read', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	code := runPlatform([]string{"apikeys", "revoke", "--id", "1", "--user-id", "1", "--reason", "compromised", "--confirm", "REVOKE-1"}, deps)
	if code != exitSuccess {
		t.Fatalf("want success, got %d", code)
	}
	var active int
	if err := db.QueryRow("SELECT active FROM api_keys WHERE id=1").Scan(&active); err != nil {
		t.Fatalf("query: %v", err)
	}
	if active != 0 {
		t.Fatal("key was not revoked")
	}
}
