package pop3

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// ── UIDL Tests ─────────────────────────────────────────────

func TestPOP3UIDLWithoutArgs(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "UIDL")
	if !strings.Contains(resp, "1 ") || !strings.Contains(resp, "2 ") || !strings.Contains(resp, "3 ") {
		t.Fatalf("expected 3 UIDLs, got: %s", resp)
	}
}

func TestPOP3TopDotStuffing(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	ctx := context.TODO()
	folder, err := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if err != nil || folder == nil {
		t.Fatalf("get inbox: %v", err)
	}
	msg := &storage.Message{
		MessageID: "dot-top-msg", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "sender@test.com", ToAddresses: "user@test.com",
		Subject: "Dot TOP",
	}
	if err := ms.StoreMessage(ctx, msg, []byte("Subject: Dot TOP\r\n\r\n.line one\r\n..line two\r\n"), nil); err != nil {
		t.Fatalf("store dot message: %v", err)
	}

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "TOP 4 2")
	if !strings.Contains(resp, "..line one") || !strings.Contains(resp, "...line two") {
		t.Fatalf("expected dot-stuffed TOP body, got: %s", resp)
	}
}

func TestPOP3UIDLWithNumber(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "UIDL 1")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

func TestPOP3UIDLInvalidMessage(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "UIDL 99")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for invalid UIDL, got: %s", resp)
	}
}

func TestPOP3UIDLAfterDele(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	resp := pop3Cmd(t, conn, reader, "UIDL 1")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for deleted UIDL, got: %s", resp)
	}
}

// ── TOP Tests ──────────────────────────────────────────────

func TestPOP3TopCommand(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "TOP 1 0")
	if !strings.Contains(resp, "Subject:") {
		t.Fatalf("expected Subject header in TOP, got: %s", resp)
	}
}

func TestPOP3TopLines(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "TOP 1 1")
	if !strings.Contains(resp, "message body") {
		t.Fatalf("expected body content in TOP, got: %s", resp)
	}
}

func TestPOP3TopInvalidMessage(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "TOP 99 0")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for invalid TOP, got: %s", resp)
	}
}

func TestPOP3TopInvalidLineCount(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "TOP 1 abc")
	if !strings.HasPrefix(resp, "-ERR") {
		t.Fatalf("expected ERR for invalid line count, got: %s", resp)
	}
}

// ── POP3 Metrics Tests ─────────────────────────────────────

func TestPOP3DedicatedMetrics(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncPOP3Session()
	obs.Metrics.IncPOP3LoginSuccess()
	obs.Metrics.IncPOP3LoginFailure()
	obs.Metrics.IncPOP3MessageRetrieved()
	obs.Metrics.IncPOP3MessageDeleted()

	snap := obs.Metrics.Snapshot()
	if snap.POP3Sessions != 1 {
		t.Fatalf("expected 1 POP3 session, got %d", snap.POP3Sessions)
	}
	if snap.POP3LoginSuccess != 1 {
		t.Fatalf("expected 1 POP3 login success, got %d", snap.POP3LoginSuccess)
	}
	if snap.POP3LoginFailure != 1 {
		t.Fatalf("expected 1 POP3 login failure, got %d", snap.POP3LoginFailure)
	}
	if snap.POP3MessagesRetrieved != 1 {
		t.Fatalf("expected 1 POP3 retrieved, got %d", snap.POP3MessagesRetrieved)
	}
	if snap.POP3MessagesDeleted != 1 {
		t.Fatalf("expected 1 POP3 deleted, got %d", snap.POP3MessagesDeleted)
	}
}

// ── Regression Tests ───────────────────────────────────────

func TestPOP3RegressionStatUnchanged(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3Cmd(t, conn, reader, "STAT")
	if !strings.HasPrefix(resp, "+OK") {
		t.Fatalf("STAT regression: %s", resp)
	}
}

func TestPOP3RegressionListUnchanged(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "LIST")
	if !strings.Contains(resp, "1 ") {
		t.Fatal("LIST regression failed")
	}
}

func TestPOP3RegressionRetrUnchanged(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	resp := pop3CmdMulti(t, conn, reader, "RETR 1")
	if !strings.Contains(resp, "message body") {
		t.Fatal("RETR regression failed")
	}
}

func TestPOP3RegressionDeleRsetQuit(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "RSET")
	pop3Cmd(t, conn, reader, "QUIT")
	conn.Close()

	// All 3 messages should remain.
	ctx := context.TODO()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if count != 3 {
		t.Fatalf("expected 3 after RSET+QUIT, got %d", count)
	}
}

func TestPOP3RegressionDisconnectPreserves(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "DELE 2")
	conn.Close()

	// Give server time to close.
	time.Sleep(50 * time.Millisecond)

	ctx := context.TODO()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if count != 3 {
		t.Fatalf("expected 3 after disconnect, got %d", count)
	}
}

func TestPOP3RegressionQuitCommits(t *testing.T) {
	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")
	pop3Cmd(t, conn, reader, "DELE 1")
	pop3Cmd(t, conn, reader, "DELE 2")
	pop3Cmd(t, conn, reader, "QUIT")
	conn.Close()

	// All queries rest until the purge completes.
	ctx := context.TODO()
	var count int64
	for i := 0; i < 50; i++ {
		folder, err := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
		if err != nil || folder == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		count, err = ms.Messages.CountByFolder(ctx, folder.ID, nil)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected 1 after QUIT commit, got %d", count)
}

// ── Load/Concurrency Tests ────────────────────────────────

func TestPOP3Load100ConcurrentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	var wg sync.WaitGroup
	errs := make(chan string, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				errs <- fmt.Sprintf("session %d dial: %v", id, err)
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
			fmt.Fprintf(conn, "LIST\r\n")
			// Read multi-line response.
			for {
				line, _ := reader.ReadString('\n')
				if strings.TrimSpace(line) == "." {
					break
				}
			}
			fmt.Fprintf(conn, "QUIT\r\n")
			reader.ReadString('\n')
		}(i)
	}
	wg.Wait()
	close(errs)

	var failures []string
	for e := range errs {
		failures = append(failures, e)
	}
	if len(failures) > 0 {
		t.Fatalf("%d failures: %s", len(failures), failures[0])
	}
}

func TestPOP3ConcurrentRetrieveSameMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')
			fmt.Fprintf(conn, "USER user@test.com\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "PASS pass\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "RETR 1\r\n")
			// Read until dot terminator.
			for {
				line, _ := reader.ReadString('\n')
				if strings.TrimSpace(line) == "." {
					break
				}
			}
			fmt.Fprintf(conn, "QUIT\r\n")
			reader.ReadString('\n')
		}()
	}
	wg.Wait()
}

func TestPOP3ConcurrentQUITDeletionSafety(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	ms, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')
			fmt.Fprintf(conn, "USER user@test.com\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "PASS pass\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "DELE 1\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "DELE 2\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "DELE 3\r\n")
			reader.ReadString('\n')
			fmt.Fprintf(conn, "QUIT\r\n")
			reader.ReadString('\n')
		}()
	}
	wg.Wait()

	// Wait for server to settle.
	time.Sleep(100 * time.Millisecond)
	ctx := context.TODO()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	t.Logf("messages remaining after concurrent sessions: %d", count)
}

func TestPOP3NoPanicOnConnectionClose(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, _ := net.Dial("tcp", addr)
	// Close immediately without any protocol interaction.
	conn.Close()
	// If we reach here, no panic occurred.
}

// ── Failure Injection Tests ────────────────────────────────

func TestPOP3FailureInvalidMessageNumber(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()
	pop3Cmd(t, conn, reader, "USER user@test.com")
	pop3Cmd(t, conn, reader, "PASS pass")

	invalidCmds := []string{"RETR 0", "RETR -1", "RETR abc", "LIST 0"}
	for _, cmd := range invalidCmds {
		resp := pop3Cmd(t, conn, reader, cmd)
		if !strings.HasPrefix(resp, "-ERR") {
			t.Fatalf("expected ERR for '%s', got: %s", cmd, resp)
		}
	}
}

func TestPOP3FailureInvalidCommandState(t *testing.T) {
	_, addr, cleanup := testPOP3Server(t)
	defer cleanup()

	conn, reader := pop3Dial(t, addr)
	defer conn.Close()

	// Commands before authentication should fail.
	preAuthCmds := []string{"STAT", "LIST", "RETR 1", "DELE 1", "UIDL", "TOP 1 0"}
	for _, cmd := range preAuthCmds {
		resp := pop3Cmd(t, conn, reader, cmd)
		if !strings.HasPrefix(resp, "-ERR") {
			t.Fatalf("expected ERR for '%s' before auth, got: %s", cmd, resp)
		}
	}
}
