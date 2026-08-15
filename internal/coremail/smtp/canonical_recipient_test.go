package smtp

// Canonical recipient-plan tests (CONCERN C).
//
// buildRecipientPlan is the side-effect-free canonical recipient
// resolution used by AcceptMessage. These tests pin:
//   - direct mailbox recipients resolve with the target's OWNING
//     domain/tenant attribution;
//   - alias/catchall indirection can never bypass domain operability:
//     disabled/suspended/locked/soft-deleted owning domains of
//     resolved targets fail closed;
//   - cross-tenant resolved targets fail closed (mailbox tenant vs
//     owning domain tenant, and target tenant vs presented tenant);
//   - unknown external domains keep the normal external-recipient
//     behavior;
//   - infrastructure failures produce errors (transient SMTP failure),
//     never acceptance;
//   - canonical domain checks are deduplicated by domain name.
//
// The SMTP-level tests drive the real command handler over loopback
// TCP and assert the authoritative acceptance boundary: rejection
// persists zero spool/queue/message state.

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// seedExtraDomainAndMailbox inserts a domain + mailbox directly
// (mirroring seedPolicySMTPMailbox) and returns the mailbox email,
// domain id, and mailbox id.
func seedExtraDomainAndMailbox(t *testing.T, db *sql.DB, domainName, email, status string) (uint, uint) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at)
		 VALUES (?, 1, ?, 'smb', 'internal_external', ?, ?)
		 ON CONFLICT(name) DO NOTHING`, domainName, status, now, now); err != nil {
		t.Fatalf("seed domain %s: %v", domainName, err)
	}
	var domainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name=?`, domainName).Scan(&domainID); err != nil {
		t.Fatalf("domain id %s: %v", domainName, err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, 1, ?, ?, ?, 'x-placeholder', 'bcrypt', 'active', 1024, 0, ?, ?)`,
		domainID, strings.Split(email, "@")[0], email, email, now, now); err != nil {
		t.Fatalf("seed mailbox %s: %v", email, err)
	}
	var mailboxID uint
	if err := db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email=?`, email).Scan(&mailboxID); err != nil {
		t.Fatalf("mailbox id %s: %v", email, err)
	}
	return domainID, mailboxID
}

func seedAlias(t *testing.T, db *sql.DB, domainID uint, fromAddr, toAddr string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
		 VALUES (?, 1, ?, ?, 1, ?, ?)`, domainID, fromAddr, toAddr, now, now); err != nil {
		t.Fatalf("seed alias %s -> %s: %v", fromAddr, toAddr, err)
	}
}

func countTableRows(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// planFor builds the canonical plan for the given recipients.
func planFor(t *testing.T, eng *coremail.Engine, rcv *Receiver, rcpts ...string) (*recipientPlan, error) {
	t.Helper()
	return rcv.buildRecipientPlan(context.Background(), rcpts)
}

func TestCanonicalRecipientPlan_DirectMailbox(t *testing.T) {
	_, eng, ms, qe, rcv := testIntegrationEnvWithDB(t)

	plan, err := planFor(t, eng, rcv, "user@test.com")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.external) != 0 {
		t.Fatalf("external = %+v, want none", plan.external)
	}
	if len(plan.local) != 1 {
		t.Fatalf("local = %+v, want 1", plan.local)
	}
	r := plan.local[0]
	if r.Kind != recipientDirect {
		t.Errorf("kind = %v, want recipientDirect", r.Kind)
	}
	if r.Presented != "user@test.com" || r.Target != "user@test.com" {
		t.Errorf("presented/target = %q/%q", r.Presented, r.Target)
	}
	if r.TenantID != 1 || r.Domain != "test.com" || r.MailboxID == 0 {
		t.Errorf("attribution tenant=%d domain=%s mailbox=%d", r.TenantID, r.Domain, r.MailboxID)
	}
	if len(plan.domains) != 1 || plan.domains[0].Name != "test.com" {
		t.Errorf("domains = %+v, want [test.com]", plan.domains)
	}
	// Sanity: the env still has a working store.
	_ = ms
	_ = qe
}

func TestCanonicalRecipientPlan_ExternalRecipientPreserved(t *testing.T) {
	_, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	plan, err := planFor(t, eng, rcv, "someone@external.example")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.local) != 0 {
		t.Fatalf("local = %+v, want none", plan.local)
	}
	if len(plan.external) != 1 || plan.external[0].Email != "someone@external.example" {
		t.Fatalf("external = %+v", plan.external)
	}
}

func TestCanonicalRecipientPlan_DomainsDeduplicated(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	// Two mailboxes on the same domain: the plan must record the
	// domain exactly once.
	seedExtraDomainAndMailbox(t, db, "shared.test", "a@shared.test", "active")
	seedExtraDomainAndMailbox(t, db, "shared.test", "b@shared.test", "active")

	plan, err := planFor(t, eng, rcv, "a@shared.test", "b@shared.test")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.local) != 2 {
		t.Fatalf("local = %d, want 2", len(plan.local))
	}
	count := 0
	for _, d := range plan.domains {
		if d.Name == "shared.test" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared.test recorded %d times, want exactly 1 (deduplicated): %+v", count, plan.domains)
	}
}

func TestCanonicalRecipientPlan_AliasToActiveLocalTargetUsesTargetAttribution(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	_, _ = seedExtraDomainAndMailbox(t, db, "other.test", "bob@other.test", "active")
	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	seedAlias(t, db, testDomainID, "sales@test.com", "bob@other.test")

	plan, err := planFor(t, eng, rcv, "sales@test.com")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.local) != 1 {
		t.Fatalf("local = %+v, want 1", plan.local)
	}
	r := plan.local[0]
	if r.Kind != recipientAlias {
		t.Errorf("kind = %v, want recipientAlias", r.Kind)
	}
	if r.Target != "bob@other.test" || r.Presented != "sales@test.com" {
		t.Errorf("target/presented = %q/%q", r.Target, r.Presented)
	}
	if r.Domain != "other.test" {
		t.Errorf("domain = %q, want other.test (target's own domain, not test.com)", r.Domain)
	}
	var otherDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='other.test'`).Scan(&otherDomainID); err != nil {
		t.Fatalf("other.test id: %v", err)
	}
	if r.DomainID != otherDomainID {
		t.Errorf("domain_id = %d, want %d (target's owning domain)", r.DomainID, otherDomainID)
	}
	// Both presented and owning domains are recorded, deduplicated.
	if len(plan.domains) != 2 {
		t.Errorf("domains = %+v, want [test.com other.test]", plan.domains)
	}
}

// TestCanonicalRecipientPlan_AliasTargetDomainInoperableFailsClosed
// pins that alias indirection can never bypass domain operability: a
// resolved target whose OWNING domain is disabled/suspended/locked/
// soft-deleted fails the whole plan (and therefore the acceptance).
func TestCanonicalRecipientPlan_AliasTargetDomainInoperableFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status string
		del    bool
	}{
		{"disabled", "disabled", false},
		{"suspended", "suspended", false},
		{"locked", "locked", false},
		{"deleted", "deleted", false},
		{"soft-deleted", "active", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

			domainID, _ := seedExtraDomainAndMailbox(t, db, "target.test", "bob@target.test", "active")
			var testDomainID uint
			if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
				t.Fatalf("test.com id: %v", err)
			}
			seedAlias(t, db, testDomainID, "sales@test.com", "bob@target.test")

			// Make the TARGET's owning domain inoperable AFTER the
			// alias was seeded (simulating disable-after-RCPT).
			if tc.del {
				if _, err := db.Exec(`UPDATE coremail_domains SET deleted_at = datetime('now') WHERE id = ?`, domainID); err != nil {
					t.Fatalf("soft-delete target domain: %v", err)
				}
			} else {
				if _, err := db.Exec(`UPDATE coremail_domains SET status = ? WHERE id = ?`, tc.status, domainID); err != nil {
					t.Fatalf("set target domain status: %v", err)
				}
			}

			_, err := planFor(t, eng, rcv, "sales@test.com")
			if err == nil {
				t.Fatalf("expected plan failure for target domain %q (status=%q deleted=%v)", tc.name, tc.status, tc.del)
			}
			// The failure must be about the owning domain, never a
			// silent acceptance.
			if !strings.Contains(err.Error(), "owning domain") {
				t.Errorf("error %q does not reference owning domain", err)
			}
		})
	}
}

func TestCanonicalRecipientPlan_CrossTenantAliasTargetFailsClosed(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	// Tenant 2 domain + mailbox.
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at)
		 VALUES ('tenant2.test', 2, 'active', 'smb', 'internal_external', ?, ?)`, now, now); err != nil {
		t.Fatalf("tenant2 domain: %v", err)
	}
	var t2DomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='tenant2.test'`).Scan(&t2DomainID); err != nil {
		t.Fatalf("tenant2 domain id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, 2, 'bob', 'bob@tenant2.test', 'Bob', 'x-placeholder', 'bcrypt', 'active', 1024, 0, ?, ?)`,
		t2DomainID, now, now); err != nil {
		t.Fatalf("tenant2 mailbox: %v", err)
	}

	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	// Alias on tenant-1's domain pointing into tenant 2.
	seedAlias(t, db, testDomainID, "sales@test.com", "bob@tenant2.test")

	_, err := planFor(t, eng, rcv, "sales@test.com")
	if err == nil {
		t.Fatal("expected cross-tenant alias target to fail the plan")
	}
	if !strings.Contains(err.Error(), "cross-tenant") {
		t.Errorf("error %q does not mention cross-tenant", err)
	}
}

func TestCanonicalRecipientPlan_OwnDomainTenantMismatchFailsClosed(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	domainID, mailboxID := seedExtraDomainAndMailbox(t, db, "mismatch.test", "bob@mismatch.test", "active")
	// Corrupt the mailbox's tenant so it disagrees with its own
	// owning domain row.
	if _, err := db.Exec(`UPDATE coremail_mailboxes SET tenant_id = 2 WHERE id = ?`, mailboxID); err != nil {
		t.Fatalf("corrupt mailbox tenant: %v", err)
	}
	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	seedAlias(t, db, testDomainID, "sales@test.com", "bob@mismatch.test")
	_ = domainID

	_, err := planFor(t, eng, rcv, "sales@test.com")
	if err == nil {
		t.Fatal("expected owning-domain tenant mismatch to fail the plan")
	}
	if !strings.Contains(err.Error(), "tenant mismatch") {
		t.Errorf("error %q does not mention tenant mismatch", err)
	}
}

func TestCanonicalRecipientPlan_RepositoryFailureIsError(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	// Simulate an infrastructure failure: drop the aliases table so
	// ResolveAddress hits a repository error for the local address.
	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	seedAlias(t, db, testDomainID, "sales@test.com", "user@test.com")
	if _, err := db.Exec(`DROP TABLE coremail_aliases`); err != nil {
		t.Fatalf("drop aliases: %v", err)
	}

	_, err := planFor(t, eng, rcv, "sales@test.com")
	if err == nil {
		t.Fatal("expected repository failure to produce an error, never acceptance")
	}
}

// TestCanonicalRecipientPlan_SoftDeletedPresentedDomainWithMailboxFailsClosed
// pins that a soft-deleted presented domain is NOT silently treated as
// an unknown external domain when the address still has local state: a
// mailbox row on the soft-deleted domain fails the plan (owning domain
// unavailable) instead of relaying the message as external.
func TestCanonicalRecipientPlan_SoftDeletedPresentedDomainWithMailboxFailsClosed(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	domainID, _ := seedExtraDomainAndMailbox(t, db, "dying.test", "alice@dying.test", "active")
	if _, err := db.Exec(`UPDATE coremail_domains SET deleted_at = datetime('now') WHERE id = ?`, domainID); err != nil {
		t.Fatalf("soft-delete domain: %v", err)
	}

	_, err := planFor(t, eng, rcv, "alice@dying.test")
	if err == nil {
		t.Fatal("expected soft-deleted owning domain with existing mailbox to fail the plan")
	}
	if !strings.Contains(err.Error(), "owning domain") {
		t.Errorf("error %q does not reference owning domain", err)
	}
}

// TestCanonicalRecipientPlan_SoftDeletedPresentedDomainWithAliasFailsClosed
// pins the same fail-closed rule for alias state: an alias row on a
// soft-deleted domain fails the plan instead of relaying externally.
func TestCanonicalRecipientPlan_SoftDeletedPresentedDomainWithAliasFailsClosed(t *testing.T) {
	db, eng, _, _, rcv := testIntegrationEnvWithDB(t)

	domainID, _ := seedExtraDomainAndMailbox(t, db, "dying.test", "bob@dying.test", "active")
	seedAlias(t, db, domainID, "sales@dying.test", "bob@dying.test")
	if _, err := db.Exec(`UPDATE coremail_domains SET deleted_at = datetime('now') WHERE id = ?`, domainID); err != nil {
		t.Fatalf("soft-delete domain: %v", err)
	}

	_, err := planFor(t, eng, rcv, "sales@dying.test")
	if err == nil {
		t.Fatal("expected soft-deleted owning domain with existing alias to fail the plan")
	}
}

// ── SMTP-level authoritative acceptance tests (real TCP sessions) ──

type canonicalSMTPEnv struct {
	addr string
	db   *sql.DB
	eng  *coremail.Engine
	ms   *storage.MailStore
}

// newCanonicalSMTPEnv builds a real SMTP server over loopback TCP with
// the real command handler + receiver, seeded with test.com (active)
// and user@test.com, plus an alias sales@test.com -> bob@target.test
// (target seeded active on target.test). The caller can mutate the DB
// between RCPT and DATA.
func newCanonicalSMTPEnv(t *testing.T, targetStatus string) *canonicalSMTPEnv {
	t.Helper()
	db, eng, ms, qe, _ := testIntegrationEnvWithDB(t)

	domainID, targetMailboxID := seedExtraDomainAndMailbox(t, db, "target.test", "bob@target.test", targetStatus)
	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	seedAlias(t, db, testDomainID, "sales@test.com", "bob@target.test")
	_ = domainID

	// The raw-SQL-seeded target mailbox has no system folders yet;
	// delivery requires an INBOX.
	if err := ms.Folders.EnsureSystemFolders(context.Background(), targetMailboxID, nil); err != nil {
		t.Fatalf("ensure target mailbox folders: %v", err)
	}

	cfg := DefaultConfig()
	cfg.RequireAuthForSubmission = false
	cfg.RequireTLSForAuth = false
	cfg.AllowPlainAuthWithoutTLS = true

	identity := NewIdentityService(eng)
	auth := NewAuthenticator(identity)
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	rcv := NewReceiver(eng, ms, qe, cfg)
	srv := NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(identity.IsLocalDomain)
	srv.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := eng.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	go func() {
		srv.listener = listener
		_ = srv.serve()
	}()
	t.Cleanup(func() { listener.Close() })
	return &canonicalSMTPEnv{addr: addr, db: db, eng: eng, ms: ms}
}

func (e *canonicalSMTPEnv) conn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", e.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if !strings.HasPrefix(greeting, "220") {
		t.Fatalf("greeting: %q", greeting)
	}
	return conn, reader
}

func (e *canonicalSMTPEnv) cmd(t *testing.T, conn net.Conn, reader *bufio.Reader, line string) string {
	t.Helper()
	if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
	// Read the full response, including multiline replies (e.g. EHLO
	// "250-..." continuation lines), returning the LAST line (the one
	// whose code is not followed by '-').
	var last string
	for {
		resp, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read after %q: %v", line, err)
		}
		last = resp
		trimmed := strings.TrimRight(resp, "\r\n")
		if len(trimmed) < 4 || trimmed[3] != '-' {
			return last
		}
	}
}

// TestSMTPAliasTargetDomainDisabledAfterRCPTRejectsAtDATA proves the
// authoritative acceptance boundary: the presented domain was active
// at RCPT (250), but when the alias TARGET's owning domain becomes
// inoperable before DATA, the message is rejected and NOTHING is
// spooled or queued.
func TestSMTPAliasTargetDomainDisabledAfterRCPTRejectsAtDATA(t *testing.T) {
	env := newCanonicalSMTPEnv(t, "active")

	conn, reader := env.conn(t)
	env.cmd(t, conn, reader, "EHLO mx.example")
	env.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	if !strings.HasPrefix(rcpt, "250") {
		t.Fatalf("RCPT (presented domain active): %q", rcpt)
	}

	// The alias target's owning domain becomes disabled AFTER RCPT.
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='suspended' WHERE name='target.test'`); err != nil {
		t.Fatalf("disable target domain: %v", err)
	}

	data := env.cmd(t, conn, reader, "DATA")
	if !strings.HasPrefix(data, "354") {
		t.Fatalf("DATA: %q", data)
	}
	if _, err := conn.Write([]byte("Subject: X\r\n\r\nbody\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	final, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx rejection at DATA, got %q", final)
	}

	if n := countTableRows(t, env.db, "coremail_messages"); n != 0 {
		t.Errorf("messages persisted on rejection: %d, want 0", n)
	}
	if n := countTableRows(t, env.db, "coremail_queue"); n != 0 {
		t.Errorf("queue rows persisted on rejection: %d, want 0", n)
	}
}

// TestSMTPAliasTargetActiveAcceptedWithCorrectAttribution proves the
// positive path: an alias to an active mailbox on another active local
// domain is accepted exactly once, and the stored message + queue row
// are attributed to the TARGET's owning domain and tenant, not the
// presented address's domain.
func TestSMTPAliasTargetActiveAcceptedWithCorrectAttribution(t *testing.T) {
	env := newCanonicalSMTPEnv(t, "active")

	var targetDomainID, targetMailboxID uint
	if err := env.db.QueryRow(`SELECT id FROM coremail_domains WHERE name='target.test'`).Scan(&targetDomainID); err != nil {
		t.Fatalf("target domain id: %v", err)
	}
	if err := env.db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email='bob@target.test'`).Scan(&targetMailboxID); err != nil {
		t.Fatalf("target mailbox id: %v", err)
	}

	conn, reader := env.conn(t)
	env.cmd(t, conn, reader, "EHLO mx.example")
	env.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	env.cmd(t, conn, reader, "DATA")
	if _, err := conn.Write([]byte("Subject: Alias delivery\r\n\r\nbody\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	final, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected acceptance, got %q", final)
	}

	var msgMailbox, msgTenant, msgDomain uint
	if err := env.db.QueryRow(`SELECT mailbox_id, tenant_id, domain_id FROM coremail_messages`).Scan(&msgMailbox, &msgTenant, &msgDomain); err != nil {
		t.Fatalf("stored message: %v", err)
	}
	if msgMailbox != targetMailboxID {
		t.Errorf("message mailbox_id = %d, want %d (target mailbox)", msgMailbox, targetMailboxID)
	}
	if msgDomain != targetDomainID {
		t.Errorf("message domain_id = %d, want %d (target owning domain)", msgDomain, targetDomainID)
	}
	if msgTenant != 1 {
		t.Errorf("message tenant_id = %d, want 1", msgTenant)
	}

	var qTo string
	var qTenant, qDomain uint
	if err := env.db.QueryRow(`SELECT to_address, tenant_id, domain_id FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&qTo, &qTenant, &qDomain); err != nil {
		t.Fatalf("queue row: %v", err)
	}
	if qTo != "bob@target.test" {
		t.Errorf("queue to_address = %q, want bob@target.test", qTo)
	}
	if qDomain != targetDomainID || qTenant != 1 {
		t.Errorf("queue attribution = domain %d tenant %d, want %d / 1", qDomain, qTenant, targetDomainID)
	}

	// Exactly one durable acceptance: one message row, one queue row.
	if n := countTableRows(t, env.db, "coremail_messages"); n != 1 {
		t.Errorf("message rows = %d, want 1", n)
	}
	if n := countTableRows(t, env.db, "coremail_queue"); n != 1 {
		t.Errorf("queue rows = %d, want 1", n)
	}
}

// TestSMTPCrossTenantAliasTargetRejectedAtDATA proves a cross-tenant
// resolved target is rejected at the acceptance boundary with zero
// durable side effects.
func TestSMTPCrossTenantAliasTargetRejectedAtDATA(t *testing.T) {
	env := newCanonicalSMTPEnv(t, "active")

	// Move the target mailbox + domain to tenant 2 AFTER seeding, so
	// RCPT still passes (presented domain active) but the resolved
	// target is cross-tenant at DATA.
	if _, err := env.db.Exec(`UPDATE coremail_domains SET tenant_id = 2 WHERE name = 'target.test'`); err != nil {
		t.Fatalf("re-tenant target domain: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE coremail_mailboxes SET tenant_id = 2 WHERE email = 'bob@target.test'`); err != nil {
		t.Fatalf("re-tenant target mailbox: %v", err)
	}

	conn, reader := env.conn(t)
	env.cmd(t, conn, reader, "EHLO mx.example")
	env.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	env.cmd(t, conn, reader, "DATA")
	if _, err := conn.Write([]byte("Subject: X\r\n\r\nbody\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	final, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient rejection for cross-tenant target, got %q", final)
	}
	if n := countTableRows(t, env.db, "coremail_messages"); n != 0 {
		t.Errorf("messages persisted: %d, want 0", n)
	}
	if n := countTableRows(t, env.db, "coremail_queue"); n != 0 {
		t.Errorf("queue rows persisted: %d, want 0", n)
	}
}

// TestSMTPUnknownExternalDomainKeepsNormalBehavior proves the
// enforcement did not change normal external-recipient behavior: an
// unauthenticated client relaying to an unknown external domain still
// gets the standard relay-protection rejection at RCPT (no data is
// ever read or accepted).
func TestSMTPUnknownExternalDomainKeepsNormalBehavior(t *testing.T) {
	env := newCanonicalSMTPEnv(t, "active")

	conn, reader := env.conn(t)
	env.cmd(t, conn, reader, "EHLO mx.example")
	env.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<victim@notours.example>")
	if !strings.HasPrefix(rcpt, "5") {
		t.Fatalf("expected permanent relay-protection rejection at RCPT, got %q", rcpt)
	}
	if n := countTableRows(t, env.db, "coremail_queue"); n != 0 {
		t.Errorf("queue rows after relay rejection: %d, want 0", n)
	}
	_ = fmt.Sprint()
}
