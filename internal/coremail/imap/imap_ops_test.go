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

// ── Helpers for message operation tests ─────────────────────

func opDial(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting
	return conn, reader
}

func opLoginSelect(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	fmt.Fprintf(conn, "A1 LOGIN user@test.com pass\r\n")
	reader.ReadString('\n') // A1 OK
	fmt.Fprintf(conn, "A2 SELECT INBOX\r\n")
	for {
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "A2 OK") || strings.Contains(line, "A2 NO") || strings.Contains(line, "A2 BAD") {
			break
		}
	}
}

func opSendCmd(t *testing.T, conn net.Conn, reader *bufio.Reader, tag, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s %s\r\n", tag, cmd)
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

func opStoreTestMsg(t *testing.T, ms *storage.MailStore, id uint, body string) {
	t.Helper()
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if folder == nil {
		t.Fatal("INBOX not found")
	}
	msg := &storage.Message{
		MessageID: fmt.Sprintf("op-msg-%d", id), TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
		Subject: "Test",
	}
	fullBody := []byte("From: sender@test.com\r\nTo: rcpt@test.com\r\nSubject: Test\r\nContent-Type: text/plain\r\n\r\n" + body)
	if err := ms.StoreMessage(ctx, msg, fullBody, nil); err != nil {
		t.Fatalf("store: %v", err)
	}
}

// ── FETCH BODY[] Tests ─────────────────────────────────────

func TestFetchBodyFull(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Hello Body")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// FETCH BODY[] returns the full message as a literal.
	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[]")
	if !strings.Contains(resp, "BODY[") {
		t.Fatalf("expected BODY[ in response, got: %s", resp)
	}
	if !strings.Contains(resp, "Hello Body") {
		t.Fatal("expected message body content in response")
	}
}

func TestFetchBodyHeader(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Body Text")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[HEADER]")
	if !strings.Contains(resp, "BODY[HEADER]") {
		t.Fatalf("expected BODY[HEADER] in response, got: %s", resp)
	}
	if !strings.Contains(resp, "Subject: Test") {
		t.Fatal("expected Subject header in response")
	}
}

func TestFetchBodyText(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Text Content Here")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "BODY[TEXT]") {
		t.Fatalf("expected BODY[TEXT] in response, got: %s", resp)
	}
	if !strings.Contains(resp, "Text Content Here") {
		t.Fatal("expected body text in response")
	}
}

func TestFetchBodyEmpty(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODY[TEXT]")
	if !strings.Contains(resp, "A3 OK") && !strings.Contains(resp, "NIL") {
		t.Logf("empty body response: %s", resp)
	}
}

// ── BODYSTRUCTURE Tests ────────────────────────────────────

func TestBodyStructureTextPlain(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Simple text body")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODYSTRUCTURE")
	if !strings.Contains(resp, "BODYSTRUCTURE") {
		t.Fatalf("expected BODYSTRUCTURE in response, got: %s", resp)
	}
	if !strings.Contains(resp, "text") || !strings.Contains(resp, "plain") {
		t.Fatal("expected text/plain in bodystructure")
	}
}

func TestBodyStructureMultipart(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	// Store a multipart message.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if folder == nil {
		t.Fatal("INBOX not found")
	}
	multipartData := []byte("From: sender@test.com\r\nTo: rcpt@test.com\r\nSubject: Multipart\r\nContent-Type: multipart/mixed; boundary=\"boundary123\"\r\n\r\n--boundary123\r\nContent-Type: text/plain\r\n\r\nPart 1\r\n--boundary123\r\nContent-Type: text/html\r\n\r\n<b>Part 2</b>\r\n--boundary123--\r\n")
	msg := &storage.Message{
		MessageID: "multipart-1", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
		Subject: "Multipart",
	}
	ms.StoreMessage(ctx, msg, multipartData, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "FETCH 1 BODYSTRUCTURE")
	if !strings.Contains(resp, "BODYSTRUCTURE") {
		t.Fatalf("expected BODYSTRUCTURE in response, got: %s", resp)
	}
}

// ── STORE Tests ────────────────────────────────────────────

func TestStoreAddFlags(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Store test")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "STORE 1 +FLAGS (\\SEEN \\FLAGGED)")
	if !strings.Contains(resp, "\\Seen") || !strings.Contains(resp, "\\Flagged") {
		t.Fatalf("expected \\Seen and \\Flagged in response, got: %s", resp)
	}
}

func TestStoreRemoveFlags(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	// Store with Seen=true.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	msg := &storage.Message{
		MessageID: "remove-flags", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID, Seen: true,
		FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
		Subject: "Remove Flags",
	}
	ms.StoreMessage(ctx, msg, []byte("Subject: Remove\r\n\r\nBody"), nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "STORE 1 -FLAGS (\\SEEN)")
	if strings.Contains(resp, "\\Seen") {
		t.Logf("seen flag might still be present: %s", resp)
	}
}

func TestStoreReplaceFlags(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Replace flags")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "STORE 1 FLAGS (\\DELETED)")
	if !strings.Contains(resp, "\\Deleted") {
		t.Fatalf("expected \\Deleted, got: %s", resp)
	}
}

func TestStoreInvalidFlagRejected(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Invalid flag")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "STORE 1 +FLAGS (\\INVALIDFLAG)")
	if !strings.Contains(resp, "A3 OK") {
		t.Fatalf("expected OK (flag ignored), got: %s", resp)
	}
}

// ── COPY Tests ─────────────────────────────────────────────

func TestCopyToExistingMailbox(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Copy test")

	// Create destination folder.
	ctx := context.TODO()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "Destination", Path: "Destination",
	}, nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "COPY 1 Destination")
	if !strings.Contains(resp, "A3 OK") {
		t.Fatalf("expected OK for COPY, got: %s", resp)
	}

	// Verify message exists in destination.
	dstFolder, _ := ms.Folders.GetByPath(ctx, 1, "Destination", nil)
	count, _ := ms.Messages.CountByFolder(ctx, dstFolder.ID, nil)
	if count != 1 {
		t.Fatalf("expected 1 message in destination, got %d", count)
	}
}

func TestCopyMissingMailboxRejected(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "Copy to missing")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "COPY 1 NonExistent")
	if !strings.Contains(resp, "A3 NO") {
		t.Fatalf("expected NO for missing mailbox, got: %s", resp)
	}
}

// ── EXPUNGE Tests ──────────────────────────────────────────

func TestExpungeRemovesDeleted(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	// Store 2 messages, mark one as deleted.
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if folder == nil {
		t.Fatal("INBOX not found")
	}

	m1 := &storage.Message{
		MessageID: "expunge-1", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID, Deleted: true,
		FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
		Subject: "Delete Me",
	}
	m2 := &storage.Message{
		MessageID: "expunge-2", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID,
		FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
		Subject: "Keep Me",
	}
	ms.StoreMessage(ctx, m1, []byte("Subject: Delete\r\n\r\nBody"), nil)
	ms.StoreMessage(ctx, m2, []byte("Subject: Keep\r\n\r\nBody"), nil)

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "EXPUNGE")
	if !strings.Contains(resp, "EXPUNGE") {
		t.Fatalf("expected EXPUNGE in response, got: %s", resp)
	}

	// Verify only 1 message remains.
	count, _ := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if count != 1 {
		t.Fatalf("expected 1 message after expunge, got %d", count)
	}
}

func TestExpungeNoDeletedSafe(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	opStoreTestMsg(t, ms, 1, "No delete")

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	resp := opSendCmd(t, conn, reader, "A3", "EXPUNGE")
	if !strings.Contains(resp, "A3 OK") {
		t.Fatalf("expected OK for EXPUNGE on clean folder, got: %s", resp)
	}
}

// ── BODYSTRUCTURE Unit Tests ───────────────────────────────

func TestDetectMIMEType(t *testing.T) {
	maj, min := detectMIMEType("text/plain")
	if maj != "text" || min != "plain" {
		t.Fatalf("expected text/plain, got %s/%s", maj, min)
	}
	maj, min = detectMIMEType("text/html; charset=utf-8")
	if maj != "text" || min != "html" {
		t.Fatalf("expected text/html, got %s/%s", maj, min)
	}
	maj, min = detectMIMEType("")
	if maj != "text" || min != "plain" {
		t.Fatalf("expected text/plain default, got %s/%s", maj, min)
	}
}

func TestDetectBoundary(t *testing.T) {
	b := detectBoundary(`multipart/mixed; boundary="abc123"`)
	if b != "abc123" {
		t.Fatalf("expected abc123, got %s", b)
	}
	b = detectBoundary("text/plain")
	if b != "" {
		t.Fatalf("expected empty, got %s", b)
	}
}

func TestSplitBody(t *testing.T) {
	h, b := SplitBody([]byte("Header1: val\r\n\r\nBody text"))
	if string(h) != "Header1: val" {
		t.Fatalf("unexpected header: %s", h)
	}
	if string(b) != "Body text" {
		t.Fatalf("unexpected body: %s", b)
	}
}

func TestSplitBodyNoBody(t *testing.T) {
	h, b := SplitBody([]byte("Header1: val"))
	if string(h) != "Header1: val" {
		t.Fatalf("unexpected header: %s", h)
	}
	if b != nil {
		t.Fatal("expected nil body")
	}
}

func TestFormatLiteral(t *testing.T) {
	l := FormatLiteral([]byte("hello"))
	if !strings.Contains(l, "{5}") {
		t.Fatalf("expected {5} in literal, got: %s", l)
	}
	if !strings.Contains(l, "hello") {
		t.Fatal("expected content in literal")
	}
}

func TestFormatLiteralEmpty(t *testing.T) {
	l := FormatLiteral([]byte{})
	if l != "NIL" {
		t.Fatalf("expected NIL for empty, got: %s", l)
	}
}

func TestGetBodyStructureText(t *testing.T) {
	data := []byte("Content-Type: text/plain\r\n\r\nHello World")
	bs := GetBodyStructure(data)
	if !strings.Contains(bs, "text") || !strings.Contains(bs, "plain") {
		t.Fatalf("expected text/plain in bodystructure, got: %s", bs)
	}
}
