package runtime

// Phase 8 C3A: domain operability enforcement for SMTP RCPT
// acceptance. checkRecipientDomainOperability is the canonical guard
// wired into smtpMailAccessPolicy (see module.go); it is unexported,
// so these tests live in-package rather than doing a full TCP/session
// simulation — they exercise the exact function the protocol handler
// calls, with the same *sql.DB-backed lookup it uses in production.

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func rcptTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "rcpt_operability_test.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}
	return db
}

func seedRcptDomain(t *testing.T, db *sql.DB, name, status string, deleted bool) {
	t.Helper()
	var deletedAt interface{}
	if deleted {
		deletedAt = "2020-01-01 00:00:00"
	}
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, deleted_at) VALUES (1, ?, ?, ?)`,
		name, status, deletedAt); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
}

func TestCheckRecipientDomainOperability_ActiveDomainAllowed(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "active.example", "active", false)
	m := &Module{db: db}

	allow, reason, err := m.checkRecipientDomainOperability(t.Context(), "user@active.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allow {
		t.Fatalf("expected active domain recipient to be allowed, got reason=%q", reason)
	}
}

func TestCheckRecipientDomainOperability_DisabledSuspendedLockedRejected(t *testing.T) {
	for _, status := range []string{"disabled", "suspended", "locked"} {
		t.Run(status, func(t *testing.T) {
			db := rcptTestDB(t)
			domainName := status + ".example"
			seedRcptDomain(t, db, domainName, status, false)
			m := &Module{db: db}

			allow, reason, err := m.checkRecipientDomainOperability(t.Context(), "user@"+domainName)
			if err != nil {
				t.Fatalf("unexpected error (must be a policy denial, not an infra error): %v", err)
			}
			if allow {
				t.Fatalf("expected a %s domain recipient to be rejected", status)
			}
			if reason == "" {
				t.Fatalf("expected a non-empty rejection reason")
			}
			// Requirement #8: never reveal tenant identity or the
			// domain's real status/name via the reason string.
			if reason != "recipient domain unavailable" {
				t.Fatalf("rejection reason must not leak domain state, got %q", reason)
			}
		})
	}
}

func TestCheckRecipientDomainOperability_UnknownDomainPreservesAntiEnumeration(t *testing.T) {
	db := rcptTestDB(t)
	m := &Module{db: db}

	allow, reason, err := m.checkRecipientDomainOperability(t.Context(), "user@never-registered.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allow {
		t.Fatalf("an unknown domain must not be distinguished from a known/active one here (reason=%q) — the isLocalDomain gate upstream is the real existence check", reason)
	}
}

func TestCheckRecipientDomainOperability_SoftDeletedDomainTreatedAsUnknown(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "gone.example", "active", true)
	m := &Module{db: db}

	allow, _, err := m.checkRecipientDomainOperability(t.Context(), "user@gone.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allow {
		t.Fatalf("a soft-deleted domain row must not be distinguishable from a never-registered one")
	}
}

func TestCheckRecipientDomainOperability_InfrastructureFailureIsTransientNotAcceptance(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "active.example", "active", false)
	m := &Module{db: db}
	db.Close() // simulate a repository failure on the very next query

	allow, _, err := m.checkRecipientDomainOperability(t.Context(), "user@active.example")
	if allow {
		t.Fatalf("an infrastructure failure must never silently accept the recipient")
	}
	if err != errPolicyUnavailable {
		t.Fatalf("an infrastructure failure must map to errPolicyUnavailable (SMTP 4.x transient), got %v", err)
	}
}

// TestCheckRecipientDomainOperability_IgnoresSenderEntirely proves
// spoofing MAIL FROM cannot bypass the guard: the function's only
// input is the RCPT address, so a disabled sender-side domain (which
// would appear in MAIL FROM) has no bearing at all — only the
// recipient's own domain status matters.
func TestCheckRecipientDomainOperability_IgnoresSenderEntirely(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "active-recipient.example", "active", false)
	seedRcptDomain(t, db, "disabled-sender.example", "disabled", false)
	m := &Module{db: db}

	// A RCPT to the active domain must be allowed no matter what a
	// spoofable MAIL FROM on the disabled domain might claim — the
	// function has no sender parameter at all, so this is enforced by
	// construction, not by a runtime check.
	allow, _, err := m.checkRecipientDomainOperability(t.Context(), "user@active-recipient.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allow {
		t.Fatalf("recipient's own active domain must be allowed regardless of any other domain's status")
	}
}

func TestCheckRecipientDomainOperability_NilDBPreservesLegacyBehavior(t *testing.T) {
	m := &Module{}
	allow, reason, err := m.checkRecipientDomainOperability(t.Context(), "user@anything.example")
	if err != nil || !allow || reason != "" {
		t.Fatalf("expected a no-op allow when db is nil, got allow=%v reason=%q err=%v", allow, reason, err)
	}
}
