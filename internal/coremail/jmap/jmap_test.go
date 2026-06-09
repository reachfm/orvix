package jmap

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/observability"
	_ "modernc.org/sqlite"
)

// ── Test Infrastructure ─────────────────────────────────────

func testJMAPEnv(t *testing.T) (*coremail.Engine, string, func()) {
	t.Helper()

	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "jmap_test.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	engCfg := coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()}
	eng := coremail.NewEngine(engCfg)

	// Provision a domain and mailbox.
	_, mbox, err := eng.ProvisionDomain(context.Background(), "test.com", "smb",
		"user@test.com", "pass", "Test User", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	_ = mbox

	// Create JMAP server.
	srv := NewServer(eng)
	srv.Hostname = "jmap.test.com"

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()

	go func() {
		srv.srv = &http.Server{
			Addr:    addr,
			Handler: srv.withMiddleware(srv.mux),
		}
		srv.srv.Serve(listener)
	}()

	cleanup := func() { listener.Close() }
	return eng, addr, cleanup
}

func coremailTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_forwarder INTEGER NOT NULL DEFAULT 0,
			forward_to TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			last_ip TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	}
}

func jmapRequest(t *testing.T, addr, method, path, username, password string) (*http.Response, string) {
	t.Helper()
	url := fmt.Sprintf("http://%s%s", addr, path)

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if username != "" && password != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// ── Session Endpoint Tests ─────────────────────────────────

func TestJMAPIAPISessionEndpoint(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, body := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "pass")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var session Session
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatalf("parse session: %v", err)
	}

	if session.Username != "user@test.com" {
		t.Fatalf("expected user@test.com, got %s", session.Username)
	}
	if len(session.Accounts) == 0 {
		t.Fatal("expected at least 1 account")
	}
	if _, ok := session.Capabilities["urn:ietf:params:jmap:core"]; !ok {
		t.Fatal("expected core capability")
	}
	if session.APITURL == "" {
		t.Fatal("expected apiUrl")
	}
}

// ── Capability Advertisement Tests ─────────────────────────

func TestJMAPCoreCapability(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	_, body := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "pass")

	var session Session
	json.Unmarshal([]byte(body), &session)

	core, ok := session.Capabilities["urn:ietf:params:jmap:core"].(map[string]interface{})
	if !ok {
		t.Fatal("core capability not found")
	}

	if core["maxSizeUpload"] == nil {
		t.Fatal("expected maxSizeUpload")
	}
	if core["maxConcurrentUpload"] == nil {
		t.Fatal("expected maxConcurrentUpload")
	}
}

// ── Authentication Tests ───────────────────────────────────

func TestJMAPAuthSuccess(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, _ := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "pass")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestJMAPAuthFailure(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, body := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "wrongpass")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
	}
}

func TestJMAPAuthMissing(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, _ := jmapRequest(t, addr, "GET", "/jmap/session", "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestJMAPAuthUnknownUser(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, _ := jmapRequest(t, addr, "GET", "/jmap/session", "unknown@test.com", "pass")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// ── Account Resolution Tests ───────────────────────────────

func TestJMAPAccountResolution(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	_, body := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "pass")

	var session Session
	json.Unmarshal([]byte(body), &session)

	if len(session.Accounts) == 0 {
		t.Fatal("expected accounts")
	}

	for id, acct := range session.Accounts {
		if id == "" {
			t.Fatal("expected non-empty account ID")
		}
		if !acct.IsPersonal {
			t.Fatal("expected personal account")
		}
		if acct.Name != "user@test.com" {
			t.Fatalf("expected user@test.com, got %s", acct.Name)
		}
	}
}

// ── Well-Known Endpoint Tests ──────────────────────────────

func TestJMAPWellKnown(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, body := jmapRequest(t, addr, "GET", "/.well-known/jmap", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	if !strings.Contains(body, "sessionUrl") {
		t.Fatal("expected sessionUrl in well-known")
	}
	if !strings.Contains(body, "apiUrl") {
		t.Fatal("expected apiUrl in well-known")
	}
}

// ── Observability Tests ────────────────────────────────────

func TestJMAPObservabilityEvents(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncIMAPLoginSuccess()
	obs.Metrics.IncIMAPLoginFailure()

	snap := obs.Metrics.Snapshot()
	if snap.IMAPLoginSuccess != 1 {
		t.Fatalf("expected 1 login success, got %d", snap.IMAPLoginSuccess)
	}
	if snap.IMAPLoginFailure != 1 {
		t.Fatalf("expected 1 login failure, got %d", snap.IMAPLoginFailure)
	}
}

// ── Concurrency Tests ───────────────────────────────────────

func TestJMAPConcurrentRequests(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := jmapRequest(t, addr, "GET", "/jmap/session", "user@test.com", "pass")
			var session Session
			if err := json.Unmarshal([]byte(body), &session); err != nil {
				errs <- err
			}
			if session.Username != "user@test.com" {
				errs <- fmt.Errorf("wrong username: %s", session.Username)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}

func TestJMAPWellKnownUnauthenticated(t *testing.T) {
	_, addr, cleanup := testJMAPEnv(t)
	defer cleanup()

	resp, body := jmapRequest(t, addr, "GET", "/.well-known/jmap", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}
