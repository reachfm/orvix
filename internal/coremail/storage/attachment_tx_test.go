package storage

// Transaction-aware attachment metadata tests.
//
// ExtractAndStoreAttachmentsTx pins the contract that attachment
// metadata must commit or roll back atomically with the Sent-message
// row: on rollback ZERO attachment metadata rows survive; on success
// every extracted attachment references the committed message exactly
// once; retry/replay never duplicates attachment rows; and the
// attachment bytes come from the in-memory RFC822 payload (no separate
// staging subsystem is involved).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
)

// multipartRFC822WithAttachments builds a multipart/mixed RFC822 body
// with a text part plus the given attachment filenames.
func multipartRFC822WithAttachments(attachments ...string) []byte {
	boundary := "==txattach=="
	b := "Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n"
	for i, name := range attachments {
		b += "--" + boundary + "\r\nContent-Disposition: attachment; filename=\"" + name + "\"\r\n" +
			"Content-Type: application/octet-stream\r\n\r\ncontent-of-" + name + "-" + fmt.Sprint(i) + "\r\n"
	}
	b += "--" + boundary + "--\r\n"
	return []byte(b)
}

// queryRower is satisfied by both *sql.DB and *sql.Tx.
type queryRower interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

func countRows(t *testing.T, q queryRower, table string) int64 {
	t.Helper()
	var n int64
	if err := q.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestExtractAttachmentsTxRollbackLeavesZeroRows proves that when the
// caller rolls back the shared transaction, ZERO attachment metadata
// rows survive (and the message row rolls back with them).
func TestExtractAttachmentsTxRollbackLeavesZeroRows(t *testing.T) {
	db, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	msg := makeMessage(1, inbox.ID, 1, 1)
	if err := store.StoreMessage(ctx, msg, multipartRFC822WithAttachments("a.txt", "b.txt"), tx); err != nil {
		t.Fatalf("store message in tx: %v", err)
	}
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, multipartRFC822WithAttachments("a.txt", "b.txt"), tx); err != nil {
		t.Fatalf("extract attachments in tx: %v", err)
	}
	// Inside the tx the rows exist (uncommitted).
	if n := countRows(t, tx, "coremail_attachments"); n != 2 {
		t.Fatalf("in-tx attachment rows = %d, want 2", n)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// After rollback nothing survives.
	if n := countRows(t, db, "coremail_attachments"); n != 0 {
		t.Errorf("attachment rows survived rollback: %d, want 0", n)
	}
	if n := countRows(t, db, "coremail_messages"); n != 0 {
		t.Errorf("message rows survived rollback: %d, want 0", n)
	}
}

// TestExtractAttachmentsTxSuccessReferencesMessageExactlyOnce proves
// that on commit each extracted attachment references the committed
// message exactly once, with a real file on disk and a real SHA256.
func TestExtractAttachmentsTxSuccessReferencesMessageExactlyOnce(t *testing.T) {
	db, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := multipartRFC822WithAttachments("doc.pdf", "notes.txt")
	if err := store.StoreMessage(ctx, msg, rfc822, tx); err != nil {
		t.Fatalf("store message in tx: %v", err)
	}
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, tx); err != nil {
		t.Fatalf("extract attachments in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := countRows(t, db, "coremail_messages"); n != 1 {
		t.Fatalf("message rows = %d, want 1", n)
	}
	atts, err := store.Attachments.ListByMessage(ctx, msg.ID, nil)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("attachment rows = %d, want 2", len(atts))
	}
	seen := map[string]bool{}
	for _, a := range atts {
		if a.MessageID != msg.ID {
			t.Errorf("attachment %q references message_id=%d, want %d", a.Filename, a.MessageID, msg.ID)
		}
		if seen[a.Filename] {
			t.Errorf("duplicate attachment row for %q", a.Filename)
		}
		seen[a.Filename] = true
		if a.SHA256 == "" {
			t.Errorf("attachment %q has empty sha256", a.Filename)
		}
		if a.StoragePath == "" {
			t.Errorf("attachment %q has empty storage path", a.Filename)
		}
		if _, err := os.Stat(a.StoragePath); os.IsNotExist(err) {
			t.Errorf("attachment file missing for %q: %s", a.Filename, a.StoragePath)
		}
	}
}

// TestExtractAttachmentsTxMultiAttachment proves a three-attachment
// message extracts all three rows atomically with the message.
func TestExtractAttachmentsTxMultiAttachment(t *testing.T) {
	db, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := multipartRFC822WithAttachments("one.txt", "two.txt", "three.png")
	if err := store.StoreMessage(ctx, msg, rfc822, tx); err != nil {
		t.Fatalf("store message in tx: %v", err)
	}
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, tx); err != nil {
		t.Fatalf("extract attachments in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := countRows(t, db, "coremail_attachments"); n != 3 {
		t.Fatalf("attachment rows = %d, want 3", n)
	}
}

// failAfterNAttachmentRepo fails every Create after the Nth success.
// Used to simulate partial attachment-metadata failure inside the tx.
type failAfterNAttachmentRepo struct {
	AttachmentRepository
	callCount int
	failAfter int
}

func (r *failAfterNAttachmentRepo) Create(ctx context.Context, a *Attachment, tx interface{}) error {
	r.callCount++
	if r.callCount > r.failAfter {
		return fmt.Errorf("simulated attachment metadata failure after %d successes", r.failAfter)
	}
	return r.AttachmentRepository.Create(ctx, a, tx)
}

// TestExtractAttachmentsTxFailureRollsBackEverything proves that when
// attachment extraction fails mid-transaction and the caller rolls
// back, ZERO attachment rows AND the message row disappear — the
// committed state can never hold a message with missing attachment
// metadata.
func TestExtractAttachmentsTxFailureRollsBackEverything(t *testing.T) {
	db, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	store.Attachments = &failAfterNAttachmentRepo{AttachmentRepository: store.Attachments, failAfter: 1}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := multipartRFC822WithAttachments("ok.txt", "boom.txt")
	if err := store.StoreMessage(ctx, msg, rfc822, tx); err != nil {
		t.Fatalf("store message in tx: %v", err)
	}
	err = store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, tx)
	if err == nil {
		t.Fatal("expected attachment extraction error, got nil")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if n := countRows(t, db, "coremail_attachments"); n != 0 {
		t.Errorf("attachment rows survived failed extraction: %d, want 0", n)
	}
	if n := countRows(t, db, "coremail_messages"); n != 0 {
		t.Errorf("message rows survived failed extraction: %d, want 0", n)
	}
}

// TestExtractAttachmentsTxDuplicatePrevention proves retry/replay can
// never duplicate attachment rows: (a) re-running extraction on the
// same message inside the same transaction is a no-op; (b) replaying
// the same MessageID in a new transaction fails on the UNIQUE(message_id)
// constraint before any attachment row is written, so the total
// attachment count stays exactly N.
func TestExtractAttachmentsTxDuplicatePrevention(t *testing.T) {
	db, store := testStore(t)
	ctx := context.Background()

	store.Folders.EnsureSystemFolders(ctx, 1, nil)
	inbox, _ := store.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// First store: message + 2 attachment rows, committed.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	msg := makeMessage(1, inbox.ID, 1, 1)
	rfc822 := multipartRFC822WithAttachments("a.txt", "b.txt")
	if err := store.StoreMessage(ctx, msg, rfc822, tx); err != nil {
		t.Fatalf("store message in tx: %v", err)
	}
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, tx); err != nil {
		t.Fatalf("extract attachments: %v", err)
	}
	// (a) In-tx replay is a no-op.
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, tx); err != nil {
		t.Fatalf("re-extract attachments in same tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := countRows(t, db, "coremail_attachments"); n != 2 {
		t.Fatalf("attachment rows after first store = %d, want 2", n)
	}

	// (b) Replay with the same MessageID in a new tx must fail on the
	// UNIQUE(message_id) constraint, rolling back before any
	// attachment row is written.
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	replay := *msg // copy with the same MessageID
	replay.ID = 0
	if err := store.StoreMessage(ctx, &replay, rfc822, tx2); err == nil {
		_ = tx2.Rollback()
		t.Fatal("expected replay StoreMessage to fail on UNIQUE(message_id)")
	}
	_ = tx2.Rollback()

	if n := countRows(t, db, "coremail_messages"); n != 1 {
		t.Errorf("message rows after replay = %d, want 1", n)
	}
	if n := countRows(t, db, "coremail_attachments"); n != 2 {
		t.Errorf("attachment rows after replay = %d, want 2 (no duplicates)", n)
	}

	// Post-commit replay of extraction on the same committed message
	// is also a no-op (idempotency guard on committed rows).
	if err := store.ExtractAndStoreAttachmentsTx(ctx, msg.ID, rfc822, nil); err != nil {
		t.Fatalf("post-commit re-extract: %v", err)
	}
	if n := countRows(t, db, "coremail_attachments"); n != 2 {
		t.Errorf("attachment rows after post-commit re-extract = %d, want 2", n)
	}
}
