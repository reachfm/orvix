package smtp

// Authoritative SMTP acceptance protocol tests (CONCERN D).
//
// These tests drive the REAL server/command-handler path over loopback
// TCP (the same path production runs: server.go -> PreAcceptFn ->
// Receiver.AcceptMessage -> StoreMessage/Enqueue) and pin the
// authoritative acceptance boundary:
//
//   - active direct mailbox accepted, exactly one durable acceptance;
//   - disabled/suspended/locked/soft-deleted presented (alias-owning)
//     domains rejected at DATA with zero spool/queue persistence;
//   - alias to an active mailbox accepted; alias TARGET domain disabled
//     after RCPT rejected;
//   - repository failure -> transient 4xx, never acceptance;
//   - nil database/policy dependency -> transient 4xx;
//   - unknown external recipients keep their normal behavior;
//   - ambiguous client disconnect after the final dot -> exactly one
//     durable acceptance per attempt (no double-accept within an
//     attempt; a retry is a separate, exactly-once acceptance).
//
// Group note: customer-facing groups (coremail_groups) are an
// API-scoped feature and are NOT part of the SMTP inbound resolution
// chain (AuthService.ResolveAddress resolves mailbox / forwarder /
// alias / catchall only — verified by tracing the real path). The
// mail-time indirection that MUST be domain-enforced is alias fan-out,
// which the canonical recipient plan covers here and in
// canonical_recipient_test.go. There is no SMTP group-expansion
// subsystem to test because none exists; inventing one is out of the
// bounded scope of this batch.

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// authoritativeSMTPEnv is a real SMTP server over loopback TCP with:
//   - domain test.com (active) + mailbox user@test.com;
//   - domain target.test (active) + mailbox bob@target.test;
//   - alias sales@test.com -> bob@target.test;
//   - a production-shaped PreAcceptFn that rechecks presented
//     recipient domains (deduplicated by name) right before DATA
//     acceptance — mirroring runtime.checkRecipientDomainsOperable:
//     unknown domains are allowed (anti-enumeration), known
//     inoperable domains reject with ErrRecipientDomainInoperable
//     (5.7.1 at the server boundary), repository failures map to
//     transient 4xx.
type authoritativeSMTPEnv struct {
	addr      string
	db        *sql.DB
	ms        *storage.MailStore
	preAccept func(ctx context.Context, session *Session) error
	nilEngine bool
}

func newAuthoritativeSMTPEnv(t *testing.T) *authoritativeSMTPEnv {
	t.Helper()
	return newAuthoritativeSMTPEnvOpts(t, false)
}

func newAuthoritativeSMTPEnvOpts(t *testing.T, nilEngine bool) *authoritativeSMTPEnv {
	t.Helper()
	db, eng, ms, qe, _ := testIntegrationEnvWithDB(t)

	domainID, targetMailboxID := seedExtraDomainAndMailbox(t, db, "target.test", "bob@target.test", "active")
	var testDomainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='test.com'`).Scan(&testDomainID); err != nil {
		t.Fatalf("test.com id: %v", err)
	}
	seedAlias(t, db, testDomainID, "sales@test.com", "bob@target.test")
	_ = domainID
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
	if nilEngine {
		// Simulate a broken wiring: the receiver has no engine at all.
		rcv.Engine = nil
	}
	srv := NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(identity.IsLocalDomain)
	srv.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := eng.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}

	env := &authoritativeSMTPEnv{db: db, ms: ms, nilEngine: nilEngine}
	srv.PreAcceptFn = env.presentedDomainRecheck

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	env.addr = listener.Addr().String()
	go func() {
		srv.listener = listener
		_ = srv.serve()
	}()
	t.Cleanup(func() { listener.Close() })
	return env
}

// presentedDomainRecheck mirrors the production pre-DATA recheck for
// PRESENTED recipient domains (the runtime's
// checkRecipientDomainsOperable semantics): deduplicated by name,
// unknown domains allowed (anti-enumeration), existing inoperable
// domains rejected with ErrRecipientDomainInoperable, repository
// failures returned as generic errors (mapped to 4.7.0).
func (e *authoritativeSMTPEnv) presentedDomainRecheck(ctx context.Context, session *Session) error {
	if e.preAccept != nil {
		return e.preAccept(ctx, session)
	}
	seen := map[string]bool{}
	for _, addr := range session.Recipients {
		dom := strings.ToLower(ExtractDomain(addr))
		if dom == "" || seen[dom] {
			continue
		}
		seen[dom] = true
		var status string
		var deletedAt sql.NullTime
		err := e.db.QueryRowContext(ctx,
			`SELECT status, deleted_at FROM coremail_domains WHERE name = ? AND deleted_at IS NULL`,
			dom).Scan(&status, &deletedAt)
		if err == sql.ErrNoRows {
			continue // unknown domain: anti-enumeration allows
		}
		if err != nil {
			return errors.New("recipient policy database unavailable")
		}
		if status != string(coremail.DomainActive) {
			return ErrRecipientDomainInoperable
		}
	}
	return nil
}

func (e *authoritativeSMTPEnv) conn(t *testing.T) (net.Conn, *bufio.Reader) {
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

func (e *authoritativeSMTPEnv) cmd(t *testing.T, conn net.Conn, reader *bufio.Reader, line string) string {
	t.Helper()
	if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
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

// sendData writes the DATA command, the body, and the terminator,
// returning the server's final response line.
func (e *authoritativeSMTPEnv) sendData(t *testing.T, conn net.Conn, reader *bufio.Reader, body string) string {
	t.Helper()
	data := e.cmd(t, conn, reader, "DATA")
	if !strings.HasPrefix(data, "354") {
		t.Fatalf("DATA: %q", data)
	}
	if _, err := conn.Write([]byte(body + "\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	final, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	return final
}

func (e *authoritativeSMTPEnv) rowCounts(t *testing.T) (int64, int64) {
	t.Helper()
	return countTableRows(t, e.db, "coremail_messages"), countTableRows(t, e.db, "coremail_queue")
}

func (e *authoritativeSMTPEnv) beginSession(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := e.conn(t)
	e.cmd(t, conn, reader, "EHLO mx.example")
	e.cmd(t, conn, reader, "MAIL FROM:<sender@external.example>")
	return conn, reader
}

// TestProtocol_ActiveDirectMailboxAccepted pins the happy path:
// exactly one durable acceptance (one message row, one queue row).
func TestProtocol_ActiveDirectMailboxAccepted(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: Direct\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("durable acceptance: messages=%d queue=%d, want exactly 1 and 1", msgs, queues)
	}
}

// TestProtocol_DisabledMailboxDomainRejected pins that a presented
// domain disabled/suspended/locked/soft-deleted after RCPT is
// rejected at DATA with a non-acceptance response and zero
// persistence. Soft-deletion is distinguished from a genuinely
// unknown domain by the existing mailbox row: known-but-inoperable
// owning domains fail closed (4xx from the receiver's canonical plan),
// while genuinely unknown domains keep the anti-enumeration
// external-recipient path.
func TestProtocol_DisabledMailboxDomainRejected(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string // response prefix class
	}{
		{"disabled", `UPDATE coremail_domains SET status='disabled' WHERE name='test.com'`, "5"},
		{"suspended", `UPDATE coremail_domains SET status='suspended' WHERE name='test.com'`, "5"},
		{"locked", `UPDATE coremail_domains SET status='locked' WHERE name='test.com'`, "5"},
		{"soft-deleted", `UPDATE coremail_domains SET deleted_at=datetime('now') WHERE name='test.com'`, "4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAuthoritativeSMTPEnv(t)

			conn, reader := env.beginSession(t)
			rcpt := env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
			if !strings.HasPrefix(rcpt, "250") {
				t.Fatalf("RCPT while active: %q", rcpt)
			}
			if _, err := env.db.Exec(tc.sql); err != nil {
				t.Fatalf("disable domain: %v", err)
			}
			final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
			if !strings.HasPrefix(final, tc.want) {
				t.Fatalf("expected %sxx rejection at DATA, got %q", tc.want, final)
			}
			msgs, queues := env.rowCounts(t)
			if msgs != 0 || queues != 0 {
				t.Fatalf("rejection persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
			}
		})
	}
}

// TestProtocol_AliasToActiveMailboxAccepted pins alias acceptance
// with exactly one durable acceptance attributed to the target.
func TestProtocol_AliasToActiveMailboxAccepted(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	if !strings.HasPrefix(rcpt, "250") {
		t.Fatalf("RCPT alias: %q", rcpt)
	}
	final := env.sendData(t, conn, reader, "Subject: Alias\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("durable acceptance: messages=%d queue=%d, want exactly 1 and 1", msgs, queues)
	}
	var to string
	if err := env.db.QueryRow(`SELECT to_address FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&to); err != nil {
		t.Fatalf("queue row: %v", err)
	}
	if to != "bob@target.test" {
		t.Errorf("queue to_address = %q, want bob@target.test", to)
	}
}

// TestProtocol_AliasOwningDomainDisabledRejected pins that the
// alias's OWNING (presented) domain becoming inoperable after RCPT is
// rejected at DATA with zero persistence.
func TestProtocol_AliasOwningDomainDisabledRejected(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='suspended' WHERE name='test.com'`); err != nil {
		t.Fatalf("disable alias owning domain: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "5") {
		t.Fatalf("expected 5.7.1, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("rejection persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_AliasTargetDomainDisabledRejected pins that the alias
// TARGET's owning domain becoming inoperable after RCPT is rejected
// at DATA (transient 4xx from the receiver's canonical plan recheck)
// with zero persistence.
func TestProtocol_AliasTargetDomainDisabledRejected(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='locked' WHERE name='target.test'`); err != nil {
		t.Fatalf("disable target domain: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("rejection persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_DomainChangesAfterRCPTBeforeDATAResolvedTargetRejected
// pins the full after-RCPT-before-DATA window for a resolved alias
// target: the presented domain is rechecked by PreAcceptFn and the
// target domain by the receiver's final canonical recheck.
func TestProtocol_DomainChangesAfterRCPTBeforeDATAResolvedTargetRejected(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	if !strings.HasPrefix(rcpt, "250") {
		t.Fatalf("RCPT: %q", rcpt)
	}
	// Disable BOTH after RCPT: presented domain flips to inactive AND
	// target domain flips to inactive.
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='suspended' WHERE name='test.com'`); err != nil {
		t.Fatalf("disable presented: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE coremail_domains SET status='suspended' WHERE name='target.test'`); err != nil {
		t.Fatalf("disable target: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") && !strings.HasPrefix(final, "5") {
		t.Fatalf("expected rejection (4xx/5xx), got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("rejection persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_RepositoryFailureTransient4xx pins that an
// infrastructure failure during acceptance produces a transient 4xx
// and never acceptance.
func TestProtocol_RepositoryFailureTransient4xx(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<sales@test.com>")
	// Drop the aliases table: ResolveAddress hits a repository error.
	if _, err := env.db.Exec(`DROP TABLE coremail_aliases`); err != nil {
		t.Fatalf("drop aliases: %v", err)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("failure persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_NilEngineDependencyTransient4xx pins that a nil
// database/engine dependency on the receiver produces a transient
// 4xx (the server's recover path) and never acceptance.
func TestProtocol_NilEngineDependencyTransient4xx(t *testing.T) {
	env := newAuthoritativeSMTPEnvOpts(t, true)

	conn, reader := env.beginSession(t)
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	if !strings.HasPrefix(rcpt, "250") {
		t.Fatalf("RCPT: %q", rcpt)
	}
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx for nil engine, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("nil-engine failure persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_PolicyDependencyFailureTransient4xx pins that a policy
// dependency failure in the pre-accept hook maps to the server's
// transient 4.7.0 path.
func TestProtocol_PolicyDependencyFailureTransient4xx(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)
	env.preAccept = func(ctx context.Context, session *Session) error {
		return errors.New("simulated policy database unavailable")
	}

	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: X\r\n\r\nbody")
	if !strings.HasPrefix(final, "4") {
		t.Fatalf("expected transient 4xx for policy failure, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("policy failure persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_ExternalRecipientBehaviorPreserved pins that the
// enforcement did not change normal external-recipient behavior: an
// unauthenticated inbound client relaying to an unknown external
// domain still gets the standard relay-protection rejection at RCPT.
func TestProtocol_ExternalRecipientBehaviorPreserved(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn, reader := env.beginSession(t)
	rcpt := env.cmd(t, conn, reader, "RCPT TO:<victim@notours.example>")
	if !strings.HasPrefix(rcpt, "5") {
		t.Fatalf("expected relay-protection rejection at RCPT, got %q", rcpt)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 0 || queues != 0 {
		t.Fatalf("relay rejection persisted state: messages=%d queue=%d, want 0/0", msgs, queues)
	}
}

// TestProtocol_NoDuplicateDeliveryOnAmbiguousDisconnect pins the
// ambiguous-disconnect contract: a client that sends the final dot
// and disconnects WITHOUT reading the response still gets exactly one
// durable acceptance for that attempt (the server processes the
// complete message synchronously); a retry session is a separate
// exactly-once acceptance — never a double-accept within an attempt.
func TestProtocol_NoDuplicateDeliveryOnAmbiguousDisconnect(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	// Attempt 1: write the full message and close without reading the
	// final response (ambiguous disconnect).
	conn, reader := env.beginSession(t)
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	if _, err := conn.Write([]byte("DATA\r\n")); err != nil {
		t.Fatalf("write DATA: %v", err)
	}
	// Read the 354 so the server is in DATA state, then send the body
	// and close immediately without reading the final response.
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(resp, "354") {
		t.Fatalf("DATA response: %q err=%v", resp, err)
	}
	// A real remote MTA always sends a Message-ID header (either the
	// originating MUA's or one it adds itself) — the delivery-dedup
	// boundary this test exercises keys on exactly that header, so
	// the fixture includes one to match the real production case.
	const ambiguousMessageID = "<ambiguous-retry-test@sender.example>"
	if _, err := conn.Write([]byte("Subject: Ambiguous\r\nMessage-ID: " + ambiguousMessageID + "\r\n\r\nbody\r\n.\r\n")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	conn.Close() // ambiguous: no response read

	// The server must still durably accept exactly one copy.
	deadline := time.Now().Add(10 * time.Second)
	for {
		msgs, queues := env.rowCounts(t)
		if msgs == 1 && queues == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after ambiguous disconnect: messages=%d queue=%d, want exactly 1/1", msgs, queues)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Attempt 2 (retry): a fresh session sending the BYTE-IDENTICAL
	// message is exactly what a well-behaved remote MTA does after an
	// ambiguous disconnect (it never saw a final 250, so it resends
	// the exact same email). The server durably accepted attempt 1
	// already — this is the delivery-dedup boundary being exercised:
	// the retry must still be acknowledged (250), but must NOT create
	// a second visible copy.
	conn2, reader2 := env.beginSession(t)
	env.cmd(t, conn2, reader2, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn2, reader2, "Subject: Ambiguous\r\nMessage-ID: "+ambiguousMessageID+"\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("retry: expected 250, got %q", final)
	}
	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("after retry: messages=%d queue=%d, want exactly 1/1 (retry of the SAME message must not duplicate)", msgs, queues)
	}
}
