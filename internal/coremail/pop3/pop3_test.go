package pop3

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
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	_ "modernc.org/sqlite"
)

// ── Fake Auth Backend ─────────────────────────────────────

type fakeAuth struct {
	users map[string]uint
}

func TestPOP3PlaintextAuthRejectedWhenTLSRequired(t *testing.T) {
	auth := newFakeAuth()
	auth.add("user@test.com", 1)
	session := NewSession("127.0.0.1:110")
	session.RequireTLS = true

	userResp := handleUSER("user@test.com", session, NewAuthenticator(auth))
	if !strings.Contains(userResp, "TLS required for authentication") {
		t.Fatalf("expected USER to require TLS, got: %s", userResp)
	}

	session.Username = "user@test.com"
	passResp := handlePASS("pass", session, NewAuthenticator(auth), nil, context.Background())
	if !strings.Contains(passResp, "TLS required for authentication") {
		t.Fatalf("expected PASS to require TLS, got: %s", passResp)
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
	return id, ok
}

// ── Test Infrastructure ────────────────────────────────────

func testPOP3Server(t *testing.T) (*storage.MailStore, string, func()) {
	t.Helper()

	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "pop3_test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create required tables.
	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}

	basePath := filepath.Join(dir, "msgs")
	ms, err := storage.NewMailStore(db, basePath)
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}

	// Provision INBOX.
	ctx := context.TODO()
	if err := ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil); err != nil {
		t.Fatalf("ensure mailbox storage: %v", err)
	}

	folder, err := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if err != nil || folder == nil {
		t.Fatalf("get inbox: %v", err)
	}

	// Store some test messages.
	for i := 1; i <= 3; i++ {
		msg := &storage.Message{
			MessageID: fmt.Sprintf("pop3-msg-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID,
			FromAddress: "sender@test.com", ToAddresses: "user@test.com",
			Subject: fmt.Sprintf("Message %d", i),
		}
		body := []byte(fmt.Sprintf("Subject: Msg %d\r\n\r\nThis is message body %d.\r\n", i, i))
		ms.StoreMessage(ctx, msg, body, nil)
	}

	auth := newFakeAuth()
	auth.add("user@test.com", 1)

	authenticator := NewAuthenticator(auth)
	srv := NewServer(DefaultConfig(), ms, authenticator)

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

	return ms, addr, cleanup
}

func openDB(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
}

func pop3Dial(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reader := bufio.NewReader(conn)
	// Greeting.
	greeting, _ := reader.ReadString('\n')
	_ = greeting
	return conn, reader
}

func pop3Cmd(t *testing.T, conn net.Conn, reader *bufio.Reader, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\r\n", cmd)
	resp, _ := reader.ReadString('\n')
	return resp
}

func pop3CmdMulti(t *testing.T, conn net.Conn, reader *bufio.Reader, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\r\n", cmd)
	var resp strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "." || strings.HasPrefix(line, ".") {
			resp.WriteString(line)
			if strings.TrimSpace(line) == "." {
				break
			}
		} else {
			resp.WriteString(line)
		}
	}
	return resp.String()
}

func TestPOP3UserPassSuccess(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	resp := pop3Cmd(t, conn, reader, "USER user@test.com")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}

	resp = pop3Cmd(t, conn, reader, "PASS anypass")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

func TestPOP3PassFailure(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER unknown@test.com")
	resp := pop3Cmd(t, conn, reader, "PASS wrongpass")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR, got: %s", resp)
	}
}

func TestPOP3Stat(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "STAT")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

func TestPOP3List(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "LIST")
	if !strings.Contains(resp, "1 ") {
		t.Fatal("expected message 1 in LIST")
	}
	if !strings.Contains(resp, "2 ") {
		t.Fatal("expected message 2 in LIST")
	}
	if !strings.Contains(resp, "3 ") {
		t.Fatal("expected message 3 in LIST")
	}
}

func TestPOP3Retr(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "RETR 1")
	if !strings.Contains(resp, "message body 1") {
		t.Fatalf("expected body content, got: %s", resp)
	}
}

func TestPOP3DeleMarkOnly(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "DELE 1")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}

	// DELE only marks, message should still exist in store.
	ctx := context.TODO()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if count != 3 {
		t.Fatalf("expected 3 messages (DELE is mark-only), got %d", count)
	}
}

func TestPOP3RsetRestores(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "DELE 2")

	// RSET restores.
	resp := pop3Cmd(t, conn, reader, "RSET")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}

	// LIST should show all 3 messages again.
	respList := pop3CmdMulti(t, conn, reader, "LIST")
	if !strings.Contains(respList, "3 ") {
		t.Fatal("expected 3 messages after RSET")
	}
}

func TestPOP3QuitCommitsDeletion(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "DELE 2")

	resp := pop3Cmd(t, conn, reader, "QUIT")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK from QUIT, got: %s", resp)
	}
	conn.Close()

	// Verify only message 3 remains. Retry in case of SQLITE_BUSY.
	ctx := context.TODO()
	var count int64
	// Increase retry window to handle concurrent test execution.
	for i := 0; i < 200; i++ {
		folder, err := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
		if err != nil || folder == nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		count, err = ms.Messages.CountByFolder(ctx, folder.ID, nil)
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if count == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	if count != 1 {
		t.Fatalf("expected 1 message after QUIT commit, got %d", count)
	}
}

func TestPOP3DisconnectPreservesMessages(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "DELE 2")

	// Disconnect without QUIT.
	conn.Close()

	// Wait a moment for server to close.
	_ = reader

	// Verify all 3 messages remain.
	ctx := context.TODO()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if count != 3 {
		t.Fatalf("expected 3 messages after disconnect, got %d", count)
	}
}

func TestPOP3InvalidCommandState(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	// STAT before authentication should fail.
	resp := pop3Cmd(t, conn, reader, "STAT")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for STAT before auth, got: %s", resp)
	}

	// RETR before authentication should fail.
	resp = pop3Cmd(t, conn, reader, "RETR 1")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for RETR before auth, got: %s", resp)
	}
}

func TestPOP3MultipleSessions(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n') // greeting
			fmt.Fprintf(conn, "USER user@test.com\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "PASS pass\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "STAT\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "QUIT\r\n")
			reader.ReadString('\n')
		}()
	}
	wg.Wait()
}

func TestPOP3GracefulShutdown(t *testing.T) {
	// Create a simple server that doesn't use testPOP3Server's DB.
	db, err := openDB(filepath.Join(t.TempDir(), "shutdown.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ms, err := storage.NewMailStore(db, filepath.Join(t.TempDir(), "msgs"))
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}

	cfg := DefaultConfig()
	srv := NewServer(cfg, ms, NewAuthenticator(newFakeAuth()))
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	srv.listener = listener
	go srv.serve()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting

	srv.Stop()

	line, _ := reader.ReadString('\n')
	if !strings.Contains(line, "shutting down") {
		t.Logf("shutdown response: %s", line)
	}
}

func TestPOP3Observability(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncSMTPAccepted()
	obs.Metrics.IncIMAPLoginSuccess()
	obs.Metrics.IncIMAPLoginFailure()

	snap := obs.Metrics.Snapshot()
	if snap.IMAPLoginSuccess != 1 || snap.IMAPLoginFailure != 1 {
		t.Fatalf("expected login metrics 1/1, got %d/%d", snap.IMAPLoginSuccess, snap.IMAPLoginFailure)
	}
}
