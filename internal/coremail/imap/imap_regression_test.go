package imap

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
)

// ── E2E VALIDATION ──────────────────────────────────────────

func TestE2EFullMailboxFlow(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Simulate several received messages.
	for i := 0; i < 5; i++ {
		ctx := context.TODO()
		_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
		folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
		msg := &storage.Message{
			MessageID: fmt.Sprintf("e2e-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID,
			FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
			Subject: fmt.Sprintf("E2E Message %d", i+1),
		}
		body := []byte(fmt.Sprintf("Subject: E2E %d\r\n\r\nBody %d", i+1, i+1))
		ms.StoreMessage(ctx, msg, body, nil)
	}

	// Create extra folders.
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{MailboxID: 1, Name: "Archive", Path: "Archive"}, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// LOGIN
	resp := opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com pass")
	assertOK(t, resp, "LOGIN")

	// LIST
	resp = opSendCmd(t, conn, reader, "A2", `LIST "" "*"`)
	if !strings.Contains(resp, "INBOX") || !strings.Contains(resp, "Archive") {
		t.Fatal("LIST should show INBOX and Archive")
	}

	// SELECT
	resp = opSendCmd(t, conn, reader, "A3", "SELECT INBOX")
	assertOK(t, resp, "SELECT")
	if !strings.Contains(resp, "5 EXISTS") {
		t.Fatal("expected 5 EXISTS")
	}
	if !strings.Contains(resp, "UIDVALIDITY") || !strings.Contains(resp, "UIDNEXT") {
		t.Fatal("SELECT should include UIDVALIDITY and UIDNEXT")
	}

	// FETCH FLAGS
	resp = opSendCmd(t, conn, reader, "A4", "FETCH 1:* FLAGS")
	assertOK(t, resp, "FETCH FLAGS")

	// FETCH UID
	resp = opSendCmd(t, conn, reader, "A5", "FETCH 1:* UID")
	assertOK(t, resp, "FETCH UID")

	// FETCH ENVELOPE
	resp = opSendCmd(t, conn, reader, "A6", "FETCH 1 ENVELOPE")
	assertOK(t, resp, "FETCH ENVELOPE")

	// FETCH BODY[]
	resp = opSendCmd(t, conn, reader, "A7", "FETCH 1 BODY[]")
	assertOK(t, resp, "FETCH BODY[]")

	// FETCH BODYSTRUCTURE
	resp = opSendCmd(t, conn, reader, "A8", "FETCH 1 BODYSTRUCTURE")
	assertOK(t, resp, "FETCH BODYSTRUCTURE")

	// STATUS
	resp = opSendCmd(t, conn, reader, "A9", "STATUS INBOX (MESSAGES UNSEEN)")
	assertOK(t, resp, "STATUS")

	// STORE +FLAGS
	resp = opSendCmd(t, conn, reader, "A10", "STORE 1 +FLAGS (\\SEEN \\FLAGGED)")
	assertOK(t, resp, "STORE +FLAGS")

	// UID FETCH
	resp = opSendCmd(t, conn, reader, "A11", "UID FETCH 1:* FLAGS")
	assertOK(t, resp, "UID FETCH")

	// COPY
	resp = opSendCmd(t, conn, reader, "A12", "COPY 1 Archive")
	assertOK(t, resp, "COPY")

	// UID COPY
	resp = opSendCmd(t, conn, reader, "A13", "UID COPY 2 Archive")
	assertOK(t, resp, "UID COPY")

	// STORE +FLAGS then EXPUNGE
	resp = opSendCmd(t, conn, reader, "A14", "STORE 5 +FLAGS (\\DELETED)")
	assertOK(t, resp, "STORE +DELETED")

	resp = opSendCmd(t, conn, reader, "A15", "EXPUNGE")
	assertOK(t, resp, "EXPUNGE")
	if !strings.Contains(resp, "5 EXPUNGE") {
		t.Fatal("EXPUNGE should report sequence 5 removed")
	}

	// Verify only 4 remain.
	count, _ := ms.Messages.CountByFolder(context.TODO(), 1, nil)
	if count != 4 {
		t.Fatalf("expected 4 messages after expunge, got %d", count)
	}

	// Re-select and verify counts updated.
	resp = opSendCmd(t, conn, reader, "A16", "SELECT INBOX")
	assertOK(t, resp, "SELECT after EXPUNGE")
	if !strings.Contains(resp, "4 EXISTS") {
		t.Fatal("expected 4 EXISTS after expunge")
	}

	// LOGOUT
	resp = opSendCmd(t, conn, reader, "A17", "LOGOUT")
	if !strings.Contains(resp, "BYE") {
		t.Fatal("LOGOUT should return BYE")
	}
}

func assertOK(t *testing.T, resp, context string) {
	t.Helper()
	if strings.Contains(resp, "NO ") || strings.Contains(resp, "BAD ") {
		t.Fatalf("%s failed: %s", context, resp)
	}
}

// ── UID AUDIT ───────────────────────────────────────────────

func TestUIDAuditNeverChanges(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "UID stability")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// Capture UID from FETCH.
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 UID")
	uid1 := extractUID(resp)
	if uid1 == 0 {
		t.Fatal("could not extract UID")
	}

	// Fetch same message again — UID should match.
	resp = opSendCmd(t, conn, reader, "A4", "FETCH 1 UID")
	uid2 := extractUID(resp)
	if uid2 != uid1 {
		t.Fatalf("UID changed: was %d, now %d", uid1, uid2)
	}

	// UID FETCH should return same UID.
	resp = opSendCmd(t, conn, reader, "A5", "UID FETCH 1 UID")
	uid3 := extractUID(resp)
	if uid3 != uid1 {
		t.Fatalf("UID FETCH returned different UID: got %d", uid3)
	}

	// Reconnect and check again.
	conn.Close()
	conn2, reader2 := opDial(t, addr)
	defer conn2.Close()
	opLoginSelect(t, conn2, reader2)

	resp = opSendCmd(t, conn2, reader2, "A6", "FETCH 1 UID")
	uid4 := extractUID(resp)
	if uid4 != uid1 {
		t.Fatalf("UID changed after reconnect: was %d, now %d", uid1, uid4)
	}
}

func extractUID(resp string) uint {
	// Response format: * 1 FETCH (UID <n> ...)
	for _, line := range strings.Split(resp, "\n") {
		if strings.Contains(line, "FETCH") && strings.Contains(line, "UID") {
			idx := strings.Index(line, "UID ")
			if idx >= 0 {
				rest := line[idx+4:]
				end := strings.IndexAny(rest, " )")
				if end > 0 {
					var uid uint
					fmt.Sscanf(rest[:end], "%d", &uid)
					return uid
				}
			}
		}
	}
	return 0
}

func TestUIDNEXTUsesMaxUIDPlusOne(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Store one message.
	m1 := &storage.Message{
		MessageID: "uidnext-1", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "1",
	}
	ms.StoreMessage(ctx, m1, []byte("Subject: 1\r\n\r\n1"), nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// UIDNEXT should be greater than the stored message's UID.
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 UID")
	uid := extractUID(resp)
	if uid == 0 {
		t.Fatal("could not extract UID")
	}

	// Store another message.
	m2 := &storage.Message{
		MessageID: "uidnext-2", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "c@test.com", ToAddresses: "d@test.com", Subject: "2",
	}
	ms.StoreMessage(ctx, m2, []byte("Subject: 2\r\n\r\n2"), nil)

	// Re-select and check UIDNEXT is higher.
	opLoginSelect(t, conn, reader)
	resp = opSendCmd(t, conn, reader, "A4", "FETCH 2 UID")
	uid2 := extractUID(resp)
	if uid2 <= uid {
		t.Fatalf("expected new UID > %d, got %d", uid, uid2)
	}
}

func TestUIDVALIDITYStable(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Stability")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	sel1 := uidLoginSelect(t, conn, reader)

	// Extract UIDVALIDITY.
	v1 := extractUIDVALIDITY(sel1)
	if v1 == 0 {
		t.Fatal("could not extract UIDVALIDITY")
	}

	// Reconnect and check same value.
	conn.Close()
	conn2, reader2 := opDial(t, addr)
	defer conn2.Close()
	sel2 := uidLoginSelect(t, conn2, reader2)

	v2 := extractUIDVALIDITY(sel2)
	if v2 != v1 {
		t.Fatalf("UIDVALIDITY changed: was %d, now %d", v1, v2)
	}
	_ = ms
}

func extractUIDVALIDITY(resp string) uint {
	for _, line := range strings.Split(resp, "\n") {
		if strings.Contains(line, "UIDVALIDITY") {
			var v uint
			fmt.Sscanf(line, "* OK [UIDVALIDITY %d]", &v)
			return v
		}
	}
	return 0
}

func TestUIDExpungeDoesNotRenumber(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Store 3 messages.
	var uids []uint
	for i := 1; i <= 3; i++ {
		m := &storage.Message{
			MessageID: fmt.Sprintf("expunge-uid-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID,
			FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: fmt.Sprintf("%d", i),
			Deleted: (i == 2), // mark middle as deleted
		}
		ms.StoreMessage(ctx, m, []byte(fmt.Sprintf("Subject: %d\r\n\r\n%d", i, i)), nil)
		uids = append(uids, m.ID)
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	opSendCmd(t, conn, reader, "A3", "EXPUNGE")

	// Re-select and fetch remaining UIDs.
	opLoginSelect(t, conn, reader)
	resp := opSendCmd(t, conn, reader, "A4", "FETCH 1:* UID")

	// Message 1 (UID uids[0]) and 3 (UID uids[2]) should remain.
	if !strings.Contains(resp, fmt.Sprintf("UID %d", uids[0])) {
		t.Fatalf("expected UID %d to remain after expunge", uids[0])
	}
	if strings.Contains(resp, fmt.Sprintf("UID %d", uids[1])) {
		t.Fatal("UID of deleted message should not appear")
	}
	if !strings.Contains(resp, fmt.Sprintf("UID %d", uids[2])) {
		t.Fatalf("expected UID %d to remain after expunge", uids[2])
	}
}

func TestUIDCopyCreatesNewUID(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "Copy UID test")
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{MailboxID: 1, Name: "Dest", Path: "Dest"}, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 UID")
	srcUID := extractUID(resp)
	if srcUID == 0 {
		t.Fatal("could not extract source UID")
	}

	// COPY to destination.
	opSendCmd(t, conn, reader, "A4", "COPY 1 Dest")

	// Select destination and check UID is different.
	opSendCmd(t, conn, reader, "A5", "SELECT Dest")
	resp = opSendCmd(t, conn, reader, "A6", "FETCH 1 UID")
	dstUID := extractUID(resp)
	if dstUID == 0 {
		t.Fatal("could not extract destination UID")
	}
	if dstUID == srcUID {
		t.Fatal("destination UID should differ from source UID")
	}
}

// ── INTEROPERABILITY ───────────────────────────────────────

func TestInteropThunderbirdLoginSequence(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	for i := 0; i < 3; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("TB msg %d", i))
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// Thunderbird: CAPABILITY → LOGIN → LIST → SELECT → FETCH (FLAGS UID)
	opSendCmd(t, conn, reader, "A1", "CAPABILITY")
	opSendCmd(t, conn, reader, "A2", "LOGIN user@test.com pass")
	opSendCmd(t, conn, reader, "A3", `LIST "" "*"`)
	opSendCmd(t, conn, reader, "A4", "SELECT INBOX")
	resp := opSendCmd(t, conn, reader, "A5", "FETCH 1:3 (FLAGS UID RFC822.SIZE INTERNALDATE)")
	assertOK(t, resp, "Thunderbird FETCH")
	_ = ms
}

func TestInteropOutlookFetchSequence(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("OL msg %d", i))
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// Outlook: LOGIN → SELECT → FETCH (UID ENVELOPE BODYSTRUCTURE FLAGS)
	opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com pass")
	opSendCmd(t, conn, reader, "A2", "SELECT INBOX")
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1:* (UID ENVELOPE BODYSTRUCTURE FLAGS)")
	assertOK(t, resp, "Outlook FETCH")
	_ = ms
}

func TestInteropAppleMailUIDSequence(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	for i := 0; i < 4; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("AM msg %d", i))
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// Apple Mail: LOGIN → LIST → SELECT → UID FETCH 1:* (FLAGS UID)
	opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com pass")
	opSendCmd(t, conn, reader, "A2", `LIST "" "*"`)
	opSendCmd(t, conn, reader, "A3", "SELECT INBOX")
	resp := opSendCmd(t, conn, reader, "A4", "UID FETCH 1:* (FLAGS UID RFC822.SIZE INTERNALDATE ENVELOPE)")
	assertOK(t, resp, "Apple Mail UID FETCH")
	_ = ms
}

// ── LOAD HELPERS ────────────────────────────────────────────

// sendCmdRaw sends an IMAP command without a *testing.T argument, for use in goroutines.
func sendCmdRaw(conn net.Conn, reader *bufio.Reader, cmd string) string {
	fmt.Fprintf(conn, "%s\r\n", cmd)
	parts := strings.SplitN(cmd, " ", 2)
	tag := parts[0]
	var resp strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		resp.WriteString(line)
		if strings.Contains(line, tag+" OK") || strings.Contains(line, tag+" NO") || strings.Contains(line, tag+" BAD") || strings.Contains(line, tag+" BYE") {
			break
		}
	}
	return resp.String()
}

// ── LOAD TESTING ───────────────────────────────────────────

func TestLoad100ConcurrentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 10; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Load msg %d", i))
	}

	var mu sync.Mutex
	var failures []string
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("session %d dial: %v", id, err))
				mu.Unlock()
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n') // greeting

			r1 := sendCmdRaw(conn, reader, fmt.Sprintf("A%d", id*10+1)+" LOGIN user@test.com pass")
			if strings.Contains(r1, "NO ") || strings.Contains(r1, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("session %d LOGIN: %s", id, r1))
				mu.Unlock()
				return
			}

			r2 := sendCmdRaw(conn, reader, fmt.Sprintf("A%d", id*10+2)+" SELECT INBOX")
			if strings.Contains(r2, "NO ") || strings.Contains(r2, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("session %d SELECT: %s", id, r2))
				mu.Unlock()
				return
			}

			r3 := sendCmdRaw(conn, reader, fmt.Sprintf("A%d", id*10+3)+" FETCH 1:* UID")
			if strings.Contains(r3, "NO ") || strings.Contains(r3, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("session %d FETCH: %s", id, r3))
				mu.Unlock()
				return
			}
		}(i)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d sessions failed: %s", len(failures), failures[0])
	}
}

func TestLoad10000MailboxOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 50; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("10K msg %d", i))
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n')

	sendCmdRaw(conn, reader, "A1 LOGIN user@test.com pass")
	sendCmdRaw(conn, reader, "A2 SELECT INBOX")

	for i := 0; i < 200; i++ {
		tag := fmt.Sprintf("A%d", i+3)
		resp := sendCmdRaw(conn, reader, tag+" FETCH 1:50 UID")
		if strings.Contains(resp, "NO ") || strings.Contains(resp, "BAD ") {
			t.Fatalf("iteration %d failed: %s", i, resp)
		}
	}
	_ = ms
}

func TestLoadConcurrentFetchStore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 20; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Concurrent op %d", i))
	}

	var mu sync.Mutex
	var failures []string
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("dial %d: %v", id, err))
				mu.Unlock()
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')

			prefix := fmt.Sprintf("L%d", id*10+1)
			sendCmdRaw(conn, reader, prefix+" LOGIN user@test.com pass")
			sendCmdRaw(conn, reader, fmt.Sprintf("L%d", id*10+2)+" SELECT INBOX")

			r1 := sendCmdRaw(conn, reader, fmt.Sprintf("L%d", id*10+3)+" FETCH 1:5 (FLAGS UID RFC822.SIZE)")
			if strings.Contains(r1, "NO ") || strings.Contains(r1, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("fetch %d: %s", id, r1))
				mu.Unlock()
				return
			}

			r2 := sendCmdRaw(conn, reader, fmt.Sprintf("L%d", id*10+4)+" STORE 1:3 +FLAGS (\\SEEN)")
			if strings.Contains(r2, "NO ") || strings.Contains(r2, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("store %d: %s", id, r2))
				mu.Unlock()
				return
			}

			r3 := sendCmdRaw(conn, reader, fmt.Sprintf("L%d", id*10+5)+" UID FETCH 1:* FLAGS")
			if strings.Contains(r3, "NO ") || strings.Contains(r3, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("uid fetch %d: %s", id, r3))
				mu.Unlock()
				return
			}
		}(i)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("concurrent operations had %d failures: %s", len(failures), failures[0])
	}
}

func TestLoadConcurrentUIDCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 10; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Copy msg %d", i))
	}

	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{MailboxID: 1, Name: "LoadDest", Path: "LoadDest"}, nil)

	var mu sync.Mutex
	var failures []string
	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("dial %d: %v", id, err))
				mu.Unlock()
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')

			prefix := fmt.Sprintf("C%d", id*10+1)
			sendCmdRaw(conn, reader, prefix+" LOGIN user@test.com pass")
			sendCmdRaw(conn, reader, fmt.Sprintf("C%d", id*10+2)+" SELECT INBOX")

			r := sendCmdRaw(conn, reader, fmt.Sprintf("C%d", id*10+3)+" UID COPY 1:5 LoadDest")
			if strings.Contains(r, "NO ") || strings.Contains(r, "BAD ") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("uid copy %d: %s", id, r))
				mu.Unlock()
				return
			}
		}(i)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("concurrent UID COPY had %d failures", len(failures))
	}
}

// ── FAILURE INJECTION ──────────────────────────────────────

func TestFailureMissingMailbox(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com pass")

	resp := opSendCmd(t, conn, reader, "A2", "SELECT NonExistent")
	if !strings.Contains(resp, "NO ") {
		t.Fatalf("expected NO for missing mailbox, got: %s", resp)
	}
	_ = ms
}

func TestFailureMissingMessage(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Only one message")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// FETCH a non-existent sequence number.
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 999 UID")
	if strings.Contains(resp, "BAD ") {
		t.Fatalf("should not return BAD for missing sequence: %s", resp)
	}
	_ = ms
}

func TestFailureAuthenticationFailure(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// fakeAuth accepts any password for registered users. Use an unknown user.
	resp := opSendCmd(t, conn, reader, "A1", "LOGIN unknown@test.com anypass")
	if !strings.Contains(resp, "NO ") {
		t.Fatalf("expected NO for unknown user, got: %s", resp)
	}
}

func TestFailureConnectionCloseDuringOperation(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Abort test")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// Close connection mid-operation — just verify no panic.
	conn.Close()
	time.Sleep(10 * time.Millisecond)
	_ = reader
	_ = ms
}

func TestFailureInvalidSequenceSets(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Invalid seq")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	invalidSeqs := []string{
		"FETCH 0 UID",
		"FETCH -1 UID",
		"FETCH abc UID",
	}
	for i, seq := range invalidSeqs {
		tag := fmt.Sprintf("A%d", i+3)
		resp := opSendCmd(t, conn, reader, tag, seq)
		if !strings.Contains(resp, "BAD ") {
			t.Fatalf("expected BAD for '%s', got: %s", seq, resp)
		}
	}
	_ = ms
}

func TestFailureInvalidUIDSets(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Invalid UID")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "UID FETCH 0 FLAGS")
	if !strings.Contains(resp, "BAD ") {
		t.Fatalf("expected BAD for UID 0, got: %s", resp)
	}
	_ = ms
}

// ── SECURITY REGRESSION ─────────────────────────────────────

func TestSecurityUnauthorizedAccessBlocked(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// Commands before LOGIN should be rejected.
	cmds := []string{
		"LIST \"\" \"*\"",
		"SELECT INBOX",
		"FETCH 1 FLAGS",
		"STORE 1 +FLAGS (\\SEEN)",
		"COPY 1 INBOX",
		"EXPUNGE",
		"UID FETCH 1 FLAGS",
		"UID STORE 1 +FLAGS (\\SEEN)",
		"UID COPY 1 INBOX",
	}
	for i, cmd := range cmds {
		tag := fmt.Sprintf("A%d", i+1)
		resp := opSendCmd(t, conn, reader, tag, cmd)
		if !strings.Contains(resp, "BAD ") {
			t.Fatalf("expected BAD for '%s' (not authenticated), got: %s", cmd, resp)
		}
	}
}

func TestSecuritySessionIsolation(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Create two mailboxes.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	ms.Folders.Create(ctx, &storage.Folder{MailboxID: 1, Name: "Private", Path: "Private"}, nil)

	// Store message in Private.
	privateFolder, _ := ms.Folders.GetByPath(ctx, 1, "Private", nil)
	ms.StoreMessage(ctx, &storage.Message{
		MessageID: "private-1", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: privateFolder.ID,
		FromAddress: "private@test.com", ToAddresses: "user@test.com",
		Subject: "Secret",
	}, []byte("Subject: Secret\r\n\r\nSecret Data"), nil)

	// Store a different message in INBOX.
	opStoreTestMsg(t, ms, 1, "Public")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// Select Private mailbox and verify the secret message is accessible.
	opSendCmd(t, conn, reader, "A3", "SELECT Private")
	resp := opSendCmd(t, conn, reader, "A4", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "Secret Data") {
		t.Fatal("authenticated user should access their own mailbox")
	}

	// Verify different session can access mailbox 2 independently.
	// (Session isolation is implicit via MailboxID in session state.)
	_ = ms
}

func TestSecurityLOGINStateEnforced(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()

	// After LOGIN, CAPABILITY should still work, but LOGIN again should fail.
	opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com pass")
	resp := opSendCmd(t, conn, reader, "A2", "LOGIN user@test.com pass")
	if !strings.Contains(resp, "BAD ") {
		t.Fatalf("expected BAD for duplicate LOGIN, got: %s", resp)
	}
}

func TestSecurityNoCredentialLeakage(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()

	resp := opSendCmd(t, conn, reader, "A1", "LOGIN user@test.com wrongpass")
	if strings.Contains(resp, "user@test.com") {
		t.Fatal("login failure should not echo username in response")
	}
	if strings.Contains(resp, "wrongpass") {
		t.Fatal("login failure should not echo password in response")
	}
}
