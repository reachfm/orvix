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
	db.Exec(`CREATE TABLE IF NOT EXISTS platform_jobs (id INTEGER PRIMARY KEY, type TEXT, status TEXT DEFAULT 'queued', progress INTEGER DEFAULT 0, tenant_id INTEGER DEFAULT 0, version INTEGER DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, scope TEXT DEFAULT 'platform', actor TEXT DEFAULT '', payload_version INTEGER DEFAULT 1, run_after DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	db.Exec(`INSERT INTO platform_jobs (id, type, status, created_at, updated_at, run_after) VALUES (1, 'bulk-import', 'queued', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

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
	db.Exec(`CREATE TABLE IF NOT EXISTS platform_support_access_grants (id INTEGER PRIMARY KEY, ticket_ref TEXT, reason TEXT, target_tenant_id INTEGER, granted_by_id INTEGER, permission_scope TEXT, status TEXT, expires_at DATETIME, version INTEGER DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`)
	db.Exec(`INSERT INTO platform_support_access_grants (id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, expires_at, created_at, updated_at) VALUES (1, 'T-1', 'investigation', 1, 1, 'read_only', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

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
