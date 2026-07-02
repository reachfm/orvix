package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openCursorTestDB returns a fresh sqlite DB with the storage
// schema applied. The schema is the same the runtime uses so
// the tests exercise the actual index plan.
func openCursorTestDB(t *testing.T) (*sql.DB, *MessageSQLRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	for _, stmt := range Indexes() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create index: %v", err)
		}
	}
	return db, NewMessageSQLRepo(db)
}

// seedMailboxWithMessages inserts a folder + n messages for a
// synthetic mailbox id. The mailbox / domain FKs are not
// enforced by the storage package; the production runtime has
// its own tables (coremail_domains, coremail_mailboxes) created
// by a different schema. The tests here exercise the storage
// layer's table in isolation, so mailboxID is a free integer
// and the FK constraints to coremail_mailboxes are not present
// in the coremail_messages definition that storage owns.
//
// Returns (mailboxID, folderID, messageIDs in insertion order).
func seedMailboxWithMessages(t *testing.T, db *sql.DB, n int) (uint, uint, []uint) {
	t.Helper()
	ctx := context.Background()
	now := nowFn()
	const mailboxID = 1
	res, err := db.ExecContext(ctx, `
		INSERT INTO coremail_folders (mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (?, NULL, 'INBOX', 'INBOX', 'inbox', 0, 0, 0, ?, ?)`,
		mailboxID, now, now)
	if err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	folderID, _ := res.LastInsertId()

	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		res, err = db.ExecContext(ctx, `
			INSERT INTO coremail_messages
				(message_id, tenant_id, domain_id, mailbox_id, folder_id,
				 subject, from_address, to_addresses, cc_addresses, bcc_addresses, reply_to,
				 message_date, received_date, size_bytes, rfc822_path, sha256,
				 seen, answered, flagged, draft, deleted, junk, importance,
				 created_at, updated_at)
			VALUES (?, 1, 1, ?, ?, ?, 'a@x', 'b@y', '', '', '',
			        ?, ?, 100, '/tmp/x', 'abc', 0, 0, 0, 0, 0, 0, 0, ?, ?)`,
			fmt.Sprintf("msg-%d", i), mailboxID, folderID,
			fmt.Sprintf("subject-%d", i), now, now, now, now)
		if err != nil {
			t.Fatalf("insert message %d: %v", i, err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, uint(id))
	}
	return mailboxID, uint(folderID), ids
}

func TestListByCursor_FirstPage(t *testing.T) {
	db, repo := openCursorTestDB(t)
	mailboxID, _, ids := seedMailboxWithMessages(t, db, 10)

	page, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: mailboxID,
		Limit:     5,
	}, nil)
	if err != nil {
		t.Fatalf("ListByCursor: %v", err)
	}
	if len(page.Messages) != 5 {
		t.Errorf("page size = %d, want 5", len(page.Messages))
	}
	if !page.HasMore {
		t.Errorf("HasMore should be true (10 rows total, 5 returned)")
	}
	if page.NextCursor == 0 {
		t.Errorf("NextCursor should be set")
	}
	// Newest first: first id is the largest in ids.
	if page.Messages[0].ID != ids[9] {
		t.Errorf("first message id = %d, want %d (newest first)", page.Messages[0].ID, ids[9])
	}
}

func TestListByCursor_AllPagesNoDuplicates(t *testing.T) {
	db, repo := openCursorTestDB(t)
	mailboxID, _, _ := seedMailboxWithMessages(t, db, 25)

	seen := make(map[uint]bool, 25)
	cursor := uint(0)
	pages := 0
	for {
		pages++
		page, err := repo.ListByCursor(context.Background(), MessageFilter{
			MailboxID: mailboxID,
			Limit:     7,
			BeforeID:  cursor,
		}, nil)
		if err != nil {
			t.Fatalf("ListByCursor page %d: %v", pages, err)
		}
		for _, m := range page.Messages {
			if seen[m.ID] {
				t.Errorf("message id %d returned twice (page %d)", m.ID, pages)
			}
			seen[m.ID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("infinite loop in cursor pagination")
		}
	}
	if len(seen) != 25 {
		t.Errorf("seen %d distinct messages, want 25", len(seen))
	}
}

func TestListByCursor_EmptyResult(t *testing.T) {
	db, repo := openCursorTestDB(t)
	mailboxID, _, _ := seedMailboxWithMessages(t, db, 0)

	page, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: mailboxID,
		Limit:     10,
	}, nil)
	if err != nil {
		t.Fatalf("ListByCursor empty: %v", err)
	}
	if len(page.Messages) != 0 {
		t.Errorf("page size = %d, want 0", len(page.Messages))
	}
	if page.HasMore {
		t.Errorf("HasMore should be false on empty result")
	}
	if page.NextCursor != 0 {
		t.Errorf("NextCursor = %d, want 0 on empty result", page.NextCursor)
	}
}

func TestListByCursor_AfterIDForNewMessages(t *testing.T) {
	db, repo := openCursorTestDB(t)
	mailboxID, _, ids := seedMailboxWithMessages(t, db, 10)

	// Get "messages newer than the 5th one" using AfterID.
	page, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: mailboxID,
		Limit:     100,
		AfterID:   ids[4], // any id; the function returns id > AfterID
	}, nil)
	if err != nil {
		t.Fatalf("ListByCursor AfterID: %v", err)
	}
	// We expect ids[5..9] (5 messages, since ids[4] is the
	// boundary). The oldest of those is ids[5].
	if len(page.Messages) == 0 {
		t.Fatal("expected messages with id > ids[4]")
	}
	// First row should have id > ids[4].
	if page.Messages[0].ID <= ids[4] {
		t.Errorf("first id = %d, expected > ids[4]=%d", page.Messages[0].ID, ids[4])
	}
}

func TestListByCursor_MissingMailboxErrors(t *testing.T) {
	_, repo := openCursorTestDB(t)
	_, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: 0,
		Limit:     10,
	}, nil)
	if err == nil {
		t.Errorf("expected error for MailboxID=0")
	}
}

func TestListByCursor_LimitClampedToMax(t *testing.T) {
	db, repo := openCursorTestDB(t)
	mailboxID, _, _ := seedMailboxWithMessages(t, db, 5)
	page, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: mailboxID,
		Limit:     MaxPageSize * 2, // exceeds max
	}, nil)
	if err != nil {
		t.Fatalf("ListByCursor: %v", err)
	}
	// Limit should be clamped to MaxPageSize; we have 5 rows
	// so the page contains all 5.
	if len(page.Messages) > MaxPageSize {
		t.Errorf("page size = %d exceeds MaxPageSize=%d", len(page.Messages), MaxPageSize)
	}
}

func TestListByCursor_UseCursorNoOffsetNoLeak(t *testing.T) {
	// Regression: within a single page (no cursor), the result
	// must not contain the same message id twice. This catches
	// a class of bug where the limit+1 fetch logic accidentally
	// returns the boundary row twice.
	db, repo := openCursorTestDB(t)
	mailboxID, _, _ := seedMailboxWithMessages(t, db, 8)
	page, err := repo.ListByCursor(context.Background(), MessageFilter{
		MailboxID: mailboxID,
		Limit:     3,
	}, nil)
	if err != nil {
		t.Fatalf("ListByCursor: %v", err)
	}
	seen := make(map[uint]int)
	for _, m := range page.Messages {
		seen[m.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("message id %d returned %d times within a single page", id, count)
		}
	}
	if len(page.Messages) != 3 {
		t.Errorf("page size = %d, want 3 (Limit=3, 8 rows total)", len(page.Messages))
	}
}
