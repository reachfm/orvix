package mailbox

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

// ── Mailbox access-mode service tests (MAILBOX-ACCESS-MODE-PHASE1) ─

func seedAccessModeDomain(t *testing.T, db *sql.DB, tenantID uint, name, mode string) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := db.Exec(
		`INSERT INTO coremail_domains (tenant_id, name, status, mail_access_mode, created_at, updated_at) VALUES (?, ?, 'active', ?, ?, ?)`,
		tenantID, name, mode, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func TestCreateMailbox_OmittedModePersistsInherit(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)

	// Legacy tenant-admin shape: no mail_access_mode field at all.
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "legacy@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Mailbox.MailAccessMode != string(MailAccessInherit) {
		t.Fatalf("omitted mode must persist inherit, got %q", created.Mailbox.MailAccessMode)
	}
	var stored string
	if err := db.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored mode: %v", err)
	}
	if stored != string(MailAccessInherit) {
		t.Fatalf("stored mode must be inherit, got %q", stored)
	}
}

func TestCreateMailbox_ExplicitModePersisted(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)

	mode := string(MailAccessInternalOnly)
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "restricted@example.test", Password: "InitialPassword123!", MailAccessMode: &mode,
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Mailbox.MailAccessMode != string(MailAccessInternalOnly) {
		t.Fatalf("explicit mode must persist, got %q", created.Mailbox.MailAccessMode)
	}
	var stored string
	if err := db.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored mode: %v", err)
	}
	if stored != string(MailAccessInternalOnly) {
		t.Fatalf("stored mode mismatch: %q", stored)
	}
}

func TestCreateMailbox_InvalidModeRejected(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)

	for _, bad := range []string{"external_only", "open", "inherit_external", "internal", ""} {
		mode := bad
		_, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
			Email:    "x" + strings.ReplaceAll(bad, " ", "_") + "@example.test",
			Password: "InitialPassword123!", MailAccessMode: &mode,
		}, 1)
		if err == nil {
			t.Fatalf("mode %q must be rejected", bad)
		}
	}

	// A normalized "inherit" (even with surrounding whitespace) is a
	// legal persisted value and must be accepted.
	inherited := " inherit "
	if _, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "x-inherit@example.test", Password: "InitialPassword123!", MailAccessMode: &inherited,
	}, 1); err != nil {
		t.Fatalf("normalized inherit must be accepted, got %v", err)
	}
}

func TestEffectiveModeResolution_InheritThroughDomain(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	domainID := seedAccessModeDomain(t, db, 1, "internal.test", string(MailAccessInternalOnly))

	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, mail_access_mode, version, created_at, updated_at)
		 VALUES (?, 1, 'alice', 'alice@internal.test', 'Alice', 'h', 'argon2id', 'active', 1024, 'inherit', 1, ?, ?)`,
		domainID, now, now); err != nil {
		t.Fatal(err)
	}
	var id uint
	if err := db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email='alice@internal.test'`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	m, err := svc.GetMailbox(context.Background(), id, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.MailAccessMode != string(MailAccessInherit) {
		t.Fatalf("configured=%q want inherit", m.MailAccessMode)
	}
	if m.EffectiveMailAccessMode != string(MailAccessInternalOnly) {
		t.Fatalf("effective=%q want internal_only (inherited from domain)", m.EffectiveMailAccessMode)
	}
}

func TestEffectiveModeResolution_ExplicitOverridesDomain(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	domainID := seedAccessModeDomain(t, db, 1, "open.test", string(MailAccessInternalExternal))
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, mail_access_mode, version, created_at, updated_at)
		 VALUES (?, 1, 'alice', 'alice@open.test', 'Alice', 'h', 'argon2id', 'active', 1024, 'internal_only', 1, ?, ?)`,
		domainID, now, now); err != nil {
		t.Fatal(err)
	}
	var id uint
	if err := db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email='alice@open.test'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	cfg, eff, v, err := svc.MailAccessModeState(context.Background(), id, 1)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if cfg != string(MailAccessInternalOnly) || eff != string(MailAccessInternalOnly) || v != 1 {
		t.Fatalf("cfg=%q eff=%q version=%d", cfg, eff, v)
	}
}

func TestSetMailAccessMode_SuccessBumpsVersion(t *testing.T) {
	db := newMailboxTestDB(t)
	store := audit.NewExtendedStore(db)
	if err := store.EnsureTable(context.Background()); err != nil {
		t.Fatalf("audit table: %v", err)
	}
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), store, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	mode := string(MailAccessInternalExternal)
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "alice@example.test", Password: "InitialPassword123!", MailAccessMode: &mode,
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	cfg, eff, newVersion, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, string(MailAccessInternalOnly), 1)
	if err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if cfg != string(MailAccessInternalOnly) || eff != string(MailAccessInternalOnly) {
		t.Fatalf("cfg=%q eff=%q", cfg, eff)
	}
	if newVersion != 2 {
		t.Fatalf("version=%d want 2", newVersion)
	}

	// Audit evidence was written.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='mailbox.mail_access_mode.set'`).Scan(&count); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit record, got %d", count)
	}
}

func TestSetMailAccessMode_StaleVersionConflict(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "alice@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, _, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, string(MailAccessInternalOnly), 1); err != nil {
		t.Fatalf("first set: %v", err)
	}
	// The version has moved to 2; retrying with 1 must conflict.
	if _, _, _, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, string(MailAccessInternalOnly), 1); err == nil {
		t.Fatal("stale version must conflict")
	}
	// The stored mode must be unchanged from the first successful set.
	var stored string
	if err := db.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(MailAccessInternalOnly) {
		t.Fatalf("mode changed by a conflicted write: %q", stored)
	}
}

func TestSetMailAccessMode_CrossTenantNotFound(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "alice@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Another tenant must resolve to not-found, never a conflict, and
	// never disclose the mailbox.
	if _, _, _, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 999, string(MailAccessInternalOnly), 1); err != ErrMailboxNotFound {
		t.Fatalf("cross-tenant must be ErrMailboxNotFound, got %v", err)
	}
}

func TestSetMailAccessMode_InvalidModeRejected(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "alice@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, _, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, "external_only", 1); err != ErrInvalidMailAccessMode {
		t.Fatalf("invalid mode must be rejected, got %v", err)
	}
}

func TestSetMailAccessMode_AuditFailureRollsBack(t *testing.T) {
	db := newMailboxTestDB(t)
	db.SetMaxOpenConns(1)
	// Create the mailbox with a service that has NO audit store (the
	// create path succeeds), then build a service whose audit store
	// has no table: RecordTx fails and the access-mode mutation must
	// roll back, including the version bump.
	plain := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	created, err := plain.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: "alice@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store := audit.NewExtendedStore(db) // no orvix_audit table in this fixture
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), store, nil)
	if _, _, _, err := svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, string(MailAccessInternalOnly), 1); err == nil {
		t.Fatal("audit failure must fail the mutation")
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version must roll back to 1, got %d", version)
	}
	var stored string
	if err := db.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(MailAccessInherit) {
		t.Fatalf("mode must roll back to inherit, got %q", stored)
	}
}

func TestSetMailAccessMode_ConcurrentGuardedWritesOneWins(t *testing.T) {
	// Repeated runs (3x) demonstrate stable optimistic-concurrency
	// results: exactly one writer wins per run, the rest conflict.
	for run := 0; run < 3; run++ {
		// A FILE-backed SQLite DB so concurrent goroutines share one
		// database (an in-memory :memory: DB is per-connection).
		db, err := sql.Open("sqlite", t.TempDir()+"/conc.db?_busy_timeout=5000&_txlock=immediate")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
			mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		); CREATE TABLE coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL,
			local_part TEXT NOT NULL,
			email TEXT NOT NULL,
			name TEXT,
			password_hash TEXT NOT NULL,
			auth_scheme TEXT,
			status TEXT NOT NULL,
			quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			allow_smtp INTEGER NOT NULL DEFAULT 1,
			allow_imap INTEGER NOT NULL DEFAULT 1,
			allow_pop3 INTEGER NOT NULL DEFAULT 1,
			allow_jmap INTEGER NOT NULL DEFAULT 1,
			allow_webmail INTEGER NOT NULL DEFAULT 1,
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			last_ip TEXT,
			mail_access_mode TEXT NOT NULL DEFAULT 'inherit',
			version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		); CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
		seedDomain(t, db, 1, "example.test", "active", false)
		created, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
			Email: "alice@example.test", Password: "InitialPassword123!",
		}, 1)
		if err != nil {
			db.Close()
			t.Fatalf("create: %v", err)
		}

		const workers = 8
		var wg sync.WaitGroup
		results := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _, _, results[i] = svc.SetMailAccessMode(context.Background(), created.Mailbox.ID, 1, string(MailAccessInternalOnly), 1)
			}(i)
		}
		wg.Wait()

		// Exactly one writer wins (no version conflict), the rest conflict.
		wins := 0
		for _, err := range results {
			if err == nil {
				wins++
			}
		}
		if wins != 1 {
			db.Close()
			t.Fatalf("run %d: expected exactly 1 successful guarded write, got %d", run, wins)
		}
		var version int
		if err := db.QueryRow(`SELECT version FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&version); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if version != 2 {
			db.Close()
			t.Fatalf("run %d: version=%d want 2", run, version)
		}
		db.Close()
	}
}

func TestCreateMailboxWithFolders_ProvisionsInTransaction(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)

	// The fixture has no coremail_folders table; create it so the
	// transactional provisioning can succeed.
	if _, err := db.Exec(`CREATE TABLE coremail_folders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox_id INTEGER NOT NULL,
		parent_id INTEGER,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		folder_type TEXT NOT NULL DEFAULT 'custom',
		message_count INTEGER NOT NULL DEFAULT 0,
		unread_count INTEGER NOT NULL DEFAULT 0,
		total_size INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	created, err := svc.CreateMailboxWithFolders(context.Background(), CreateMailboxRequest{
		Email: "provisioned@example.test", Password: "InitialPassword123!",
	}, 1)
	if err != nil {
		t.Fatalf("create with folders: %v", err)
	}
	var folderCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id=?`, created.Mailbox.ID).Scan(&folderCount); err != nil {
		t.Fatal(err)
	}
	if folderCount != 6 {
		t.Fatalf("expected 6 canonical system folders, got %d", folderCount)
	}
}

func TestCreateMailboxWithFolders_FolderFailureRollsBackMailbox(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	seedDomain(t, db, 1, "example.test", "active", false)
	// NO coremail_folders table: folder provisioning fails and the
	// mailbox insert must roll back with it — no half-created mailbox.
	_, err := svc.CreateMailboxWithFolders(context.Background(), CreateMailboxRequest{
		Email: "half@example.test", Password: "InitialPassword123!",
	}, 1)
	if err == nil {
		t.Fatal("folder provisioning failure must fail the create")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email='half@example.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("folder failure must roll back the mailbox insert, found %d rows", count)
	}
}
