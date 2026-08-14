package smtp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// ── Mailbox-level mail-access policy enforcement (real wiring) ─────
//
// These tests exercise the CANONICAL policy on the REAL SMTP path:
// a real SQLite database, the real coremail engine, the real
// mailpolicy.Policy wired through Server.SetMailAccessPolicy, real
// AUTH through the IdentityService, and a real TCP server. They prove
// the enforcement matrix from the phase spec:
//
//	internal auth mailbox -> local:             allow
//	internal auth mailbox -> external:          deny
//	external-enabled auth mailbox -> local:     allow
//	external-enabled auth mailbox -> external:  allow
//	remote unauthenticated -> internal mailbox: deny
//	trusted local auth sender -> internal mbox: allow
//	spoofed local MAIL FROM (remote) -> internal: deny
//	policy repository failure:                  temp-fail (never deliver)

type policyTestEnv struct {
	addr string
	db   *sql.DB
	eng  *coremail.Engine
	ms   *storage.MailStore
}

func newPolicyTestEnv(t *testing.T) *policyTestEnv {
	t.Helper()
	db, eng, ms, qe, _ := testIntegrationEnvWithDB(t)

	// Provision the policy test domains + mailboxes with explicit
	// per-mailbox access modes.
	seedPolicySMTPMailbox(t, db, "internal.test", "alice", "alice@internal.test", string(mailpolicy.ModeInternalOnly), "alicepass")
	seedPolicySMTPMailbox(t, db, "open.test", "carol", "carol@open.test", string(mailpolicy.ModeInternalExternal), "carolpass")

	cfg := DefaultConfig()
	// Inbound-port behavior: unauthenticated local delivery is
	// allowed (the relay-protection and policy checks decide what an
	// unauthenticated sender may do), while AUTH remains available
	// for the authenticated-sender cases.
	cfg.RequireAuthForSubmission = false
	cfg.RequireTLSForAuth = false
	cfg.AllowPlainAuthWithoutTLS = true

	identity := NewIdentityService(eng)
	auth := NewAuthenticator(identity)
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	rcv := NewReceiver(eng, ms, qe, cfg)
	srv := NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(identity.IsLocalDomain)
	srv.SetMailAccessPolicy(func(ctx context.Context, session *Session, rcptAddr string, rcptIsLocal bool) (bool, string, error) {
		pol := mailpolicy.New(&mailpolicy.EngineStore{Engine: eng}, mailpolicy.NopSink{})
		if !rcptIsLocal {
			if session.Authenticated && session.AuthIdentity != nil {
				decision := pol.CheckOutbound(ctx, "smtp_outbound", session.AuthIdentity.Username, []string{rcptAddr})
				switch {
				case decision.Allowed:
					return true, "", nil
				case decision.Unavailable:
					return false, "", errors.New("policy unavailable")
				case decision.Denied:
					return false, string(decision.Reason), nil
				}
			}
			return true, "", nil
		}
		sender := mailpolicy.Sender{Authenticated: session.Authenticated}
		if session.AuthIdentity != nil {
			sender.MailboxEmail = session.AuthIdentity.Username
		}
		decision := pol.CheckInboundRecipient(ctx, "smtp_inbound", sender, rcptAddr)
		switch {
		case decision.Allowed:
			return true, "", nil
		case decision.Unavailable:
			return false, "", errors.New("policy unavailable")
		case decision.Denied:
			return false, string(decision.Reason), nil
		}
		return true, "", nil
	})
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
	return &policyTestEnv{addr: addr, db: db, eng: eng, ms: ms}
}

func seedPolicySMTPMailbox(t *testing.T, db *sql.DB, domainName, localPart, email, mode, password string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at)
		 VALUES (?, 1, 'active', 'smb', 'internal_external', datetime('now'), datetime('now'))
		 ON CONFLICT(name) DO NOTHING`, domainName); err != nil {
		t.Fatalf("seed domain %s: %v", domainName, err)
	}
	var domainID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name=?`, domainName).Scan(&domainID); err != nil {
		t.Fatalf("domain id %s: %v", domainName, err)
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	hash, err := eng.Auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, mail_access_mode, version, created_at, updated_at)
		 VALUES (?, 1, ?, ?, ?, ?, 'argon2id', 'active', ?, 1, datetime('now'), datetime('now'))`,
		domainID, localPart, email, localPart, hash, mode); err != nil {
		t.Fatalf("seed mailbox %s: %v", email, err)
	}
}

func (e *policyTestEnv) smtpConn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", e.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(greeting, "220") {
		t.Fatalf("greeting: %q %v", greeting, err)
	}
	return conn, reader
}

func (e *policyTestEnv) authPlain(t *testing.T, conn net.Conn, reader *bufio.Reader, user, pass string) {
	t.Helper()
	creds := "\x00" + user + "\x00" + pass
	encoded := base64.StdEncoding.EncodeToString([]byte(creds))
	if resp := sendCmd(conn, reader, "AUTH PLAIN "+encoded); !strings.HasPrefix(resp, "235") {
		t.Fatalf("AUTH PLAIN: %s", resp)
	}
}

// drainMultiline reads every line of a multiline SMTP response
// (continuations use "NNN-", the final line uses "NNN ").
func drainMultiline(reader *bufio.Reader, first string) string {
	last := first
	for strings.HasPrefix(last, "250-") || strings.HasPrefix(last, "220-") {
		line, err := reader.ReadString('\n')
		if err != nil {
			return last
		}
		last = strings.TrimSpace(line)
	}
	return last
}

func (e *policyTestEnv) ehlo(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	resp := sendCmd(conn, reader, "EHLO client.test")
	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("EHLO: %s", resp)
	}
	drainMultiline(reader, resp)
}

func (e *policyTestEnv) rcpt(t *testing.T, conn net.Conn, reader *bufio.Reader, mailFrom, rcptTo string) string {
	t.Helper()
	if mailFrom != "" {
		if resp := sendCmd(conn, reader, "MAIL FROM:<"+mailFrom+">"); !strings.HasPrefix(resp, "250") {
			t.Fatalf("MAIL FROM %s: %s", mailFrom, resp)
		}
	}
	return sendCmd(conn, reader, "RCPT TO:<"+rcptTo+">")
}

// ── The enforcement matrix ─────────────────────────────────────────

func TestPolicyMatrix_InternalAuthMailboxToLocalAllowed(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.authPlain(t, conn, reader, "alice@internal.test", "alicepass")
	resp := env.rcpt(t, conn, reader, "alice@internal.test", "user@test.com")
	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("internal->local must be allowed, got %s", resp)
	}
}

func TestPolicyMatrix_InternalAuthMailboxToExternalDenied(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.authPlain(t, conn, reader, "alice@internal.test", "alicepass")
	resp := env.rcpt(t, conn, reader, "alice@internal.test", "bob@external.test")
	if !strings.HasPrefix(resp, "550") || !strings.Contains(resp, "5.7.1") {
		t.Fatalf("internal->external must be denied with 5.7.1, got %s", resp)
	}
}

func TestPolicyMatrix_ExternalEnabledMailboxToLocalAllowed(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.authPlain(t, conn, reader, "carol@open.test", "carolpass")
	resp := env.rcpt(t, conn, reader, "carol@open.test", "user@test.com")
	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("external-enabled->local must be allowed, got %s", resp)
	}
}

func TestPolicyMatrix_ExternalEnabledMailboxToExternalAllowed(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.authPlain(t, conn, reader, "carol@open.test", "carolpass")
	resp := env.rcpt(t, conn, reader, "carol@open.test", "bob@external.test")
	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("external-enabled->external must be allowed, got %s", resp)
	}
}

func TestPolicyMatrix_RemoteUnauthenticatedToInternalMailboxDenied(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.ehlo(t, conn, reader)
	resp := env.rcpt(t, conn, reader, "outsider@external.test", "alice@internal.test")
	if !strings.HasPrefix(resp, "550") || !strings.Contains(resp, "5.7.1") {
		t.Fatalf("remote unauthenticated -> internal mailbox must be denied, got %s", resp)
	}
}

func TestPolicyMatrix_TrustedLocalAuthSenderToInternalMailboxAllowed(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.authPlain(t, conn, reader, "carol@open.test", "carolpass")
	resp := env.rcpt(t, conn, reader, "carol@open.test", "alice@internal.test")
	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("trusted local authenticated sender -> internal mailbox must be allowed, got %s", resp)
	}
}

func TestPolicyMatrix_SpoofedLocalMailFromFromRemotePathDenied(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.ehlo(t, conn, reader)
	// The MAIL FROM domain is hosted locally, but the session is NOT
	// authenticated — this is a forged local address from a remote
	// path and must be treated as external.
	resp := env.rcpt(t, conn, reader, "carol@open.test", "alice@internal.test")
	if !strings.HasPrefix(resp, "550") || !strings.Contains(resp, "5.7.1") {
		t.Fatalf("spoofed local MAIL FROM must be denied for internal-only recipient, got %s", resp)
	}
}

func TestPolicyMatrix_UnauthenticatedExternalRelayStillDenied(t *testing.T) {
	env := newPolicyTestEnv(t)
	conn, reader := env.smtpConn(t)
	env.ehlo(t, conn, reader)
	resp := env.rcpt(t, conn, reader, "outsider@external.test", "bob@external.test")
	if !strings.HasPrefix(resp, "550") {
		t.Fatalf("unauthenticated relay must stay denied, got %s", resp)
	}
}

// ── Policy repository failure fails closed ─────────────────────────

type failingPolicyStore struct{}

func (failingPolicyStore) SenderIdentity(context.Context, string) (mailpolicy.SenderIdentity, error) {
	return mailpolicy.SenderIdentity{}, errors.New("database unavailable")
}
func (failingPolicyStore) RecipientEffectiveMode(context.Context, string) (mailpolicy.EffectiveMode, error) {
	return mailpolicy.EffectiveMode{}, errors.New("database unavailable")
}
func (failingPolicyStore) RecipientIsLocal(context.Context, string) (bool, error) {
	return false, errors.New("database unavailable")
}
func (failingPolicyStore) IsLocalDomain(context.Context, string) (bool, error) {
	return false, errors.New("database unavailable")
}

func TestPolicyMatrix_StoreFailureFailsClosedWithTempFailure(t *testing.T) {
	env := newPolicyTestEnv(t)
	// Replace the wired policy with one whose store always fails.
	// The server must answer with a temporary failure (4xx), never
	// accept the recipient for delivery.
	_ = env
	db, _, ms, qe, _ := testIntegrationEnvWithDB(t)
	cfg := DefaultConfig()
	cfg.RequireAuthForSubmission = false
	identity := NewIdentityService(coremail.NewEngine(coremail.EngineConfig{DB: db}))
	auth := NewAuthenticator(identity)
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	rcv := NewReceiver(coremail.NewEngine(coremail.EngineConfig{DB: db}), ms, qe, cfg)
	srv := NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(identity.IsLocalDomain)
	pol := mailpolicy.New(failingPolicyStore{}, mailpolicy.NopSink{})
	srv.SetMailAccessPolicy(func(ctx context.Context, session *Session, rcptAddr string, rcptIsLocal bool) (bool, string, error) {
		if !rcptIsLocal {
			return true, "", nil
		}
		d := pol.CheckInboundRecipient(ctx, "smtp_inbound", mailpolicy.Sender{Authenticated: false}, rcptAddr)
		if d.Unavailable {
			return false, "", errors.New("policy unavailable")
		}
		return d.Allowed, string(d.Reason), nil
	})
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

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(greeting, "220") {
		t.Fatalf("greeting: %q %v", greeting, err)
	}
	ehloResp := sendCmd(conn, reader, "EHLO client.test")
	if !strings.HasPrefix(ehloResp, "250") {
		t.Fatalf("EHLO: %s", ehloResp)
	}
	drainMultiline(reader, ehloResp)
	if resp := sendCmd(conn, reader, "MAIL FROM:<outsider@external.test>"); !strings.HasPrefix(resp, "250") {
		t.Fatalf("MAIL FROM: %s", resp)
	}
	resp := sendCmd(conn, reader, "RCPT TO:<user@test.com>")
	if !strings.HasPrefix(resp, "421") {
		t.Fatalf("policy store failure must fail closed with a temporary failure (421), got %s", resp)
	}
}
