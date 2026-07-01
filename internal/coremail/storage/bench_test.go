package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// BenchmarkCursorPagination measures the cost of paging through
// a mailbox of N messages using cursor pagination. The expected
// result is constant per-page cost regardless of N, because the
// query plan is a range scan on the (mailbox_id, folder_id, id)
// index.
//
// Run with:
//
//	go test ./internal/coremail/storage -run '^$' -bench BenchmarkCursorPagination -benchmem
func BenchmarkCursorPagination(b *testing.B) {
	db, repo := openBenchDB(b)
	mailboxID, _, _ := seedBenchMessages(b, db, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cursor := uint(0)
		total := 0
		for {
			page, err := repo.ListByCursor(context.Background(), MessageFilter{
				MailboxID: mailboxID,
				Limit:     50,
				BeforeID:  cursor,
			}, nil)
			if err != nil {
				b.Fatalf("ListByCursor: %v", err)
			}
			total += len(page.Messages)
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
		if total != 1000 {
			b.Fatalf("walked %d messages, want 1000", total)
		}
	}
}

// BenchmarkCursorPagination_LargeDataset seeds 10k messages and
// walks them. The point is to confirm that the per-page cost
// stays flat at 10k — the previous OFFSET-based path would
// degrade linearly with page depth at this size.
func BenchmarkCursorPagination_LargeDataset(b *testing.B) {
	db, repo := openBenchDB(b)
	mailboxID, _, _ := seedBenchMessages(b, db, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cursor := uint(0)
		pages := 0
		for {
			page, err := repo.ListByCursor(context.Background(), MessageFilter{
				MailboxID: mailboxID,
				Limit:     100,
				BeforeID:  cursor,
			}, nil)
			if err != nil {
				b.Fatalf("ListByCursor: %v", err)
			}
			pages++
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
	}
}

// BenchmarkOffsetPagination_ForComparison runs the legacy OFFSET
// path so the difference is visible. Note that with the cursor
// path, page 100 is the same cost as page 1; with OFFSET, page
// 100 must scan 9900 rows before returning the answer.
func BenchmarkOffsetPagination_ForComparison(b *testing.B) {
	db, repo := openBenchDB(b)
	mailboxID, _, _ := seedBenchMessages(b, db, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Walk 5 pages of 50 rows each. With OFFSET this means
		// scanning up to 250 rows per page; with cursor it is
		// always 50.
		for offset := 0; offset < 250; offset += 50 {
			_, _, err := repo.List(context.Background(), MessageFilter{
				MailboxID: mailboxID,
				Limit:     50,
				Offset:    offset,
			}, nil)
			if err != nil {
				b.Fatalf("List: %v", err)
			}
		}
	}
}

// BenchmarkMessageInsert measures single-row message insertion
// cost with the production schema (and indexes). Operators
// running bulk ingest should switch to a COPY-style import for
// sustained throughput, but this benchmark proves the per-row
// path is reasonable.
func BenchmarkMessageInsert(b *testing.B) {
	db, _ := openBenchDB(b)
	_, folderID, _ := seedBenchMessages(b, db, 1) // one message to create the folder

	now := time.Now().UTC()
	stmt, err := db.Prepare(`
		INSERT INTO coremail_messages
			(message_id, tenant_id, domain_id, mailbox_id, folder_id,
			 subject, from_address, to_addresses, cc_addresses, bcc_addresses, reply_to,
			 message_date, received_date, size_bytes, rfc822_path, sha256,
			 seen, answered, flagged, draft, deleted, junk, importance,
			 created_at, updated_at)
		VALUES (?, 1, 1, 1, ?, ?, 'a@x', 'b@y', '', '', '', ?, ?, 100, '/tmp/x', 'abc',
		        0, 0, 0, 0, 0, 0, 0, ?, ?)`)
	if err != nil {
		b.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := stmt.Exec(
			fmt.Sprintf("bench-%d", i), folderID, fmt.Sprintf("subj-%d", i),
			now, now, now, now)
		if err != nil {
			b.Fatalf("insert: %v", err)
		}
	}
}

// openBenchDB is the benchmark variant of openCursorTestDB.
// It uses the same schema but the cleanup is registered as a
// b.Cleanup callback.
func openBenchDB(b *testing.B) (*sql.DB, *MessageSQLRepo) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		b.Fatalf("open sqlite: %v", err)
	}
	b.Cleanup(func() { db.Close() })
	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			b.Fatalf("create table: %v", err)
		}
	}
	for _, stmt := range Indexes() {
		if _, err := db.Exec(stmt); err != nil {
			b.Fatalf("create index: %v", err)
		}
	}
	return db, NewMessageSQLRepo(db)
}

// seedBenchMessages inserts n messages and returns the mailbox
// id, folder id, and message ids.
func seedBenchMessages(b *testing.B, db *sql.DB, n int) (uint, uint, []uint) {
	b.Helper()
	ctx := context.Background()
	now := nowFn()
	const mailboxID = 1
	res, err := db.ExecContext(ctx, `
		INSERT INTO coremail_folders (mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (?, NULL, 'INBOX', 'INBOX', 'inbox', 0, 0, 0, ?, ?)`,
		mailboxID, now, now)
	if err != nil {
		b.Fatalf("insert folder: %v", err)
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
			b.Fatalf("insert message %d: %v", i, err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, uint(id))
	}
	return mailboxID, uint(folderID), ids
}
