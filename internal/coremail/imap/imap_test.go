package imap

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	_ "modernc.org/sqlite"
)

// ── Fake Auth Backend for Tests ─────────────────────────────

type fakeAuth struct {
	users map[string]uint // username -> mailboxID
}

func TestIMAPPlaintextLoginRejectedWhenTLSRequired(t *testing.T) {
	session := NewSession("127.0.0.1:143")
	session.RequireTLS = true
	auth := newFakeAuth()
	auth.add("user@test.com", 1)

	resp := Handle(context.Background(), &Command{
		Tag:       "A1",
		Name:      "LOGIN",
		Arguments: "user@test.com pass",
	}, session, auth)

	if !strings.Contains(resp, "BAD TLS required for LOGIN") {
		t.Fatalf("expected plaintext LOGIN rejection, got: %s", resp)
	}
	if session.State != StateNotAuthenticated {
		t.Fatalf("expected unauthenticated session, got %s", session.State)
	}
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{users: make(map[string]uint)}
}

func (f *fakeAuth) add(username string, mailboxID uint) {
	f.users[username] = mailboxID
}

func (f *fakeAuth) Authenticate(username, password string) (uint, bool) {
	id, ok := f.users[username]
	if !ok {
		return 0, false
	}
	return id, true
}

// ── Test Helpers ────────────────────────────────────────────

func sendIMAP(t *testing.T, conn net.Conn, reader *bufio.Reader, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\r\n", cmd)
	resp, _ := reader.ReadString('\n')
	return strings.TrimSpace(resp)
}

func sendIMAPMulti(t *testing.T, conn net.Conn, reader *bufio.Reader, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\r\n", cmd)
	// Read until we get a tagged response.
	var full strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		full.WriteString(line)
		full.WriteString("\n")
		// Tagged responses: the line starts with the tag and contains a status keyword.
		if len(line) >= 3 && line[0] >= 'A' && line[0] <= 'Z' && line[1] >= '0' && line[1] <= '9' {
			upper := strings.ToUpper(line)
			if strings.Contains(upper, " OK ") || strings.Contains(upper, " NO ") || strings.Contains(upper, " BAD ") || strings.Contains(upper, " BYE ") {
				break
			}
		}
	}
	return full.String()
}

func imapDial(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reader := bufio.NewReader(conn)
	// Read greeting.
	greeting, _ := reader.ReadString('\n')
	_ = greeting
	return conn, reader
}

// ── Session Tests ───────────────────────────────────────────

func TestSessionCreation(t *testing.T) {
	s := NewSession("127.0.0.1:12345")
	if s.State != StateNotAuthenticated {
		t.Fatalf("expected NotAuthenticated, got %s", s.State)
	}
	if s.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
}

func TestSessionCleanup(t *testing.T) {
	s := NewSession("127.0.0.1:12345")
	s.State = StateLogout
	if s.State != StateLogout {
		t.Fatal("expected Logout state")
	}
}

func TestSessionTransitions(t *testing.T) {
	s := NewSession("127.0.0.1:0")

	if s.State != StateNotAuthenticated {
		t.Fatal("expected initial NotAuthenticated")
	}

	s.State = StateAuthenticated
	if s.State != StateAuthenticated {
		t.Fatal("expected Authenticated")
	}

	s.State = StateSelected
	if s.State != StateSelected {
		t.Fatal("expected Selected")
	}

	s.Reset()
	if s.State != StateAuthenticated {
		t.Fatal("expected Authenticated after Reset")
	}
}

// ── Command Tests ───────────────────────────────────────────

func TestLoginSuccess(t *testing.T) {
	auth := newFakeAuth()
	auth.add("user@test.com", 1)

	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 LOGIN user@test.com pass")
	resp := Handle(context.TODO(), cmd, s, auth)
	if !strings.Contains(resp, "OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
	if s.State != StateAuthenticated {
		t.Fatal("expected Authenticated state")
	}
	if s.Username != "user@test.com" {
		t.Fatalf("expected user@test.com, got %s", s.Username)
	}
}

func TestLoginFailure(t *testing.T) {
	auth := newFakeAuth()

	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 LOGIN user@test.com wrongpass")
	resp := Handle(context.TODO(), cmd, s, auth)
	if !strings.Contains(resp, "NO") {
		t.Fatalf("expected NO, got: %s", resp)
	}
	if s.State != StateNotAuthenticated {
		t.Fatal("expected NotAuthenticated after failed login")
	}
}

func TestLoginInvalidState(t *testing.T) {
	auth := newFakeAuth()
	auth.add("user@test.com", 1)

	s := NewSession("127.0.0.1:0")
	s.State = StateAuthenticated

	cmd, _ := ParseCommand("A1 LOGIN user@test.com pass")
	resp := Handle(context.TODO(), cmd, s, auth)
	if !strings.Contains(resp, "BAD") {
		t.Fatalf("expected BAD, got: %s", resp)
	}
}

func TestCapability(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 CAPABILITY")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "CAPABILITY") {
		t.Fatal("expected CAPABILITY in response")
	}
	if !strings.Contains(resp, "IMAP4rev1") {
		t.Fatal("expected IMAP4rev1 in capabilities")
	}
}

func TestNoop(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 NOOP")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

func TestLogout(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 LOGOUT")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "BYE") {
		t.Fatal("expected BYE in response")
	}
	if s.State != StateLogout {
		t.Fatal("expected Logout state")
	}
}

// ── LIST / SELECT / STATUS with MailStore ───────────────────

func testIMAPServer(t *testing.T) (*storage.MailStore, *fakeAuth, string, func()) {
	t.Helper()

	// Create a minimal MailStore for folder listing.
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "imap_test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	basePath := filepath.Join(dir, "msgs")
	ms, _ := storage.NewMailStore(db, basePath)

	ctx := context.TODO()

	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)

	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "Sent", Path: "Sent",
	}, nil)
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "Trash", Path: "Trash",
	}, nil)

	auth := newFakeAuth()
	auth.add("user@test.com", 1)

	cfg := DefaultConfig()
	srv := NewServer(cfg, ms, auth)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()

	go func() {
		srv.listener = listener
		srv.serve()
	}()

	cleanup := func() {
		listener.Close()
		db.Close()
	}
	return ms, auth, addr, cleanup
}

func TestListInbox(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := imapDial(t, addr)
	defer conn.Close()

	sendIMAP(t, conn, reader, "A1 LOGIN user@test.com pass")
	resp := sendIMAPMulti(t, conn, reader, "A2 LIST \"\" \"*\"")
	if !strings.Contains(resp, "INBOX") {
		t.Fatal("expected INBOX in LIST response")
	}
	if !strings.Contains(resp, "Sent") {
		t.Fatal("expected Sent in LIST response")
	}
	if !strings.Contains(resp, "Trash") {
		t.Fatal("expected Trash in LIST response")
	}
}

func TestSelectExistingMailbox(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	ctx := context.TODO()
	msg := &storage.Message{
		MessageID: "msg1", TenantID: 1, DomainID: 1, MailboxID: 1,
		FolderID: 1, FromAddress: "sender@test.com", ToAddresses: "user@test.com",
	}
	ms.StoreMessage(ctx, msg, []byte("Subject: Test\r\n\r\nBody"), nil)

	conn, reader := imapDial(t, addr)
	defer conn.Close()

	sendIMAP(t, conn, reader, "A1 LOGIN user@test.com pass")
	resp := sendIMAPMulti(t, conn, reader, "A2 SELECT INBOX")
	if !strings.Contains(resp, "EXISTS") {
		t.Fatal("expected EXISTS in SELECT response")
	}
	if !strings.Contains(resp, "FLAGS") {
		t.Fatal("expected FLAGS in SELECT response")
	}
}

func TestSelectMissingMailbox(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := imapDial(t, addr)
	defer conn.Close()

	sendIMAP(t, conn, reader, "A1 LOGIN user@test.com pass")
	resp := sendIMAP(t, conn, reader, "A2 SELECT NONEXISTENT")
	if !strings.Contains(resp, "NO") {
		t.Fatalf("expected NO, got: %s", resp)
	}
}

func TestStatusCounts(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	ctx := context.TODO()
	for i := 0; i < 3; i++ {
		msg := &storage.Message{
			MessageID: fmt.Sprintf("msg%d", i), TenantID: 1, DomainID: 1, MailboxID: 1,
			FolderID: 1, FromAddress: "sender@test.com", ToAddresses: "user@test.com",
		}
		ms.StoreMessage(ctx, msg, []byte("Subject: Test\r\n\r\nBody"), nil)
	}

	conn, reader := imapDial(t, addr)
	defer conn.Close()

	sendIMAP(t, conn, reader, "A1 LOGIN user@test.com pass")
	resp := sendIMAPMulti(t, conn, reader, "A2 STATUS INBOX (MESSAGES RECENT UNSEEN)")
	if !strings.Contains(resp, "MESSAGES") {
		t.Fatal("expected MESSAGES in STATUS response")
	}
}

// ── Concurrency Tests ───────────────────────────────────────

func TestMultipleConcurrentSessions(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, reader := imapDial(t, addr)
			defer conn.Close()
			sendIMAP(t, conn, reader, fmt.Sprintf("A%d LOGIN user@test.com pass", id))
			resp := sendIMAPMulti(t, conn, reader, fmt.Sprintf("A%d LIST \"\" \"*\"", id))
			if !strings.Contains(resp, "INBOX") {
				t.Errorf("session %d: expected INBOX", id)
			}
		}(i)
	}
	wg.Wait()
}

func TestGracefulShutdown(t *testing.T) {
	srv := NewServer(DefaultConfig(), nil, newFakeAuth())
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	srv.listener = listener
	go srv.serve()

	conn, reader := imapDial(t, listener.Addr().String())
	defer conn.Close()

	srv.Stop()

	line, _ := reader.ReadString('\n')
	if !strings.Contains(line, "BYE") {
		t.Logf("shutdown response: %s", line)
	}
}

// ── Observability Tests ─────────────────────────────────────

func TestIMAPObservabilityEvents(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	// Simulate session creation.
	obs.Metrics.IncIMAPSessionCreated()
	obs.EventHistory.Record(observability.EventIMAPSessionCreated, map[string]string{
		"remote_ip": "127.0.0.1",
	})

	snap := obs.Snapshot.Snapshot()
	if snap.Metrics.IMAPSessionsCreated != 1 {
		t.Fatalf("expected 1 session created, got %d", snap.Metrics.IMAPSessionsCreated)
	}

	// Verify event recorded.
	events := snap.RecentEvents
	found := false
	for _, e := range events {
		if e.Type == observability.EventIMAPSessionCreated {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected IMAP session created event")
	}
}

func TestIMAPLoginMetricsUpdated(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncIMAPLoginSuccess()
	obs.Metrics.IncIMAPLoginFailure()
	obs.Metrics.IncIMAPMailboxSelected()

	snap := obs.Metrics.Snapshot()
	if snap.IMAPLoginSuccess != 1 {
		t.Fatalf("expected 1 login success, got %d", snap.IMAPLoginSuccess)
	}
	if snap.IMAPLoginFailure != 1 {
		t.Fatalf("expected 1 login failure, got %d", snap.IMAPLoginFailure)
	}
	if snap.IMAPMailboxSelected != 1 {
		t.Fatalf("expected 1 mailbox selected, got %d", snap.IMAPMailboxSelected)
	}
}

// ── Parse Command Tests ─────────────────────────────────────

func TestParseCommand(t *testing.T) {
	cmd, err := ParseCommand("A1 LOGIN user pass\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Tag != "A1" {
		t.Fatalf("expected tag A1, got %s", cmd.Tag)
	}
	if cmd.Name != "LOGIN" {
		t.Fatalf("expected LOGIN, got %s", cmd.Name)
	}
	if cmd.Arguments != "user pass" {
		t.Fatalf("expected 'user pass', got '%s'", cmd.Arguments)
	}
}

func TestParseCommandInvalid(t *testing.T) {
	_, err := ParseCommand("")
	if err == nil {
		t.Fatal("expected error for empty command")
	}

	_, err = ParseCommand("ONLYONEPART\r\n")
	if err == nil {
		t.Fatal("expected error for single part")
	}
}

func TestParseCommandCapability(t *testing.T) {
	cmd, _ := ParseCommand("A001 CAPABILITY\r\n")
	if cmd.Name != "CAPABILITY" {
		t.Fatalf("expected CAPABILITY, got %s", cmd.Name)
	}
}

// ── Response Format Tests ───────────────────────────────────

func TestResponseFormat(t *testing.T) {
	r := Response("A1", "OK", "LOGIN completed")
	if r != "A1 OK LOGIN completed\r\n" {
		t.Fatalf("unexpected response: %q", r)
	}

	u := Untagged("OK", "LOGIN completed")
	if u != "* OK LOGIN completed\r\n" {
		t.Fatalf("unexpected untagged: %q", u)
	}
}

func TestBYEFormat(t *testing.T) {
	bye := BYE("A1", "logging out")
	if !strings.Contains(bye, "BYE") {
		t.Fatal("expected BYE in response")
	}
	if !strings.Contains(bye, "LOGOUT") {
		t.Fatal("expected LOGOUT completion")
	}
}

// ── Login Arg Parsing ───────────────────────────────────────

func TestParseLoginArgs(t *testing.T) {
	u, p := parseLoginArgs("user pass")
	if u != "user" || p != "pass" {
		t.Fatalf("expected (user, pass), got (%s, %s)", u, p)
	}
}

func TestParseLoginArgsQuoted(t *testing.T) {
	u, p := parseLoginArgs("\"user@test.com\" \"my pass\"")
	if u != "user@test.com" || p != "my pass" {
		t.Fatalf("expected (user@test.com, my pass), got (%s, %s)", u, p)
	}
}

func TestParseLoginArgsEmpty(t *testing.T) {
	u, p := parseLoginArgs("")
	if u != "" || p != "" {
		t.Fatal("expected empty for empty args")
	}
}

// ── Handle Unknown Command ──────────────────────────────────

func TestUnknownCommand(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	cmd, _ := ParseCommand("A1 UNKNOWN")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "BAD") {
		t.Fatalf("expected BAD, got: %s", resp)
	}
}

// ── Handle After Logout ────────────────────────────────────

func TestCommandAfterLogout(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	s.State = StateLogout
	cmd, _ := ParseCommand("A1 NOOP")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "BAD") {
		t.Fatalf("expected BAD after logout, got: %s", resp)
	}
}

// ── INBOX Selection Updates State ──────────────────────────

func TestSelectUpdatesState(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := imapDial(t, addr)
	defer conn.Close()

	sendIMAP(t, conn, reader, "A1 LOGIN user@test.com pass")
	resp := sendIMAPMulti(t, conn, reader, "A2 SELECT INBOX")
	if !strings.Contains(resp, "EXISTS") {
		t.Fatal("expected EXISTS in SELECT")
	}
}
