package mailpolicy

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	_ "modernc.org/sqlite"
)

// policyTestDB builds a real SQLite database with the canonical
// coremail schema, a domain, and mailboxes so the EngineStore is
// exercised against real repository code — not mocks.
func policyTestDB(t *testing.T) (*sql.DB, *coremail.Engine) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE coremail_domains (
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
			default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_mailboxes (
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
			mail_access_mode TEXT NOT NULL DEFAULT 'inherit',
			version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_aliases (
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db})
	return db, eng
}

func seedPolicyMailbox(t *testing.T, db *sql.DB, email, mode string, domainMode string) {
	t.Helper()
	parts := splitAt(email)
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, mail_access_mode, created_at, updated_at) VALUES (?, 1, 'active', ?, datetime('now'), datetime('now'))
		 ON CONFLICT(name) DO NOTHING`,
		parts[1], domainMode); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	var domainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name=?`, parts[1]).Scan(&domainID); err != nil {
		t.Fatalf("domain id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, auth_scheme, status, mail_access_mode, created_at, updated_at)
		 VALUES (?, 1, ?, ?, '', 'argon2id', 'active', ?, datetime('now'), datetime('now'))`,
		domainID, parts[0], email, mode); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
}

func seedPolicyAlias(t *testing.T, db *sql.DB, fromAddr, toAddr string) {
	t.Helper()
	parts := splitAt(fromAddr)
	var domainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name=?`, parts[1]).Scan(&domainID); err != nil {
		t.Fatalf("alias domain id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
		 VALUES (?, 1, ?, ?, 1, datetime('now'), datetime('now'))`,
		domainID, fromAddr, toAddr); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
}

func splitAt(email string) [2]string {
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			return [2]string{email[:i], email[i+1:]}
		}
	}
	return [2]string{email, ""}
}

func TestEngineStore_SenderIdentityEffectiveMode(t *testing.T) {
	db, eng := policyTestDB(t)
	seedPolicyMailbox(t, db, "alice@internal.test", string(ModeInternalOnly), string(ModeInternalExternal))
	seedPolicyMailbox(t, db, "bob@inherit.test", string(ModeInherit), string(ModeInternalOnly))
	seedPolicyMailbox(t, db, "carol@open.test", string(ModeInternalExternal), string(ModeInternalOnly))

	store := &EngineStore{Engine: eng}

	id, err := store.SenderIdentity(context.Background(), "alice@internal.test")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	if id.EffectiveMode != ModeInternalOnly {
		t.Fatalf("alice effective=%s want internal_only", id.EffectiveMode)
	}

	id, err = store.SenderIdentity(context.Background(), "bob@inherit.test")
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if id.EffectiveMode != ModeInternalOnly {
		t.Fatalf("bob (inherit -> internal_only domain) effective=%s want internal_only", id.EffectiveMode)
	}

	id, err = store.SenderIdentity(context.Background(), "carol@open.test")
	if err != nil {
		t.Fatalf("carol: %v", err)
	}
	if id.EffectiveMode != ModeInternalExternal {
		t.Fatalf("carol effective=%s want internal_external", id.EffectiveMode)
	}

	if _, err := store.SenderIdentity(context.Background(), "ghost@nowhere.test"); err != ErrSenderUnknown {
		t.Fatalf("ghost: want ErrSenderUnknown, got %v", err)
	}
}

func TestEngineStore_RecipientIsLocal(t *testing.T) {
	db, eng := policyTestDB(t)
	seedPolicyMailbox(t, db, "alice@internal.test", string(ModeInternalOnly), string(ModeInternalExternal))
	seedPolicyAlias(t, db, "team@internal.test", "alice@internal.test")

	store := &EngineStore{Engine: eng}
	if local, err := store.RecipientIsLocal(context.Background(), "alice@internal.test"); err != nil || !local {
		t.Fatalf("direct mailbox must be local: %v %v", local, err)
	}
	if local, err := store.RecipientIsLocal(context.Background(), "team@internal.test"); err != nil || !local {
		t.Fatalf("alias must be local: %v %v", local, err)
	}
	if local, err := store.RecipientIsLocal(context.Background(), "external@other.test"); err != nil || local {
		t.Fatalf("external address must not be local: %v %v", local, err)
	}
}

func TestEngineStore_RecipientEffectiveModeMostRestrictive(t *testing.T) {
	db, eng := policyTestDB(t)
	seedPolicyMailbox(t, db, "alice@internal.test", string(ModeInternalOnly), string(ModeInternalExternal))
	seedPolicyMailbox(t, db, "carol@open.test", string(ModeInternalExternal), string(ModeInternalExternal))
	// Alias fan-out: one internal-only target makes the whole alias
	// internal-only (most restrictive wins).
	seedPolicyAlias(t, db, "team@internal.test", "alice@internal.test,carol@open.test")

	store := &EngineStore{Engine: eng}
	eff, err := store.RecipientEffectiveMode(context.Background(), "team@internal.test")
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	if eff.Effective != ModeInternalOnly {
		t.Fatalf("alias effective=%s want internal_only (most restrictive)", eff.Effective)
	}

	eff, err = store.RecipientEffectiveMode(context.Background(), "carol@open.test")
	if err != nil {
		t.Fatalf("carol: %v", err)
	}
	if eff.Effective != ModeInternalExternal {
		t.Fatalf("carol effective=%s want internal_external", eff.Effective)
	}

	if _, err := store.RecipientEffectiveMode(context.Background(), "ghost@internal.test"); err != ErrRecipientUnknown {
		t.Fatalf("ghost: want ErrRecipientUnknown, got %v", err)
	}
}

func TestEngineStore_CorruptDomainFailsClosed(t *testing.T) {
	db, eng := policyTestDB(t)
	seedPolicyMailbox(t, db, "alice@corrupt.test", string(ModeInherit), "external_only") // corrupt domain value

	store := &EngineStore{Engine: eng}
	id, err := store.SenderIdentity(context.Background(), "alice@corrupt.test")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if id.EffectiveMode != ModeInternalOnly {
		t.Fatalf("corrupt domain effective=%s want internal_only (fail closed)", id.EffectiveMode)
	}
}
