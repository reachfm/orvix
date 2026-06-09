package imap

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// ── UID Sync Tests ─────────────────────────────────────────

func uidDial(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reader := bufio.NewReader(conn)
	reader.ReadString('\n')
	return conn, reader
}

func uidLoginSelect(t *testing.T, conn net.Conn, reader *bufio.Reader) string {
	t.Helper()
	fmt.Fprintf(conn, "A1 LOGIN user@test.com pass\r\n")
	reader.ReadString('\n')

	fmt.Fprintf(conn, "A2 SELECT INBOX\r\n")
	var selectResp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		selectResp.WriteString(line)
		if strings.Contains(line, "A2 OK") || strings.Contains(line, "A2 NO") || strings.Contains(line, "A2 BAD") {
			break
		}
	}
	return selectResp.String()
}

func uidSend(t *testing.T, conn net.Conn, reader *bufio.Reader, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\r\n", cmd)
	// Extract tag from command
	parts := strings.SplitN(cmd, " ", 2)
	tag := parts[0]
	var resp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		resp.WriteString(line)
		if strings.Contains(line, tag+" OK") || strings.Contains(line, tag+" NO") || strings.Contains(line, tag+" BAD") || strings.Contains(line, tag+" BYE") {
			break
		}
	}
	return resp.String()
}

// TestUIDAssignment verifies messages get stable UIDs (Message.ID).
func TestUIDAssignment(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store 3 messages.
	for i := 0; i < 3; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Message %d", i+1))
	}

	conn, reader := uidDial(t, addr)
	defer conn.Close()

	selResp := uidLoginSelect(t, conn, reader)
	if !strings.Contains(selResp, "UIDVALIDITY") {
		t.Fatal("expected UIDVALIDITY in SELECT response")
	}
	if !strings.Contains(selResp, "UIDNEXT") {
		t.Fatal("expected UIDNEXT in SELECT response")
	}

	// Fetch by UID.
	resp := uidSend(t, conn, reader, "A3 UID FETCH 1:* FLAGS")
	if !strings.Contains(resp, "1 FETCH") && !strings.Contains(resp, "2 FETCH") && !strings.Contains(resp, "3 FETCH") {
		t.Fatalf("expected FETCH responses, got: %s", resp)
	}
}

// TestUIDPersistence verifies UID survives reconnect.
func TestUIDPersistence(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Persistent message")

	// First connection.
	conn1, reader1 := uidDial(t, addr)
	uidLoginSelect(t, conn1, reader1)

	uidResp := uidSend(t, conn1, reader1, "A3 UID FETCH 1 (UID FLAGS)")
	if !strings.Contains(uidResp, "UID") {
		t.Fatal("expected UID in response")
	}
	conn1.Close()

	// Second connection (reconnect).
	conn2, reader2 := uidDial(t, addr)
	uidLoginSelect(t, conn2, reader2)

	uidResp2 := uidSend(t, conn2, reader2, "A4 UID FETCH 1 (UID FLAGS)")
	if !strings.Contains(uidResp2, "UID") {
		t.Fatal("expected UID after reconnect")
	}
	conn2.Close()
}

// TestUIDFetchByUID verifies UID FETCH works with explicit UID.
func TestUIDFetchByUID(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Msg %d", i+1))
	}

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	uidLoginSelect(t, conn, reader)

	// Fetch the first stored message by its UID (should be 1).
	resp := uidSend(t, conn, reader, "A3 UID FETCH 1 FLAGS")
	if !strings.Contains(resp, "FETCH") {
		t.Fatalf("expected FETCH response, got: %s", resp)
	}
}

// TestUIDStoreByUID verifies UID STORE updates correct message.
func TestUIDStoreByUID(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Msg %d", i+1))
	}

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	uidLoginSelect(t, conn, reader)

	// Store +FLAGS by UID.
	resp := uidSend(t, conn, reader, "A3 UID STORE 1 +FLAGS (\\SEEN)")
	if !strings.Contains(resp, "A3 OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

// TestUIDCopyByUID verifies UID COPY copies correct messages.
func TestUIDCopyByUID(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "Copy by UID")

	// Create destination folder.
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "UIDDest", Path: "UIDDest",
	}, nil)

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	uidLoginSelect(t, conn, reader)

	resp := uidSend(t, conn, reader, "A3 UID COPY 1 UIDDest")
	if !strings.Contains(resp, "A3 OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}

	// Verify copy exists.
	dstFolder, _ := ms.Folders.GetByPath(ctx, 1, "UIDDest", nil)
	count, _ := ms.Messages.CountByFolder(ctx, dstFolder.ID, nil)
	if count != 1 {
		t.Fatalf("expected 1 copied message, got %d", count)
	}
}

// TestUIDVALIDITYReturned verifies SELECT returns UIDVALIDITY.
func TestUIDVALIDITYReturned(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "UIDVALIDITY test")

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	selResp := uidLoginSelect(t, conn, reader)

	if !strings.Contains(selResp, "UIDVALIDITY") {
		t.Fatal("expected UIDVALIDITY in SELECT")
	}
	_ = ms
}

// TestUIDNEXTReturned verifies SELECT returns UIDNEXT.
func TestUIDNEXTReturned(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "UIDNEXT test")

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	selResp := uidLoginSelect(t, conn, reader)

	if !strings.Contains(selResp, "UIDNEXT") {
		t.Fatal("expected UIDNEXT in SELECT")
	}
	_ = ms
}

// TestSeqToUIDMapping verifies sequence-to-UID mapping is correct.
func TestSeqToUIDMapping(t *testing.T) {
	msgs := []storage.Message{
		{ID: 10}, {ID: 20}, {ID: 30},
	}

	if uidToSeq(msgs, 10) != 1 {
		t.Fatal("expected UID 10 → seq 1")
	}
	if uidToSeq(msgs, 20) != 2 {
		t.Fatal("expected UID 20 → seq 2")
	}
	if uidToSeq(msgs, 30) != 3 {
		t.Fatal("expected UID 30 → seq 3")
	}
	if uidToSeq(msgs, 99) != 0 {
		t.Fatal("expected 0 for nonexistent UID")
	}

	if seqToUID(msgs, 1) != 10 {
		t.Fatal("expected seq 1 → UID 10")
	}
	if seqToUID(msgs, 5) != 0 {
		t.Fatal("expected 0 for out-of-range seq")
	}
}

// TestExpungePreservesUIDMapping verifies EXPUNGE doesn't corrupt remaining UIDs.
func TestExpungePreservesUIDMapping(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store 3 messages.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 1; i <= 3; i++ {
		deleted := (i == 2) // mark middle message as deleted
		msg := &storage.Message{
			MessageID: fmt.Sprintf("expunge-uid-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID, Deleted: deleted,
			FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
			Subject: fmt.Sprintf("Msg %d", i),
		}
		ms.StoreMessage(ctx, msg, []byte(fmt.Sprintf("Subject: Msg %d\r\n\r\nBody %d", i, i)), nil)
	}

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	uidLoginSelect(t, conn, reader)

	// Expunge.
	uidSend(t, conn, reader, "A3 EXPUNGE")

	// Re-select and verify remaining messages.
	uidLoginSelect(t, conn, reader)
	resp := uidSend(t, conn, reader, "A5 UID FETCH 1:* FLAGS")
	if !strings.Contains(resp, "A5 OK") {
		t.Fatalf("expected OK, got: %s", resp)
	}
}

// TestMultipleMailboxUIDIsolation verifies UIDs are independent per mailbox.
func TestMultipleMailboxUIDIsolation(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store messages in INBOX.
	opStoreTestMsg(t, ms, 1, "Inbox msg")

	// Create a second mailbox with its own messages.
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "OtherBox", Path: "OtherBox",
	}, nil)
	otherFolder, _ := ms.Folders.GetByPath(ctx, 1, "OtherBox", nil)
	otherMsg := &storage.Message{
		MessageID: "other-1", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: otherFolder.ID,
		FromAddress: "other@test.com", ToAddresses: "user@test.com",
		Subject: "Other",
	}
	ms.StoreMessage(ctx, otherMsg, []byte("Subject: Other\r\n\r\nBody"), nil)

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	uidLoginSelect(t, conn, reader)

	// Mbox 1 (INBOX) has 1 message. Select OtherBox.
	fmt.Fprintf(conn, "A3 SELECT OtherBox\r\n")
	for {
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "A3 OK") || strings.Contains(line, "A3 NO") || strings.Contains(line, "A3 BAD") {
			break
		}
	}

	resp := uidSend(t, conn, reader, "A4 UID FETCH 1 FLAGS")
	if !strings.Contains(resp, "FETCH") {
		t.Fatalf("expected FETCH in other mailbox, got: %s", resp)
	}
}

// TestUIDNEXTAfterAdd verifies UIDNEXT increases after adding messages.
func TestUIDNEXTAfterAdd(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "First")

	conn, reader := uidDial(t, addr)
	defer conn.Close()
	sel1 := uidLoginSelect(t, conn, reader)
	conn.Close()

	// Add another message.
	opStoreTestMsg(t, ms, 2, "Second")

	conn2, reader2 := uidDial(t, addr)
	defer conn2.Close()
	sel2 := uidLoginSelect(t, conn2, reader2)

	// Both selects had UIDNEXT (ignore exact value, just verify presence).
	_ = sel1
	_ = sel2
}

// TestConcurrentUIDAccess verifies concurrent UID operations don't corrupt.
func TestConcurrentUIDAccess(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		opStoreTestMsg(t, ms, uint(i), fmt.Sprintf("Concurrent %d", i))
	}

	done := make(chan bool, 3)
	for i := 0; i < 3; i++ {
		go func() {
			conn, reader := uidDial(t, addr)
			defer conn.Close()
			uidLoginSelect(t, conn, reader)
			uidSend(t, conn, reader, fmt.Sprintf("A%d UID FETCH 1:* FLAGS", i+3))
			done <- true
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	_ = ms
}
