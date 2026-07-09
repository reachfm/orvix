package loadtest

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- shared helpers ---

// uniqueKey prefixes every benchmark metric so the reader can
// determine which driver the numbers came from.
func (b *benchEnv) uniqueKey(suffix string) string {
	return fmt.Sprintf("%s_%s", b.Driver, suffix)
}

// --- Schema compatibility ---

// TestPostgresSchemaCompat attempts to create the benchmark table and
// indexes on whatever database ORVIX_DB_DRIVER / ORVIX_DB_DSN point
// at.  When the driver is postgres and no DSN is available the test
// skips (the opener returns nil).
func TestPostgresSchemaCompat(t *testing.T) {
	b, err := openBenchDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b == nil {
		t.Skip("ORVIX_RUN_DB_LOADTEST not set or postgres DSN empty")
	}
	defer b.closeBenchDB()
	defer b.dropBenchTable()

	if err := b.createBenchTable(); err != nil {
		t.Fatalf("create benchmark table on %s: %v", b.Driver, err)
	}

	// Verify the table exists by inserting and counting a single row.
	_, err = b.DB.Exec(
		`INSERT INTO loadtest_coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		"bench-1", 1, 0, "schema test", "a@b.com", "c@d.com", 1000000000, 1024, 1000000000,
	)
	if err != nil {
		t.Fatalf("insert into benchmark table on %s: %v", b.Driver, err)
	}

	var count int
	if err := b.DB.QueryRow(`SELECT COUNT(*) FROM loadtest_coremail_messages`).Scan(&count); err != nil {
		t.Fatalf("count benchmark rows on %s: %v", b.Driver, err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
	t.Logf("schema compat: %s created table, inserted row, verified count = %d", b.Driver, count)

	// Check indexes exist.  The DDL is "CREATE INDEX IF NOT EXISTS <name> ON ..."
	// so we skip past "IF NOT EXISTS" tokens.
	for _, idx := range benchIndexes(b.Driver) {
		parts := strings.Fields(idx)
		var idxName string
		for i, p := range parts {
			if !strings.EqualFold(p, "INDEX") {
				continue
			}
			// Walk forward past "IF NOT EXISTS" noise to the real name.
			for j := i + 1; j < len(parts); j++ {
				if strings.EqualFold(parts[j], "IF") || strings.EqualFold(parts[j], "NOT") || strings.EqualFold(parts[j], "EXISTS") {
					continue
				}
				idxName = parts[j]
				break
			}
			break
		}
		if idxName == "" {
			continue
		}

		var indexCount int
		switch b.Driver {
		case "postgres":
			err = b.DB.QueryRow(
				`SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, idxName,
			).Scan(&indexCount)
		default:
			err = b.DB.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, idxName,
			).Scan(&indexCount)
		}
		if err != nil {
			t.Errorf("check index %s: %v", idxName, err)
		} else if indexCount == 0 {
			t.Errorf("index %s not found on %s", idxName, b.Driver)
		} else {
			t.Logf("index %s verified on %s", idxName, b.Driver)
		}
	}
}

// --- Real benchmark tests ---

// TestBenchmarkInsert inserts N rows into loadtest_coremail_messages
// in batches and reports the sustained insert rate.
func TestBenchmarkInsert(t *testing.T) {
	b, err := openBenchDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b == nil {
		t.Skip("ORVIX_RUN_DB_LOADTEST not set or postgres DSN empty")
	}
	defer b.closeBenchDB()
	defer b.dropBenchTable()

	if err := b.createBenchTable(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	t.Logf("insert benchmark: driver=%s rows=%d mailboxes=%d batch=%d",
		b.Driver, b.Rows, b.Mailboxes, b.BatchSize)

	start := time.Now()
	inserted := int64(0)

	for batchStart := 0; batchStart < b.Rows; batchStart += b.BatchSize {
		end := batchStart + b.BatchSize
		if end > b.Rows {
			end = b.Rows
		}
		batch := end - batchStart

		// Build a multi-row INSERT.
		placeholders := make([]string, 0, batch)
		args := make([]interface{}, 0, batch*9)
		for i := 0; i < batch; i++ {
			seq := batchStart + i
			mb := seq % b.Mailboxes
			folder := (seq / 100) % 5
			now := time.Now().UnixNano()

			switch b.Driver {
			case "postgres":
				placeholders = append(placeholders,
					fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
						len(args)+1, len(args)+2, len(args)+3, len(args)+4,
						len(args)+5, len(args)+6, len(args)+7, len(args)+8,
						len(args)+9))
			default:
				placeholders = append(placeholders, "(?,?,?,?,?,?,?,?,?)")
			}
			args = append(args,
				fmt.Sprintf("bench-%d-%d", time.Now().UnixNano(), seq),
				mb,
				folder,
				synthSubject(seq),
				fmt.Sprintf("sender-%d@example.com", seq),
				fmt.Sprintf("recipient-%d@example.com", seq),
				now,
				int64(1024+seq%10240),
				now,
			)
		}

		stmt := fmt.Sprintf(
			`INSERT INTO loadtest_coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, created_at) VALUES %s`,
			strings.Join(placeholders, ","),
		)
		if _, err := b.DB.Exec(stmt, args...); err != nil {
			t.Fatalf("insert batch at row %d: %v", batchStart, err)
		}
		atomic.AddInt64(&inserted, int64(batch))
	}

	elapsed := time.Since(start)
	rate := float64(inserted) / elapsed.Seconds()
	t.Logf("insert: driver=%s rows=%d elapsed=%v rate=%.0f rows/sec", b.Driver, inserted, elapsed.Round(time.Millisecond), rate)

	if rate < 100 {
		t.Errorf("INSERT_RATE_LOW: driver=%s rate=%.0f rows/sec (want >= 100)", b.Driver, rate)
	}
}

// TestBenchmarkListLatest queries the latest 50 messages per mailbox
// and measures average latency.
func TestBenchmarkListLatest(t *testing.T) {
	b, err := openBenchDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b == nil {
		t.Skip("ORVIX_RUN_DB_LOADTEST not set or postgres DSN empty")
	}
	defer b.closeBenchDB()
	defer b.dropBenchTable()

	if err := b.createBenchTable(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Pre-populate with 10k rows minimum to exercise indexes.
	preRows := b.Rows
	if preRows < 10000 {
		preRows = 10000
	}
	_ = b.bulkInsert(preRows, t)

	const limit = 50
	var totalOps int64
	var totalLatency int64
	var mu sync.Mutex

	start := time.Now()
	var wg sync.WaitGroup
	for mb := 0; mb < b.Mailboxes; mb++ {
		mb := mb
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				opStart := time.Now()
				rows, err := b.DB.Query(
					`SELECT id, subject, from_address, received_date, seen, flagged
					 FROM loadtest_coremail_messages
					 WHERE mailbox_id = $1
					 ORDER BY received_date DESC
					 LIMIT $2`, mb, limit,
				)
				if err != nil {
					t.Errorf("list query: %v", err)
					return
				}
				n := 0
				for rows.Next() {
					n++
				}
				rows.Close()
				opLat := time.Since(opStart)
				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(opLat))
				if n > limit {
					t.Errorf("returned %d rows, expected <= %d", n, limit)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	avgLat := time.Duration(0)
	if totalOps > 0 {
		avgLat = time.Duration(totalLatency / totalOps)
	}
	mu.Lock()
	t.Logf("list-latest: driver=%s queries=%d avg-latency=%v wall=%v",
		b.Driver, totalOps, avgLat, elapsed.Round(time.Millisecond))
	mu.Unlock()

	if avgLat > 500*time.Millisecond {
		t.Errorf("LIST_LATENCY_HIGH: driver=%s avg-latency=%v (want < 500ms)", b.Driver, avgLat)
	}
}

// TestBenchmarkCursorPagination simulates cursor-based pagination
// (WHERE id < cursor ORDER BY id DESC LIMIT N).
func TestBenchmarkCursorPagination(t *testing.T) {
	b, err := openBenchDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b == nil {
		t.Skip("ORVIX_RUN_DB_LOADTEST not set or postgres DSN empty")
	}
	defer b.closeBenchDB()
	defer b.dropBenchTable()

	if err := b.createBenchTable(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	preRows := b.Rows
	if preRows < 10000 {
		preRows = 10000
	}
	_ = b.bulkInsert(preRows, t)

	// Get the max ID for a mailbox to start pagination.
	var maxID int64
	if err := b.DB.QueryRow(
		`SELECT COALESCE(MAX(id), 0) FROM loadtest_coremail_messages WHERE mailbox_id = 0`,
	).Scan(&maxID); err != nil {
		t.Fatalf("max id: %v", err)
	}

	const pageSize = 50
	pages := 0
	cursor := maxID + 1
	start := time.Now()

	for {
		rows, err := b.DB.Query(
			`SELECT id, subject FROM loadtest_coremail_messages
			 WHERE mailbox_id = 0 AND id < $1
			 ORDER BY id DESC
			 LIMIT $2`, cursor, pageSize,
		)
		if err != nil {
			t.Fatalf("cursor query: %v", err)
		}
		count := 0
		var lastID int64
		for rows.Next() {
			count++
			rows.Scan(&lastID, new(string))
		}
		rows.Close()
		if count == 0 {
			break
		}
		cursor = lastID
		pages++
		if pages > 500 {
			break // safety cap
		}
	}

	elapsed := time.Since(start)
	avgPerPage := elapsed / time.Duration(pages)
	t.Logf("cursor-pagination: driver=%s pages=%d total=%v avg-per-page=%v",
		b.Driver, pages, elapsed.Round(time.Millisecond), avgPerPage)
}

// TestBenchmarkFlagUpdates updates seen/deleted flags concurrently
// and measures average per-update latency.
func TestBenchmarkFlagUpdates(t *testing.T) {
	b, err := openBenchDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b == nil {
		t.Skip("ORVIX_RUN_DB_LOADTEST not set or postgres DSN empty")
	}
	defer b.closeBenchDB()
	defer b.dropBenchTable()

	if err := b.createBenchTable(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	preRows := b.Rows
	if preRows < 10000 {
		preRows = 10000
	}
	if err := b.bulkInsert(preRows, t); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	// Grab some IDs to update.
	var ids []int64
	rows, err := b.DB.Query(
		`SELECT id FROM loadtest_coremail_messages WHERE mailbox_id = 0 ORDER BY id LIMIT 1000`,
	)
	if err != nil {
		t.Fatalf("fetch ids: %v", err)
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()

	if len(ids) == 0 {
		t.Skip("no rows to update")
	}

	var totalOps int64
	var totalLatency int64
	var wg sync.WaitGroup
	concurrency := 4
	chunk := len(ids) / concurrency

	start := time.Now()
	for g := 0; g < concurrency; g++ {
		from := g * chunk
		to := from + chunk
		if g == concurrency-1 {
			to = len(ids)
		}
		wg.Add(1)
		go func(idSlice []int64) {
			defer wg.Done()
			for _, id := range idSlice {
				opStart := time.Now()
				_, err := b.DB.Exec(
					`UPDATE loadtest_coremail_messages SET seen = 1, deleted = 0 WHERE id = $1`, id,
				)
				opLat := time.Since(opStart)
				if err != nil {
					t.Errorf("flag update id=%d: %v", id, err)
					return
				}
				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(opLat))
			}
		}(ids[from:to])
	}
	wg.Wait()
	elapsed := time.Since(start)
	avgLat := time.Duration(0)
	if totalOps > 0 {
		avgLat = time.Duration(totalLatency / totalOps)
	}
	t.Logf("flag-updates: driver=%s ops=%d avg-latency=%v wall=%v",
		b.Driver, totalOps, avgLat, elapsed.Round(time.Millisecond))

	if avgLat > 100*time.Millisecond {
		t.Errorf("FLAG_UPDATE_LATENCY_HIGH: driver=%s avg-latency=%v (want < 100ms)", b.Driver, avgLat)
	}
}

// --- internal helpers ---

func (b *benchEnv) bulkInsert(n int, t interface{ Logf(string, ...interface{}) }) error {
	total := int64(0)
	for batchStart := 0; batchStart < n; batchStart += b.BatchSize {
		end := batchStart + b.BatchSize
		if end > n {
			end = n
		}
		batch := end - batchStart
		placeholders := make([]string, 0, batch)
		args := make([]interface{}, 0, batch*9)
		for i := 0; i < batch; i++ {
			seq := batchStart + i
			mb := seq % b.Mailboxes
			folder := (seq / 100) % 5
			now := time.Now().UnixNano()
			switch b.Driver {
			case "postgres":
				pLen := len(args)
				placeholders = append(placeholders,
					fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
						pLen+1, pLen+2, pLen+3, pLen+4,
						pLen+5, pLen+6, pLen+7, pLen+8, pLen+9))
			default:
				placeholders = append(placeholders, "(?,?,?,?,?,?,?,?,?)")
			}
			args = append(args,
				fmt.Sprintf("bench-%d-%d", time.Now().UnixNano(), seq),
				mb, folder,
				synthSubject(seq),
				fmt.Sprintf("sender-%d@example.com", seq),
				fmt.Sprintf("recipient-%d@example.com", seq),
				now, int64(1024+seq%10240), now,
			)
		}
		stmt := fmt.Sprintf(
			`INSERT INTO loadtest_coremail_messages (message_id, mailbox_id, folder_id, subject, from_address, to_addresses, received_date, size_bytes, created_at) VALUES %s`,
			strings.Join(placeholders, ","),
		)
		if _, err := b.DB.Exec(stmt, args...); err != nil {
			return err
		}
		atomic.AddInt64(&total, int64(batch))
	}
	return nil
}
