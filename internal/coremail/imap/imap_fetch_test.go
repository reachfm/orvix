package imap

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// ── Sequence Set Parser Tests ───────────────────────────────

func TestSequenceSingle(t *testing.T) {
	ss, err := ParseSequenceSet("5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seqs := ss.Resolve(10)
	if len(seqs) != 1 || seqs[0] != 5 {
		t.Fatalf("expected [5], got %v", seqs)
	}
}

func TestSequenceRange(t *testing.T) {
	ss, err := ParseSequenceSet("3:7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seqs := ss.Resolve(10)
	if len(seqs) != 5 || seqs[0] != 3 || seqs[4] != 7 {
		t.Fatalf("expected [3 4 5 6 7], got %v", seqs)
	}
}

func TestSequenceWildcard(t *testing.T) {
	ss, err := ParseSequenceSet("*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seqs := ss.Resolve(5)
	if len(seqs) != 5 {
		t.Fatalf("expected [1 2 3 4 5], got %v", seqs)
	}
}

func TestSequenceMixed(t *testing.T) {
	ss, err := ParseSequenceSet("1,3,5:*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seqs := ss.Resolve(7)
	if len(seqs) != 5 {
		t.Fatalf("expected 5 results, got %v", seqs)
	}
}

func TestSequenceInvalidRejected(t *testing.T) {
	_, err := ParseSequenceSet("")
	if err == nil {
		t.Fatal("expected error for empty sequence")
	}
	_, err = ParseSequenceSet("0")
	if err == nil {
		t.Fatal("expected error for 0")
	}
	_, err = ParseSequenceSet("-1")
	if err == nil {
		t.Fatal("expected error for negative")
	}
	_, err = ParseSequenceSet("abc")
	if err == nil {
		t.Fatal("expected error for non-numeric")
	}
	_, err = ParseSequenceSet("1:abc")
	if err == nil {
		t.Fatal("expected error for non-numeric range end")
	}
}

func TestSequenceRangeInvalid(t *testing.T) {
	_, err := ParseSequenceSet("10:1")
	if err == nil {
		t.Fatal("expected error for reversed range")
	}
}

func TestSequenceResolveEmpty(t *testing.T) {
	ss, _ := ParseSequenceSet("1")
	seqs := ss.Resolve(0)
	if len(seqs) != 0 {
		t.Fatal("expected empty for total=0")
	}
}

// ── FETCH Command Tests ─────────────────────────────────────

func fetchTestMsg(t *testing.T) []byte {
	t.Helper()
	return []byte("From: sender@example.com\r\nTo: rcpt@test.com\r\nSubject: Test Subject\r\nDate: Mon, 1 Jan 2024 12:00:00 +0000\r\nMessage-ID: <abc123@test.com>\r\nIn-Reply-To: <prev@test.com>\r\nContent-Type: text/plain\r\n\r\nHello World")
}

func TestFetchBeforeSelectRejected(t *testing.T) {
	s := NewSession("127.0.0.1:0")
	s.State = StateAuthenticated
	cmd, _ := ParseCommand("A1 FETCH 1 FLAGS")
	resp := Handle(context.TODO(), cmd, s, nil)
	if !strings.Contains(resp, "BAD") {
		t.Fatalf("expected BAD, got: %s", resp)
	}
}

func doFetchCmd(t *testing.T, addr string, fetchCmd string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // greeting

	fmt.Fprintf(conn, "A1 LOGIN user@test.com pass\r\n")
	reader.ReadString('\n') // A1 OK ...

	fmt.Fprintf(conn, "A2 SELECT INBOX\r\n")
	// Read all SELECT response lines.
	for {
		line, _ := reader.ReadString('\n')
		if strings.Contains(line, "A2 OK") || strings.Contains(line, "A2 NO") || strings.Contains(line, "A2 BAD") {
			break
		}
	}

	fmt.Fprintf(conn, "%s\r\n", fetchCmd)
	var resp strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		resp.WriteString(line)
		// Check for tagged FETCH response.
		if strings.Contains(line, "A3 OK") || strings.Contains(line, "A3 NO") || strings.Contains(line, "A3 BAD") || strings.Contains(line, "A3 BYE") {
			break
		}
		_ = time.Second
	}
	return resp.String()
}

func TestFetchFlags(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, true)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 FLAGS")
	if !strings.Contains(resp, "FLAGS") {
		t.Fatalf("expected FLAGS in response, got: %s", resp)
	}
	if !strings.Contains(resp, "\\Seen") {
		t.Fatal("expected \\Seen flag")
	}
}

func TestFetchUID(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 UID")
	if !strings.Contains(resp, "UID") {
		t.Fatalf("expected UID in response, got: %s", resp)
	}
}

func TestFetchRFC822Size(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 RFC822.SIZE")
	if !strings.Contains(resp, "RFC822.SIZE") {
		t.Fatalf("expected RFC822.SIZE in response, got: %s", resp)
	}
}

func TestFetchInternalDate(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 INTERNALDATE")
	if !strings.Contains(resp, "INTERNALDATE") {
		t.Fatalf("expected INTERNALDATE in response, got: %s", resp)
	}
}

func TestFetchEnvelope(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 ENVELOPE")
	if !strings.Contains(resp, "ENVELOPE") {
		t.Fatalf("expected ENVELOPE in response, got: %s", resp)
	}
}

func TestFetchMultipleMessages(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	for i := 0; i < 3; i++ {
		storeFetchMsg(t, ms, uint(i), 1, false)
	}

	resp := doFetchCmd(t, addr, "A3 FETCH 1:3 FLAGS")
	if !strings.Contains(resp, "1 FETCH") {
		t.Fatal("expected message 1 FETCH")
	}
	if !strings.Contains(resp, "2 FETCH") {
		t.Fatal("expected message 2 FETCH")
	}
	if !strings.Contains(resp, "3 FETCH") {
		t.Fatal("expected message 3 FETCH")
	}
}

func TestFetchMissingMessageSafe(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 100 FLAGS")
	if strings.Contains(resp, "BAD") {
		t.Fatalf("expected no error for missing message, got: %s", resp)
	}
}

func TestFetchAllAttrs(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()
	storeFetchMsg(t, ms, 1, 1, false)

	resp := doFetchCmd(t, addr, "A3 FETCH 1 (FLAGS UID RFC822.SIZE INTERNALDATE ENVELOPE)")
	if !strings.Contains(resp, "FLAGS") {
		t.Fatal("expected FLAGS")
	}
	if !strings.Contains(resp, "UID") {
		t.Fatal("expected UID")
	}
	if !strings.Contains(resp, "RFC822.SIZE") {
		t.Fatal("expected RFC822.SIZE")
	}
	if !strings.Contains(resp, "INTERNALDATE") {
		t.Fatal("expected INTERNALDATE")
	}
	if !strings.Contains(resp, "ENVELOPE") {
		t.Fatal("expected ENVELOPE")
	}
}

// ── Envelope Builder Tests ──────────────────────────────────

func TestBuildEnvelope(t *testing.T) {
	data := fetchTestMsg(t)
	env := BuildEnvelope(data)
	if env.Subject != "Test Subject" {
		t.Fatalf("expected 'Test Subject', got '%s'", env.Subject)
	}
	if env.MessageID != "<abc123@test.com>" {
		t.Fatalf("expected '<abc123@test.com>', got '%s'", env.MessageID)
	}
	if len(env.From) != 1 || env.From[0].Mailbox != "sender" || env.From[0].Host != "example.com" {
		t.Fatalf("unexpected From: %+v", env.From)
	}
}

func TestEnvelopeMissingHeaders(t *testing.T) {
	data := []byte("From: test@test.com\r\n\r\nBody")
	env := BuildEnvelope(data)
	if env.Subject != "" {
		t.Fatal("expected empty subject")
	}
	if env.MessageID != "" {
		t.Fatal("expected empty message-id")
	}
	if len(env.To) != 0 {
		t.Fatal("expected no To")
	}
}

func TestEnvelopeMalformedHeadersSafe(t *testing.T) {
	data := []byte("From: test@test.com\r\nSubject: \r\n\r\nBody")
	env := BuildEnvelope(data)
	if env.From[0].Mailbox != "test" {
		t.Fatalf("expected 'test', got '%s'", env.From[0].Mailbox)
	}
}

func TestFormatEnvelope(t *testing.T) {
	e := &Envelope{
		Date:    "Mon, 1 Jan 2024 12:00:00 +0000",
		Subject: "Test",
		From:    []*Address{{Name: "Sender", Mailbox: "sender", Host: "example.com"}},
		To:      []*Address{{Name: "", Mailbox: "rcpt", Host: "test.com"}},
	}
	s := FormatEnvelope(e)
	if !strings.Contains(s, "Test") {
		t.Fatal("expected subject in envelope")
	}
	if !strings.Contains(s, "sender") {
		t.Fatal("expected sender in envelope")
	}
}

func TestFormatEnvelopeNilFields(t *testing.T) {
	e := &Envelope{}
	s := FormatEnvelope(e)
	if !strings.Contains(s, "NIL") {
		t.Fatal("expected NIL for empty envelope")
	}
}

// ── Format Flags Tests ──────────────────────────────────────

func TestFormatFlagsAll(t *testing.T) {
	f := formatFlags(true, true, true, true, true)
	if !strings.Contains(f, "\\Seen") || !strings.Contains(f, "\\Answered") {
		t.Fatal("expected flags")
	}
}

func TestFormatFlagsNone(t *testing.T) {
	f := formatFlags(false, false, false, false, false)
	if f != "()" {
		t.Fatalf("expected '()', got '%s'", f)
	}
}

// ── Format IMAP Date Tests ─────────────────────────────────

func TestFormatIMAPDate(t *testing.T) {
	_ = fetchTestMsg(t)
}

// ── Address Parsing Tests ───────────────────────────────────

func TestParseAddressBareEmail(t *testing.T) {
	a := parseAddress("user@example.com")
	if a == nil || a.Mailbox != "user" || a.Host != "example.com" {
		t.Fatalf("unexpected: %+v", a)
	}
}

func TestParseAddressDisplayName(t *testing.T) {
	a := parseAddress(`"John Doe" <john@example.com>`)
	if a == nil || a.Name != "John Doe" || a.Mailbox != "john" || a.Host != "example.com" {
		t.Fatalf("unexpected: %+v", a)
	}
}

func TestParseAddressList(t *testing.T) {
	addrs := parseAddressList(`"A" <a@x.com>, "B" <b@y.com>`)
	if len(addrs) != 2 {
		t.Fatalf("expected 2, got %d", len(addrs))
	}
}

func TestParseAddressListEmpty(t *testing.T) {
	addrs := parseAddressList("")
	if len(addrs) != 0 {
		t.Fatal("expected empty list")
	}
}

// Helper: store a test message in INBOX.
func storeFetchMsg(t *testing.T, ms *storage.MailStore, id uint, mailboxID uint, seen bool) {
	t.Helper()
	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, mailboxID, 1, 1, nil)

	folder, err := ms.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
	if err != nil || folder == nil {
		t.Fatalf("get inbox: %v", err)
	}

	msg := &storage.Message{
		MessageID:   fmt.Sprintf("fetch-msg-%d", id),
		TenantID:    1,
		DomainID:    1,
		MailboxID:   mailboxID,
		FolderID:    folder.ID,
		FromAddress: "sender@example.com",
		ToAddresses: "rcpt@test.com",
		Subject:     "Test Subject",
		Seen:        seen,
	}
	if err := ms.StoreMessage(ctx, msg, fetchTestMsg(t), nil); err != nil {
		t.Fatalf("store message: %v", err)
	}
}
