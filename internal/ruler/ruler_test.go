package ruler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/rulertypes"
	"go.uber.org/zap"
)

// openTestDB creates an in-memory SQLite database with
// the rule tables we exercise. The migrations live in
// internal/models/migrations and are pasted here
// intentionally so the ruler package does not depend
// on the wider migration pipeline during tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ruler.db")+"?_loc=auto&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE coremail_acceptance_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			sender_pattern TEXT NOT NULL DEFAULT '',
			recipient_pattern TEXT NOT NULL DEFAULT '',
			source_ip_cidr TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'accept',
			redirect_to TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_incoming_msg_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			field TEXT NOT NULL DEFAULT 'subject',
			operator TEXT NOT NULL DEFAULT 'contains',
			value TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'move',
			action_target TEXT NOT NULL DEFAULT '',
			apply_to TEXT NOT NULL DEFAULT 'all',
			stop_processing INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	return db
}

func newEngine(t *testing.T, db *sql.DB) *Engine {
	t.Helper()
	obs := observability.NewObservability(50, 50)
	return New(db, zap.NewNop(), obs)
}

// seedAcceptance inserts a single acceptance rule row.
func seedAcceptance(t *testing.T, db *sql.DB, name string, priority int, enabled bool, scope, scopeTarget, sender, recipient, ipCIDR, action string) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	en := 0
	if enabled {
		en = 1
	}
	_, err := db.Exec(`INSERT INTO coremail_acceptance_rules
		(tenant_id, name, priority, enabled, scope, scope_target, sender_pattern, recipient_pattern, source_ip_cidr, action, redirect_to, note, created_at, updated_at)
		VALUES (0, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)`,
		name, priority, en, scope, scopeTarget, sender, recipient, ipCIDR, action, now, now)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func seedIncoming(t *testing.T, db *sql.DB, name string, priority int, enabled bool, field, op, value, action, target, applyTo string) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	en := 0
	if enabled {
		en = 1
	}
	_, err := db.Exec(`INSERT INTO coremail_incoming_msg_rules
		(tenant_id, name, priority, enabled, field, operator, value, action, action_target, apply_to, stop_processing, note, created_at, updated_at)
		VALUES (0, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?)`,
		name, priority, en, field, op, value, action, target, applyTo, now, now)
	if err != nil {
		t.Fatalf("seed incoming: %v", err)
	}
}

func TestAcceptanceRejectsBySenderPattern(t *testing.T) {
	db := openTestDB(t)
	seedAcceptance(t, db, "drop_spammer", 50, true, "global", "", "*@spam.example", "", "", "reject")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, action, reason := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spam.example", Recipient: "inbox@x.local", SourceIP: "192.0.2.1",
	})
	if !matched {
		t.Fatalf("want match")
	}
	if action != "reject" {
		t.Fatalf("want reject, got %q", action)
	}
	if reason == "" {
		t.Fatalf("want reason")
	}
}

func TestAcceptanceHigherPriorityWins(t *testing.T) {
	db := openTestDB(t)
	// Lower priority number = earlier in the list. Both
	// match the same envelope. The first (priority=10)
	// must win — the receiver never sees priority=99.
	seedAcceptance(t, db, "allow", 10, true, "global", "", "", "", "", "accept")
	seedAcceptance(t, db, "reject", 99, true, "global", "", "", "", "", "reject")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, action, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "x@x.local", Recipient: "y@y.local",
	})
	if !matched {
		t.Fatalf("want match")
	}
	if action != "accept" {
		t.Fatalf("want accept (priority-10 wins), got %q", action)
	}
}

func TestAcceptanceDisabledRuleIgnored(t *testing.T) {
	db := openTestDB(t)
	seedAcceptance(t, db, "drop", 50, false /* disabled */, "global", "", "*@spam.example", "", "", "reject")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, _, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spam.example", Recipient: "inbox@x.local",
	})
	if matched {
		t.Fatalf("disabled rule must not match")
	}
}

func TestAcceptanceTenantIsolation(t *testing.T) {
	db := openTestDB(t)
	// The seeded rule has tenant_id = 1. The query
	// carries tenant_id = 0 from a not-yet-resolved
	// receive; the rule MUST NOT match.
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`INSERT INTO coremail_acceptance_rules
		(tenant_id, name, priority, enabled, scope, scope_target, sender_pattern, recipient_pattern, source_ip_cidr, action, redirect_to, note, created_at, updated_at)
		VALUES (1, 'tenant_rule', 50, 1, 'global', '', '*@spam.example', '', '', 'reject', '', '', ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, _, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spam.example", Recipient: "inbox@x.local", TenantID: 0,
	})
	if matched {
		t.Fatalf("tenant-1 rule must not match query with tenant=0")
	}
	matched, act, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spam.example", Recipient: "inbox@x.local", TenantID: 1,
	})
	if !matched || act != "reject" {
		t.Fatalf("tenant-1 query must match tenant-1 rule")
	}
}

func TestAcceptanceAllowOverrideAccept(t *testing.T) {
	db := openTestDB(t)
	seedAcceptance(t, db, "from_bad_sender", 10, true, "global", "", "spammer@bad.local", "", "", "reject")
	seedAcceptance(t, db, "vip_allow", 5, true, "global", "", "*@vip.local", "", "", "accept")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, action, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "ceo@vip.local", Recipient: "inbox@x.local",
	})
	if !matched || action != "accept" {
		t.Fatalf("vip allow must win over global reject")
	}
}

func TestAcceptanceIPCIDR(t *testing.T) {
	db := openTestDB(t)
	seedAcceptance(t, db, "ip_block", 50, true, "global", "", "", "", "203.0.113.0/24", "reject")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()

	matched, action, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "x@x.local", Recipient: "y@y.local", SourceIP: "203.0.113.42:1234",
	})
	if !matched || action != "reject" {
		t.Fatalf("CIDR match must fire")
	}
	matched, _, _ = e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "x@x.local", Recipient: "y@y.local", SourceIP: "198.51.100.10:1234",
	})
	if matched {
		t.Fatalf("non-CIDR address must not match")
	}
}

func TestIncomingSubjectReject(t *testing.T) {
	db := openTestDB(t)
	seedIncoming(t, db, "block_phish", 50, true, "subject", "contains", "urgent verify", "reject", "", "all")
	e := newEngine(t, db)
	e.MarkIncomingEnforced()

	matched, action, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Sender: "x@x.local", Recipient: "y@y.local", Subject: "URGENT verify your account",
	})
	if !matched || action != "reject" {
		t.Fatalf("subject reject must fire")
	}
}

func TestIncomingNoMatchDefaultsTrueNoDecision(t *testing.T) {
	db := openTestDB(t)
	seedIncoming(t, db, "block_phish", 50, true, "subject", "contains", "phish", "reject", "", "all")
	e := newEngine(t, db)
	e.MarkIncomingEnforced()
	matched, _, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Subject: "Hello friend",
	})
	if matched {
		t.Fatalf("non-match must not commit to a decision")
	}
}

func TestIncomingTagAction(t *testing.T) {
	db := openTestDB(t)
	seedIncoming(t, db, "tag_newsletter", 50, true, "subject", "contains", "newsletter", "tag", "Newsletter", "all")
	e := newEngine(t, db)
	e.MarkIncomingEnforced()
	matched, action, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Subject: "Weekly newsletter from X",
	})
	if !matched || action != "tag" {
		t.Fatalf("tag action: matched=%v action=%q", matched, action)
	}
}

func TestIncomingPriorityOrder(t *testing.T) {
	db := openTestDB(t)
	seedIncoming(t, db, "drop", 99, true, "subject", "contains", "spam", "reject", "", "all")
	seedIncoming(t, db, "keep", 10, true, "subject", "contains", "spam", "label", "Marketing", "all")
	e := newEngine(t, db)
	e.MarkIncomingEnforced()
	matched, action, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Subject: "this is spam",
	})
	if !matched || action != "label" {
		t.Fatalf("priority=10 keep must win, got %v/%q", matched, action)
	}
}

func TestIncomingTenantIsolation(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`INSERT INTO coremail_incoming_msg_rules
		(tenant_id, name, priority, enabled, field, operator, value, action, action_target, apply_to, stop_processing, note, created_at, updated_at)
		VALUES (7, 't7_rule', 50, 1, 'subject', 'contains', 'X', 'reject', '', 'all', 0, '', ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e := newEngine(t, db)
	e.MarkIncomingEnforced()
	matched, _, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Subject: "X", TenantID: 0,
	})
	if matched {
		t.Fatalf("tenant-7 rule must not match tenant-0 query")
	}
	matched, action, _ := e.EvaluateIncoming(context.Background(), rulertypes.Query{
		Subject: "X", TenantID: 7,
	})
	if !matched || action != "reject" {
		t.Fatalf("tenant-7 query must match tenant-7 rule")
	}
}

func TestReloadInvalidatesCache(t *testing.T) {
	db := openTestDB(t)
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()
	// First eval: nothing seeded, must NOT match.
	matched, _, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "x@x.local", Recipient: "y@y.local",
	})
	if matched {
		t.Fatalf("baseline: must not match")
	}
	seedAcceptance(t, db, "new_rule", 50, true, "global", "", "*@spammer.local", "", "", "reject")
	// Cache is now stale. Evaluate must NOT match.
	matched, _, _ = e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spammer.local", Recipient: "inbox@x.local",
	})
	if matched {
		t.Fatalf("must not see new rule before Reload")
	}
	// Reload, then eval must match.
	e.Reload(context.Background())
	matched, action, _ := e.EvaluateAcceptance(context.Background(), rulertypes.Query{
		Sender: "u@spammer.local", Recipient: "inbox@x.local",
	})
	if !matched || action != "reject" {
		t.Fatalf("after Reload: rule must fire, got %v/%q", matched, action)
	}
}

func TestAcceptanceEnvelopeLevelMatchesRPC(t *testing.T) {
	// The dry-run endpoint accepts the same envelope
	// input the runtime sees at MAIL FROM / RCPT TO.
	// We construct the same query the runtime would
	// build and assert the rule-engine result matches
	// the dry-run result by sampling the same row.
	db := openTestDB(t)
	seedAcceptance(t, db, "drop", 50, true, "global", "", "*@spam.example", "", "", "reject")
	e := newEngine(t, db)
	e.MarkAcceptanceEnforced()
	e2 := newEngine(t, db) // pretend the dry-run endpoint
	e2.MarkAcceptanceEnforced()

	q := rulertypes.Query{
		Sender: "u@spam.example", Recipient: "inbox@x.local",
		SourceIP: "192.0.2.1",
	}
	ok1, act1, _ := e.EvaluateAcceptance(context.Background(), q)
	ok2, act2, _ := e2.EvaluateAcceptance(context.Background(), q)
	if ok1 != ok2 || act1 != act2 {
		t.Fatalf("dry-run must match runtime: %v/%q vs %v/%q", ok1, act1, ok2, act2)
	}
}
