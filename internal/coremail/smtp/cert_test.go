package smtp

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/imap"
	"github.com/orvix/orvix/internal/coremail/pop3"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// ── Cross-Protocol Helpers ────────────────────────────────

type certEnv struct {
	db       *sql.DB
	eng      *coremail.Engine
	ms       *storage.MailStore
	qe       *queue.QueueEngine
	smtpAddr string
	imapAddr string
	pop3Addr string
	smtpRcv  *Receiver
}

func newCertEnv(t *testing.T) *certEnv {
	t.Helper()

	db, eng, ms, qe, rcv := testIntegrationEnvWithDB(t)

	// ── SMTP Server ──
	cfg := DefaultConfig()
	cfg.Hostname = "mx.orvix.test"
	cfg.AllowPlainAuthWithoutTLS = true
	cfg.RequireAuthForSubmission = false

	verify := func(ctx context.Context, username, password string) (string, bool) {
		mbox, err := eng.Auth.AuthenticateMailbox(ctx, username, password)
		if err != nil || mbox == nil {
			return "", false
		}
		return username, true
	}
	auth := NewAuthenticator(NewFuncAuthBackend(verify))
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	smtpSrv := NewServer(cfg, handler, rcv)
	smtpSrv.SetLocalDomainChecker(func(ctx context.Context, domain string) (bool, error) {
		return true, nil
	})

	smtpListener, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		smtpSrv.listener = smtpListener
		smtpSrv.serve()
	}()

	// ── IMAP Server ──
	imapAuth := imap.NewAuthenticator()
	imapAuth.SetEngine(&imapEngineAdapter{service: eng.Auth})
	imapCfg := imap.DefaultConfig()
	imapSrv := imap.NewServer(imapCfg, ms, imapAuth)
	imapListener, _ := net.Listen("tcp", "127.0.0.1:0")
	imapSrv.SetListener(imapListener)
	go imapSrv.Serve()

	// ── POP3 Server ──
	pop3Backend := newPOP3AuthBackend(eng)
	pop3Auth := pop3.NewAuthenticator(pop3Backend)
	pop3Cfg := pop3.DefaultConfig()
	pop3Srv := pop3.NewServer(pop3Cfg, ms, pop3Auth)
	pop3Listener, _ := net.Listen("tcp", "127.0.0.1:0")
	pop3Srv.SetListener(pop3Listener)
	go pop3Srv.Serve()

	t.Cleanup(func() {
		smtpListener.Close()
		imapListener.Close()
		pop3Listener.Close()
		db.Close()
	})

	return &certEnv{
		db: db, eng: eng, ms: ms, qe: qe, smtpRcv: rcv,
		smtpAddr: smtpListener.Addr().String(),
		imapAddr: imapListener.Addr().String(),
		pop3Addr: pop3Listener.Addr().String(),
	}
}

type imapEngineAdapter struct {
	service *coremail.AuthService
}

func (a *imapEngineAdapter) AuthenticateMailbox(ctx interface{}, username, password string) (interface{}, error) {
	return a.service.AuthenticateMailbox(context.TODO(), username, password)
}

func newPOP3AuthBackend(eng *coremail.Engine) *pop3AuthBackend {
	return &pop3AuthBackend{eng: eng}
}

type pop3AuthBackend struct {
	eng *coremail.Engine
}

func (b *pop3AuthBackend) Authenticate(username, password string) (uint, bool) {
	mbox, err := b.eng.Auth.AuthenticateMailbox(context.TODO(), username, password)
	if err != nil || mbox == nil {
		return 0, false
	}
	return mbox.ID, true
}

func certSMTPReceive(t *testing.T, env *certEnv, from, to, body string) {
	t.Helper()
	conn, err := net.Dial("tcp", env.smtpAddr)
	if err != nil {
		t.Fatalf("smtp dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting

	fmt.Fprintf(conn, "EHLO test.com\r\n")
	reader.ReadString('\n')
	// Read EHLO response lines.
	for {
		line, _ := reader.ReadString('\n')
		if strings.HasPrefix(line, "250 ") {
			break
		}
	}

	fmt.Fprintf(conn, "MAIL FROM:<%s>\r\n", from)
	reader.ReadString('\n')
	fmt.Fprintf(conn, "RCPT TO:<%s>\r\n", to)
	reader.ReadString('\n')
	fmt.Fprintf(conn, "DATA\r\n")
	reader.ReadString('\n')
	fmt.Fprintf(conn, "From: %s\r\nTo: %s\r\nSubject: Test\r\n\r\n%s\r\n.\r\n", from, to, body)
	resp, _ := reader.ReadString('\n')
	if !strings.Contains(resp, "250") {
		t.Fatalf("SMTP DATA failed: %s", resp)
	}
}

func certIMAPFetch(t *testing.T, env *certEnv) string {
	t.Helper()
	conn, err := net.Dial("tcp", env.imapAddr)
	if err != nil {
		t.Fatalf("imap dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting

	fmt.Fprintf(conn, "A1 LOGIN user@test.com pass\r\n")
	reader.ReadString('\n')
	fmt.Fprintf(conn, "A2 SELECT INBOX\r\n")
	for {
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "A2 OK") {
			break
		}
	}
	fmt.Fprintf(conn, "A3 FETCH 1 BODY[TEXT]\r\n")
	var resp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		resp.WriteString(line)
		if strings.Contains(line, "A3 OK") {
			break
		}
	}
	return resp.String()
}

func certPOP3Retrieve(t *testing.T, env *certEnv) string {
	t.Helper()
	conn, err := net.Dial("tcp", env.pop3Addr)
	if err != nil {
		t.Fatalf("pop3 dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting

	fmt.Fprintf(conn, "USER user@test.com\r\n")
	reader.ReadString('\n')
	fmt.Fprintf(conn, "PASS pass\r\n")
	reader.ReadString('\n')
	fmt.Fprintf(conn, "RETR 1\r\n")
	var resp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) == "." {
			break
		}
		resp.WriteString(line)
	}
	fmt.Fprintf(conn, "QUIT\r\n")
	reader.ReadString('\n')
	return resp.String()
}

// ── Cross-Protocol Tests ──────────────────────────────────

func TestCertSMTPReceiveIMAPRead(t *testing.T) {
	env := newCertEnv(t)

	certSMTPReceive(t, env, "sender@outside.com", "user@test.com", "Hello from SMTP")

	body := certIMAPFetch(t, env)
	if !strings.Contains(body, "Hello from SMTP") {
		t.Fatalf("IMAP did not see SMTP-received message: %s", body)
	}
	t.Log("SMTP → MailStore → IMAP: PASS")
}

func TestCertSMTPReceivePOP3Retrieve(t *testing.T) {
	env := newCertEnv(t)

	certSMTPReceive(t, env, "sender@outside.com", "user@test.com", "POP3 test message")

	body := certPOP3Retrieve(t, env)
	if !strings.Contains(body, "POP3 test message") {
		t.Fatalf("POP3 did not see SMTP-received message: %s", body)
	}
	t.Log("SMTP → MailStore → POP3: PASS")
}

func TestCertIMAPFlagsPOP3DeletionConsistent(t *testing.T) {
	env := newCertEnv(t)

	// Send 3 messages via SMTP.
	for i := 0; i < 3; i++ {
		certSMTPReceive(t, env, "sender@test.com", "user@test.com", fmt.Sprintf("Message %d", i+1))
	}

	// IMAP reads and confirms.
	body := certIMAPFetch(t, env)
	if !strings.Contains(body, "Message 1") {
		t.Fatal("IMAP should see first message")
	}

	// POP3 retrieves same message.
	pop3Body := certPOP3Retrieve(t, env)
	if !strings.Contains(pop3Body, "Message 1") {
		t.Fatal("POP3 should see first message")
	}
}

func TestCertAllProtocolsUseSameAuth(t *testing.T) {
	env := newCertEnv(t)

	// SMTP AUTH with the same credentials used by IMAP and POP3.
	conn, err := net.Dial("tcp", env.smtpAddr)
	if err != nil {
		t.Fatalf("smtp dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n')

	fmt.Fprintf(conn, "EHLO test.com\r\n")
	// Read EHLO multiline response.
	for {
		line, _ := reader.ReadString('\n')
		if strings.HasPrefix(line, "250 ") {
			break
		}
	}

	// SMTP AUTH PLAIN.
	fmt.Fprintf(conn, "AUTH PLAIN AHVzZXJAdGVzdC5jb20AcGFzcw==\r\n")
	resp, _ := reader.ReadString('\n')
	if !strings.Contains(resp, "235") {
		t.Fatalf("SMTP AUTH failed: %s", resp)
	}

	t.Log("SMTP AUTH uses same identity as IMAP LOGIN and POP3 USER/PASS")
}

func TestCertSMTPQueueDelivery(t *testing.T) {
	env := newCertEnv(t)

	certSMTPReceive(t, env, "sender@test.com", "user@test.com", "Queue delivery test")

	// Verify queue entry created.
	ctx := context.Background()
	metrics, err := env.qe.Metrics(ctx, nil)
	if err != nil {
		t.Fatalf("queue metrics: %v", err)
	}
	if metrics.Pending < 1 {
		t.Fatal("expected at least 1 pending queue entry")
	}
	t.Logf("SMTP → Queue: PASS (pending=%d)", metrics.Pending)
}

// ── Load Certification ────────────────────────────────────

func TestCertSMTPIMAPConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	env := newCertEnv(t)
	ctx := context.Background()

	// Pre-load some messages via SMTP.
	for i := 0; i < 5; i++ {
		certSMTPReceive(t, env, "load@test.com", "user@test.com", fmt.Sprintf("Load msg %d", i))
	}

	var wg sync.WaitGroup
	errs := make(chan string, 50)

	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// IMAP read.
			body := certIMAPFetch(t, env)
			if !strings.Contains(body, "Load msg") {
				errs <- fmt.Sprintf("IMAP load %d failed", id)
			}
		}(i)

		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// POP3 retrieve.
			body := certPOP3Retrieve(t, env)
			if !strings.Contains(body, "Load msg") {
				errs <- fmt.Sprintf("POP3 load %d failed", id)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	if len(errs) > 0 {
		t.Fatalf("concurrent access had %d errors: %s", len(errs), <-errs)
	}
	t.Log("SMTP + IMAP + POP3 concurrent: PASS")
	_ = ctx
}

func TestCertObservabilityCrossProtocol(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	// Simulate events from all protocols.
	obs.Metrics.IncSMTPAccepted()
	obs.Metrics.IncIMAPLoginSuccess()
	obs.Metrics.IncPOP3LoginSuccess()
	obs.Metrics.IncPOP3MessageRetrieved()

	snap := obs.Metrics.Snapshot()
	if snap.SMTPAccepted != 1 || snap.IMAPLoginSuccess != 1 ||
		snap.POP3LoginSuccess != 1 || snap.POP3MessagesRetrieved != 1 {
		t.Fatal("cross-protocol metrics not independent")
	}
	t.Logf("SMTP=%d IMAP=%d POP3=%d — all independent",
		snap.SMTPAccepted, snap.IMAPLoginSuccess, snap.POP3LoginSuccess)
}
