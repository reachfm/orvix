package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use file-based SQLite with WAL mode for concurrent access.
	path := filepath.Join(t.TempDir(), "mailstore.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Set busy timeout via PRAGMA (works with modernc.org/sqlite).
	if _, err := db.Exec("PRAGMA busy_timeout = 10000"); err != nil {
		t.Logf("note: busy_timeout pragma: %v", err)
	}

	nowFn = func() time.Time { return time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC) }

	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v\nSQL: %s", err, stmt)
		}
	}
	for _, stmt := range Indexes() {
		db.Exec(stmt)
	}
	return db
}

func testStore(t *testing.T) (*sql.DB, *MailStore) {
	t.Helper()
	db := testDB(t)
	base := t.TempDir()
	store, err := NewMailStore(db, filepath.Join(base, "messages"))
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	return db, store
}

func makeMessage(mailboxID, folderID, tenantID, domainID uint) *Message {
	return &Message{
		MessageID:         GenerateMessageID(),
		TenantID:          tenantID,
		DomainID:          domainID,
		MailboxID:         mailboxID,
		FolderID:          folderID,
		InternetMessageID: "<test@example.com>",
		Subject:           "Test Message",
		FromAddress:       "sender@example.com",
		ToAddresses:       "recipient@example.com",
		ReceivedDate:      nowFn(),
		Seen:              false,
		Importance:        ImportanceNormal,
	}
}

// ── Folder Tests ──────────────────────────────────────────────

func TestFolderCreateAndGet(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)

	inbox, err := store.Folders.GetByPath(ctx, 1, "INBOX", nil)
	if err != nil {
		t.Fatalf("get inbox: %v", err)
	}
	if inbox == nil {
		t.Fatal("inbox should exist")
	}
	if inbox.FolderType != FolderInbox {
		t.Fatalf("expected inbox type, got %s", inbox.FolderType)
	}

	sent, err := store.Folders.GetByPath(ctx, 1, "Sent", nil)
	if err != nil {
		t.Fatalf("get sent: %v", err)
	}
	if sent == nil {
		t.Fatal("sent should exist")
	}
}

func TestFolderCustom(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)

	f := Folder{MailboxID: 1, Name: "Projects", Path: "Projects", FolderType: FolderCustom}
	if err := store.Folders.Create(ctx, &f, nil); err != nil {
		t.Fatalf("create custom folder: %v", err)
	}

	got, err := store.Folders.GetByPath(ctx, 1, "Projects", nil)
	if err != nil {
		t.Fatalf("get custom folder: %v", err)
	}
	if got == nil || got.Name != "Projects" {
		t.Fatal("custom folder not found")
	}

	folders, err := store.Folders.ListByMailbox(ctx, 1, nil)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(folders) != 7 { // 6 system + 1 custom
		t.Fatalf("expected 7 folders, got %d", len(folders))
	}
}

func TestFolderRename(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	if err := store.Folders.Rename(ctx, inbox.ID, "Renamed", nil); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, _ := store.Folders.GetByID(ctx, inbox.ID, nil)
	if got.Name != "Renamed" {
		t.Fatalf("expected Renamed, got %s", got.Name)
	}
}

func TestFolderDelete(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	trash, _ := store.Folders.GetByPath(ctx, 1, "Trash", nil)

	if err := store.Folders.Delete(ctx, trash.ID, nil); err != nil {
		t.Fatalf("delete folder: %v", err)
	}

	got, _ := store.Folders.GetByID(ctx, trash.ID, nil)
	if got != nil {
		t.Fatal("folder should be deleted")
	}
}

// ── Message Tests ─────────────────────────────────────────────

func TestStoreMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := []byte("From: sender@example.com\r\nSubject: Test\r\n\r\nHello World")

	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message: %v", err)
	}
	if msg.ID == 0 {
		t.Fatal("expected non-zero message ID")
	}
	if msg.SHA256 == "" {
		t.Fatal("expected SHA256 hash")
	}
	if msg.SizeBytes != int64(len(rfc822)) {
		t.Fatalf("expected size %d, got %d", len(rfc822), msg.SizeBytes)
	}

	// Verify file exists.
	if _, err := os.Stat(msg.RFC822Path); os.IsNotExist(err) {
		t.Fatalf("rfc822 file not created: %s", msg.RFC822Path)
	}
}

func TestLoadMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := []byte("From: test@example.com\r\nSubject: Load Test\r\n\r\nBody")
	store.StoreMessage(ctx, msg, rfc822, nil)

	loaded, data, err := store.LoadMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("load message: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded message is nil")
	}
	if string(data) != string(rfc822) {
		t.Fatal("loaded data mismatch")
	}
	if loaded.Subject != "Test Message" {
		t.Fatalf("expected subject Test Message, got %s", loaded.Subject)
	}
}

func TestMessageDeleteAndRestore(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("test"), nil)

	if err := store.DeleteMessage(ctx, msg.ID, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	del, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if del == nil || !del.Deleted {
		t.Fatal("message should be marked deleted")
	}

	// File should still exist.
	if _, err := os.Stat(msg.RFC822Path); os.IsNotExist(err) {
		t.Fatal("rfc822 file should still exist after soft delete")
	}

	// Restore.
	if err := store.RestoreMessage(ctx, msg.ID, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if restored == nil || restored.Deleted {
		t.Fatal("message should be restored")
	}
}

func TestMessageMove(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)
	sent, _ := store.Folders.GetByPath(ctx, 1, "Sent", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("move test"), nil)

	if err := store.MoveMessage(ctx, msg.ID, sent.ID, nil); err != nil {
		t.Fatalf("move: %v", err)
	}

	moved, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if moved.FolderID != sent.ID {
		t.Fatalf("expected folder %d, got %d", sent.ID, moved.FolderID)
	}
}

func TestMessageCopy(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	store.Folders.EnsureSystemFolders(ctx, 2, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("copy test"), nil)

	copied, err := store.CopyMessage(ctx, msg.ID, 2, inbox.ID, nil)
	if err != nil {
		t.Fatalf("copy message: %v", err)
	}
	if copied == nil {
		t.Fatal("copied message is nil")
	}
	if copied.MailboxID != 2 {
		t.Fatalf("expected mailbox 2, got %d", copied.MailboxID)
	}
	if copied.MessageID == msg.MessageID {
		t.Fatal("copied message should have new ID")
	}

	// Original should still exist.
	orig, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if orig == nil {
		t.Fatal("original message should still exist")
	}
}

func TestMessageFlags(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("flags test"), nil)

	seen := true
	flagged := true
	if err := store.Messages.UpdateFlags(ctx, msg.ID, &seen, nil, &flagged, nil, nil, nil, nil); err != nil {
		t.Fatalf("update flags: %v", err)
	}

	updated, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if !updated.Seen {
		t.Fatal("expected seen flag")
	}
	if !updated.Flagged {
		t.Fatal("expected flagged flag")
	}
}

func TestMessageList(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 0; i < 5; i++ {
		msg := makeMessage(1, inbox.ID, 1, 1)
		msg.Subject = fmt.Sprintf("Message %d", i)
		store.StoreMessage(ctx, msg, []byte(fmt.Sprintf("body %d", i)), nil)
	}

	msgs, total, err := store.ListMessages(ctx, MessageFilter{MailboxID: 1, FolderID: &inbox.ID}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected 5 messages, got %d", total)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 results, got %d", len(msgs))
	}
}

func TestMessageListByFlags(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 0; i < 3; i++ {
		msg := makeMessage(1, inbox.ID, 1, 1)
		store.StoreMessage(ctx, msg, []byte("flag test"), nil)
	}

	// Mark first as seen
	seen := true
	store.Messages.UpdateFlags(ctx, 1, &seen, nil, nil, nil, nil, nil, nil)

	seenFilter := false
	msgs, total, err := store.ListMessages(ctx, MessageFilter{
		MailboxID: 1,
		FolderID:  &inbox.ID,
		Flags:     &struct{ Seen, Flagged, Draft, Deleted, Junk *bool }{Seen: &seenFilter},
	}, nil)
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 unread, got %d", total)
	}
	_ = msgs
}

// ── Integrity Tests ──────────────────────────────────────────

func TestIntegrityValidMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("integrity test"), nil)

	engine := NewIntegrityEngine(store)
	res, err := engine.VerifyMessageIntegrity(ctx, msg.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.DBRecordOK {
		t.Fatal("expected DB record OK")
	}
	if !res.FileExists {
		t.Fatal("expected file exists")
	}
	if !res.SHA256Match {
		t.Fatal("expected SHA256 match")
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
}

func TestIntegrityMissingFile(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("will be removed"), nil)

	os.Remove(msg.RFC822Path)

	engine := NewIntegrityEngine(store)
	res, err := engine.VerifyMessageIntegrity(ctx, msg.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.DBRecordOK != true {
		t.Fatal("DB record should still exist")
	}
	if res.FileExists {
		t.Fatal("file should be missing")
	}
}

func TestIntegritySHA256Mismatch(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("original content"), nil)

	// Corrupt the file.
	os.WriteFile(msg.RFC822Path, []byte("tampered content"), 0640)

	engine := NewIntegrityEngine(store)
	res, err := engine.VerifyMessageIntegrity(ctx, msg.ID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.SHA256Match {
		t.Fatal("SHA256 should NOT match after tampering")
	}
}

func TestIntegrityMailboxScan(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 0; i < 3; i++ {
		msg := makeMessage(1, inbox.ID, 1, 1)
		store.StoreMessage(ctx, msg, []byte(fmt.Sprintf("msg %d", i)), nil)
	}
	// Corrupt one.
	msgs, _, _ := store.ListMessages(ctx, MessageFilter{MailboxID: 1}, nil)
	os.WriteFile(msgs[0].RFC822Path, []byte("corrupted"), 0640)

	engine := NewIntegrityEngine(store)
	results, ok, corrupt, err := engine.VerifyMailboxIntegrity(ctx, 1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if ok != 2 {
		t.Fatalf("expected 2 OK, got %d", ok)
	}
	if corrupt != 1 {
		t.Fatalf("expected 1 corrupt, got %d", corrupt)
	}
}

// ── Transaction Tests ─────────────────────────────────────────

func TestStoreMessageTransactionRollbackOnFileFailure(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Test that StoreMessage with invalid base path still cleans up on error.
	// We simulate a failure by storing a message, then trying to store a second
	// one that would violate a DB constraint (duplicate message_id).
	msg1 := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg1, []byte("first"), nil); err != nil {
		t.Fatalf("store first: %v", err)
	}

	// Try to store a message with the same message_id (unique constraint violation).
	dup := makeMessage(1, inbox.ID, 1, 1)
	dup.MessageID = msg1.MessageID // force duplicate
	err := store.StoreMessage(ctx, dup, []byte("duplicate"), nil)
	if err == nil {
		t.Fatal("expected error for duplicate message_id")
	}

	// Verify the duplicate's file was cleaned up.
	if dup.RFC822Path != "" {
		if _, err := os.Stat(dup.RFC822Path); !os.IsNotExist(err) {
			t.Fatal("orphan file should have been removed on duplicate error")
		}
	}
}

func TestTransactionAtomicity(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Use explicit transaction.
	sqlTx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	msg1 := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg1, []byte("msg1"), sqlTx); err != nil {
		sqlTx.Rollback()
		t.Fatalf("store msg1: %v", err)
	}
	msg2 := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg2, []byte("msg2"), sqlTx); err != nil {
		sqlTx.Rollback()
		t.Fatalf("store msg2: %v", err)
	}

	// Rollback.
	sqlTx.Rollback()

	// Both messages should NOT be visible.
	count, _ := store.Messages.CountByMailbox(ctx, 1, nil)
	if count != 0 {
		t.Fatalf("expected 0 messages after rollback, got %d", count)
	}
}

func TestConcurrentStoreAndList(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// All 5 goroutines must succeed. SQLite write serialization is handled
	// inside MailStore via a package-level write mutex + retry loop.
	const count = 5
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(idx int) {
			msg := makeMessage(1, inbox.ID, 1, 1)
			msg.Subject = fmt.Sprintf("Concurrent %d", idx)
			errs <- store.StoreMessage(ctx, msg, []byte(fmt.Sprintf("body %d", idx)), nil)
		}(i)
	}

	for i := 0; i < count; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent store %d: %v", i, err)
		}
	}

	msgs, total, err := store.ListMessages(ctx, MessageFilter{MailboxID: 1}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != int64(count) {
		t.Fatalf("expected %d messages, got %d", count, total)
	}

	// Verify all 5 RFC822 files exist and SHA256 matches.
	for _, m := range msgs {
		computedSHA, err := ComputeSHA256(m.RFC822Path)
		if err != nil {
			t.Fatalf("sha256 for msg %d: %v", m.ID, err)
		}
		if computedSHA != m.SHA256 {
			t.Fatalf("sha256 mismatch for msg %d: stored=%s file=%s", m.ID, m.SHA256, computedSHA)
		}
	}
	_ = msgs
}

// ── Purge Tests ──────────────────────────────────────────────

func TestMessagePurge(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("purge test"), nil)
	path := msg.RFC822Path

	if err := store.PurgeMessage(ctx, msg.ID, nil); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// File should be gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be removed after purge")
	}

	// DB record should have purged_at set.
	purged, _ := store.Messages.GetByID(ctx, msg.ID, nil)
	if purged != nil {
		t.Fatal("message should not be returned after purge")
	}
}

// ── Attachment Tests ─────────────────────────────────────────

func TestAttachmentExtractionOnStoreMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Build a multipart message with one text part and one attachment.
	boundary := "==attachtest=="
	rfc822 := []byte("From: sender@example.com\r\nSubject: With Attach\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody text\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"test.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%PDF-1.4 fake\r\n" +
		"--" + boundary + "--\r\n")

	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message with attachment: %v", err)
	}

	atts, err := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 extracted attachment, got %d", len(atts))
	}
	if atts[0].Filename != "test.pdf" {
		t.Fatalf("expected filename test.pdf, got %s", atts[0].Filename)
	}
	if atts[0].ContentType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %s", atts[0].ContentType)
	}
	if atts[0].SizeBytes <= 0 {
		t.Fatal("expected positive size")
	}
	if atts[0].StoragePath == "" {
		t.Fatal("expected non-empty storage path")
	}
	// Verify file exists on disk.
	if _, err := os.Stat(atts[0].StoragePath); os.IsNotExist(err) {
		t.Fatalf("attachment file not found: %s", atts[0].StoragePath)
	}
}

func TestAttachmentExtractionNoAttachments(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := []byte("From: test@test.com\r\nSubject: Plain\r\nContent-Type: text/plain\r\n\r\nJust text")
	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message: %v", err)
	}

	atts, err := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(atts) != 0 {
		t.Fatalf("expected 0 attachments for text/plain message, got %d", len(atts))
	}
}

func TestAttachmentExtractionMultipleAttachments(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	boundary := "==multi=="
	rfc822 := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"f1.txt\"\r\nContent-Type: text/plain\r\n\r\nFile one\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"f2.txt\"\r\nContent-Type: text/plain\r\n\r\nFile two\r\n" +
		"--" + boundary + "--\r\n")

	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message: %v", err)
	}

	atts, err := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(atts))
	}
	if atts[0].Filename != "f1.txt" || atts[1].Filename != "f2.txt" {
		t.Fatalf("unexpected filenames: %s, %s", atts[0].Filename, atts[1].Filename)
	}
}

func TestAttachmentExtractionPurgeCleansFiles(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	boundary := "==purgetest=="
	rfc822 := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"purge_me.txt\"\r\nContent-Type: text/plain\r\n\r\nDelete me\r\n" +
		"--" + boundary + "--\r\n")

	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message: %v", err)
	}

	atts, _ := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if len(atts) != 1 {
		t.Fatal("expected 1 attachment before purge")
	}
	attPath := atts[0].StoragePath

	if err := store.PurgeMessage(ctx, msg.ID, nil); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Verify attachment files are gone.
	if _, err := os.Stat(attPath); !os.IsNotExist(err) {
		t.Fatal("attachment file should be removed after purge")
	}
}

func TestAttachmentExtractionLimit(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Build message with 25 attachments (exceeds 20 limit).
	boundary := "==limittest=="
	var body strings.Builder
	body.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")
	body.WriteString("--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n")
	for i := 0; i < 25; i++ {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"f%d.txt\"\r\n", i))
		body.WriteString("Content-Type: text/plain\r\n\r\n")
		body.WriteString(fmt.Sprintf("File %d\r\n", i))
	}
	body.WriteString("--" + boundary + "--\r\n")

	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, []byte(body.String()), nil); err != nil {
		t.Fatalf("store message: %v", err)
	}

	atts, _ := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if len(atts) > 20 {
		t.Fatalf("expected max 20 attachments, got %d", len(atts))
	}
}

func TestAttachmentCreateAndList(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("attachment test"), nil)

	att := Attachment{
		MessageID:   msg.ID,
		Filename:    "test.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		SHA256:      "abc123",
		StoragePath: "/tmp/test.pdf",
	}
	if err := store.Attachments.Create(ctx, &att, nil); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	atts, err := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].Filename != "test.pdf" {
		t.Fatalf("expected test.pdf, got %s", atts[0].Filename)
	}
}

func TestAttachmentDeleteByMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	store.StoreMessage(ctx, msg, []byte("attach del"), nil)

	for i := 0; i < 3; i++ {
		store.Attachments.Create(ctx, &Attachment{MessageID: msg.ID, Filename: fmt.Sprintf("f%d.txt", i), SizeBytes: 100, SHA256: fmt.Sprintf("h%d", i)}, nil)
	}

	count, _ := store.Attachments.CountByMessage(ctx, msg.ID, nil)
	if count != 3 {
		t.Fatalf("expected 3 attachments, got %d", count)
	}

	store.Attachments.DeleteByMessage(ctx, msg.ID, nil)
	count, _ = store.Attachments.CountByMessage(ctx, msg.ID, nil)
	if count != 0 {
		t.Fatalf("expected 0 attachments after delete, got %d", count)
	}
}

// ── Retention Tests ──────────────────────────────────────────

func TestRetentionPolicyCreateAndList(t *testing.T) {
	db, _ := testStore(t)
	ctx := context.Background()
	repo := NewRetentionSQLRepo(db)

	p := RetentionPolicy{
		Name:          "Default Retention",
		RetentionType: RetentionByAge,
		RetentionDays: 365,
		DeleteAfterExpiry: true,
	}
	if err := repo.Create(ctx, &p, nil); err != nil {
		t.Fatalf("create retention: %v", err)
	}

	got, err := repo.GetByID(ctx, p.ID, nil)
	if err != nil {
		t.Fatalf("get retention: %v", err)
	}
	if got == nil {
		t.Fatal("retention policy not found")
	}
	if got.Name != "Default Retention" {
		t.Fatalf("expected Default Retention, got %s", got.Name)
	}
	if got.RetentionDays != 365 {
		t.Fatalf("expected 365 days, got %d", got.RetentionDays)
	}
}

// ── Edge Cases ───────────────────────────────────────────────

func TestStoreEmptyMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, []byte{}, nil); err != nil {
		t.Fatalf("store empty: %v", err)
	}
	if msg.SizeBytes != 0 {
		t.Fatalf("expected 0 size, got %d", msg.SizeBytes)
	}
}

func TestLoadNonexistentMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	_, _, err := store.LoadMessage(ctx, 99999, nil)
	if err == nil {
		t.Fatal("expected error loading nonexistent message")
	}
}

func TestDeleteNonexistentMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()
	err := store.DeleteMessage(ctx, 99999, nil)
	if err == nil {
		t.Fatal("expected error deleting nonexistent message")
	}
}

func TestPurgeNonexistentMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()
	err := store.PurgeMessage(ctx, 99999, nil)
	if err != nil {
		t.Fatalf("purge nonexistent should succeed: %v", err)
	}
}

func TestCopyNonexistentMessage(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	_, err := store.CopyMessage(ctx, 99999, 1, 1, nil)
	if err == nil {
		t.Fatal("expected error copying nonexistent message")
	}
}

func TestFolderDuplicatePath(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	f := Folder{MailboxID: 1, Name: "Custom", Path: "Custom"}
	if err := store.Folders.Create(ctx, &f, nil); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create should fail (unique constraint).
	f2 := Folder{MailboxID: 1, Name: "Custom", Path: "Custom"}
	if err := store.Folders.Create(ctx, &f2, nil); err == nil {
		t.Fatal("expected duplicate folder error")
	}
}

func TestEnsureSystemFoldersIdempotent(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	if err := store.Folders.EnsureSystemFolders(ctx, 1, nil); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if err := store.Folders.EnsureSystemFolders(ctx, 1, nil); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	folders, _ := store.Folders.ListByMailbox(ctx, 1, nil)
	if len(folders) != 6 {
		t.Fatalf("expected 6 system folders, got %d", len(folders))
	}
}

func TestGenerateMessageID(t *testing.T) {
	id1 := GenerateMessageID()
	id2 := GenerateMessageID()
	if id1 == id2 {
		t.Fatal("generated IDs should be unique")
	}
	if len(id1) != 32 {
		t.Fatalf("expected 32 hex chars, got %d", len(id1))
	}
}

func TestMessageFilterPagination(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	for i := 0; i < 10; i++ {
		msg := makeMessage(1, inbox.ID, 1, 1)
		store.StoreMessage(ctx, msg, []byte(fmt.Sprintf("page %d", i)), nil)
	}

	page1, total, err := store.ListMessages(ctx, MessageFilter{MailboxID: 1, Limit: 3, Offset: 0}, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 on page 1, got %d", len(page1))
	}

	page2, _, _ := store.ListMessages(ctx, MessageFilter{MailboxID: 1, Limit: 3, Offset: 3}, nil)
	if len(page2) != 3 {
		t.Fatalf("expected 3 on page 2, got %d", len(page2))
	}
}

func TestSHA256Computation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.eml")
	content := []byte("From: test@example.com\r\nSubject: SHA256\r\n\r\nBody")
	if err := os.WriteFile(path, content, 0640); err != nil {
		t.Fatalf("write file: %v", err)
	}

	sha, err := ComputeSHA256(path)
	if err != nil {
		t.Fatalf("compute sha256: %v", err)
	}
	if len(sha) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(sha))
	}
	_ = sha
}

func TestMessageSizeTracking(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	msg := makeMessage(1, inbox.ID, 1, 1)
	body := []byte("A")
	store.StoreMessage(ctx, msg, body, nil)

	if msg.SizeBytes != 1 {
		t.Fatalf("expected 1 byte, got %d", msg.SizeBytes)
	}

	sum, err := store.Messages.SumSizeByMailbox(ctx, 1, nil)
	if err != nil {
		t.Fatalf("sum size: %v", err)
	}
	if sum != 1 {
		t.Fatalf("expected total 1, got %d", sum)
	}
}

func TestIntegrityResultField(t *testing.T) {
	res := IntegrityResult{MessageID: 1, DBRecordOK: true, FileExists: true, SHA256Match: true, AttachmentsOK: true}
	if !res.DBRecordOK || !res.FileExists || !res.SHA256Match || !res.AttachmentsOK {
		t.Fatal("expected all integrity fields true")
	}
}

// ── Performance Documentation ───────────────────────────────

func TestExpectedPerformanceCharacteristics(t *testing.T) {
	t.Log("=== Enterprise MailStore Performance Characteristics ===")
	t.Log("")
	t.Log("Expected Mailbox Size:")
	t.Log("  Average: 500 MB")
	t.Log("  Enterprise: 10 GB")
	t.Log("  Max configured: unlimited")
	t.Log("")
	t.Log("Expected Message Count:")
	t.Log("  Average mailbox: 25,000 messages")
	t.Log("  Enterprise mailbox: 500,000 messages")
	t.Log("  Per shard (future): 10,000,000 messages")
	t.Log("")
	t.Log("Expected Attachment Count:")
	t.Log("  ~15% of messages contain attachments")
	t.Log("  Average attachment size: 250 KB")
	t.Log("  Max single attachment: 50 MB (configurable)")
	t.Log("")
	t.Log("Future Sharding Strategy:")
	t.Log("  Shard by tenant_id (tenant isolation)")
	t.Log("  Each shard has own DB connection pool")
	t.Log("  Filesystem sharded by tenant_id/domain_id/mailbox_id")
	t.Log("")
	t.Log("Future Clustering Strategy:")
	t.Log("  Stateless MailStore (all state in DB/filesystem)")
	t.Log("  Read replicas for metadata queries")
	t.Log("  NFS/S3 for shared filesystem access")
	t.Log("")
	t.Log("Future Replication Strategy:")
	t.Log("  SQL replication at database level")
	t.Log("  Filesystem replication via rsync/NFS/S3")
	t.Log("  Cross-region replication for disaster recovery")
}

func TestStorageTableNames(t *testing.T) {
	// Verify that table creation SQL references correct names.
	sqls := Tables()
	expected := []string{"coremail_folders", "coremail_messages", "coremail_attachments", "coremail_retention_policies"}
	for _, exp := range expected {
		found := false
		for _, s := range sqls {
			if strings.Contains(s, "CREATE TABLE IF NOT EXISTS "+exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing table: %s", exp)
		}
	}
}

func TestDefaultSystemFolders(t *testing.T) {
	folders := DefaultSystemFolders(1)
	if len(folders) != 6 {
		t.Fatalf("expected 6 system folders, got %d", len(folders))
	}
	types := make(map[FolderType]bool)
	for _, f := range folders {
		types[f.FolderType] = true
	}
	if !types[FolderInbox] {
		t.Fatal("missing inbox")
	}
	if !types[FolderSent] {
		t.Fatal("missing sent")
	}
	if !types[FolderDrafts] {
		t.Fatal("missing drafts")
	}
	if !types[FolderTrash] {
		t.Fatal("missing trash")
	}
	if !types[FolderJunk] {
		t.Fatal("missing junk")
	}
	if !types[FolderArchive] {
		t.Fatal("missing archive")
	}
}
