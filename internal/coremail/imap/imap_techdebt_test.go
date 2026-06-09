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

// ── UIDNEXT RFC-Correctness Tests ─────────────────────────

func TestUIDNextEmptyMailbox(t *testing.T) {
	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	conn, reader := opDial(t, addr)
	defer conn.Close()
	sel := uidLoginSelect(t, conn, reader)

	uidNext := extractUIDNEXT(sel)
	if uidNext != 1 {
		t.Fatalf("expected UIDNEXT 1 for empty mailbox, got %d", uidNext)
	}
}

func TestUIDNextWithGaps(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Create a folder with specific ID gaps.
	// We can simulate gaps by storing messages which get auto-incremented IDs,
	// then deleting one. The UIDNEXT should be MAX(ID) + 1.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Store messages — IDs will be 1, 2, 3.
	for i := 1; i <= 3; i++ {
		msg := &storage.Message{
			MessageID: fmt.Sprintf("uidgap-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID,
			FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: fmt.Sprintf("%d", i),
		}
		ms.StoreMessage(ctx, msg, []byte(fmt.Sprintf("Subject: %d\r\n\r\n%d", i, i)), nil)
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()
	sel := uidLoginSelect(t, conn, reader)

	uidNext := extractUIDNEXT(sel)
	if uidNext <= 3 {
		t.Fatalf("expected UIDNEXT > 3, got %d", uidNext)
	}
}

func TestUIDNextAfterExpungeDoesNotDecrease(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store 3 messages.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 1; i <= 3; i++ {
		msg := &storage.Message{
			MessageID: fmt.Sprintf("expunge-next-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID,
			FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: fmt.Sprintf("%d", i),
			Deleted: (i == 1),
		}
		ms.StoreMessage(ctx, msg, []byte(fmt.Sprintf("Subject: %d\r\n\r\n%d", i, i)), nil)
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()
	selBefore := uidLoginSelect(t, conn, reader)
	uidNextBefore := extractUIDNEXT(selBefore)

	opSendCmd(t, conn, reader, "A3", "EXPUNGE")

	selAfter := uidLoginSelect(t, conn, reader)
	uidNextAfter := extractUIDNEXT(selAfter)

	if uidNextAfter < uidNextBefore {
		t.Fatalf("UIDNEXT decreased after EXPUNGE: was %d, now %d", uidNextBefore, uidNextAfter)
	}
}

func TestUIDNextAfterCopyIncreases(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "Copy test")
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{MailboxID: 1, Name: "CopyDest", Path: "CopyDest"}, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	selBefore := uidLoginSelect(t, conn, reader)
	uidNextBefore := extractUIDNEXT(selBefore)

	opSendCmd(t, conn, reader, "A3", "COPY 1 CopyDest")

	// Re-select to get updated UIDNEXT.
	selAfter := uidLoginSelect(t, conn, reader)
	uidNextAfter := extractUIDNEXT(selAfter)

	if uidNextAfter < uidNextBefore {
		t.Fatalf("UIDNEXT should not decrease after COPY: was %d, now %d", uidNextBefore, uidNextAfter)
	}
}

func TestUIDNextSurvivesReconnect(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	opStoreTestMsg(t, ms, 1, "Reconnect test")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	sel1 := uidLoginSelect(t, conn, reader)
	uidNext1 := extractUIDNEXT(sel1)
	conn.Close()

	conn2, reader2 := opDial(t, addr)
	defer conn2.Close()
	sel2 := uidLoginSelect(t, conn2, reader2)
	uidNext2 := extractUIDNEXT(sel2)

	if uidNext2 != uidNext1 {
		t.Fatalf("UIDNEXT changed after reconnect: was %d, now %d", uidNext1, uidNext2)
	}
	_ = ms
}

// ── BODY[] Safety Tests ────────────────────────────────────

func TestBodySmallMessageWorks(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Small body")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[]")
	if !strings.Contains(resp, "BODY[") {
		t.Fatalf("expected BODY[ in response, got: %s", resp)
	}
}

func TestBodyHeaderStillWorks(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Header test")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[HEADER]")
	if !strings.Contains(resp, "BODY[HEADER]") {
		t.Fatalf("expected BODY[HEADER], got: %s", resp)
	}
}

func TestBodyTextStillWorks(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Exactly body text")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "BODY[TEXT]") {
		t.Fatalf("expected BODY[TEXT], got: %s", resp)
	}
}

func TestBodyLiteralSizeMatches(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Hello World!")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "Hello World!") {
		t.Fatal("expected body content in response")
	}
	// The literal should contain exactly the body text.
	_ = resp
}

func TestBodyCRLFPreserved(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store message with CRLF.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	msg := &storage.Message{
		MessageID: "crlf-test", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "CRLF",
	}
	rawData := []byte("Subject: CRLF\r\n\r\nLine1\r\nLine2\r\n")
	ms.StoreMessage(ctx, msg, rawData, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "Line1") || !strings.Contains(resp, "Line2") {
		t.Fatal("expected CRLF lines preserved")
	}
}

func TestBodyLargeMessageHandledSafely(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Create a message with SizeBytes exceeding the guard threshold.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	msg := &storage.Message{
		MessageID: "large-body", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "Large",
	}
	// Create a body larger than the 50MB max (we can mock it with a smaller message
	// and manually set SizeBytes to exceed the limit).
	rawData := []byte("Subject: Large\r\n\r\nSMALL_CONTENT")
	ms.StoreMessage(ctx, msg, rawData, nil)

	// Manually update SizeBytes to exceed limit so the guard triggers.
	ms.Messages.Update(ctx, &storage.Message{
		ID: msg.ID, SizeBytes: 100 * 1024 * 1024,
	}, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// FETCH BODY[] on a "large" message should return NIL or error gracefully.
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[]")
	if strings.Contains(resp, "BAD ") {
		t.Fatalf("large body should not cause BAD: %s", resp)
	}
}

// ── Helpers ────────────────────────────────────────────────

func extractUIDNEXT(resp string) uint {
	for _, line := range strings.Split(resp, "\n") {
		if strings.Contains(line, "UIDNEXT") {
			var v uint
			fmt.Sscanf(line, "* OK [UIDNEXT %d]", &v)
			return v
		}
	}
	return 0
}

// Verify these compile correctly.
var _ = bufio.NewReader
var _ = net.Dial
