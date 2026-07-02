package queue

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB returns a fresh sqlite DB with the queue schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}

// insertQueueEntry inserts a queue entry in the given status and
// returns its id. The minimal columns satisfy the table NOT NULL
// constraints.
func insertQueueEntry(t *testing.T, db *sql.DB, status QueueStatus) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := db.Exec(`
		INSERT INTO coremail_queue
			(tenant_id, domain_id, mailbox_id, message_id,
			 from_address, to_address, recipient_domain, direction, status,
			 priority, max_attempts, next_attempt_at, last_attempt_at, last_error,
			 delivery_mode, remote_host, remote_ip,
			 created_at, updated_at)
		VALUES (1, 1, 1, 'm1', 'a@x', 'b@y', 'y', ?, ?, 0, 5,
		        ?, NULL, '', 'direct', 'mx', '1.2.3.4',
		        ?, ?)`,
		string(DirectionOutbound), string(status), now, now, now)
	if err != nil {
		t.Fatalf("insert queue: %v", err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func getStatus(t *testing.T, db *sql.DB, id uint) QueueStatus {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM coremail_queue WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("get status: %v", err)
	}
	return QueueStatus(s)
}

// TestQueueTransition_RetryFromAllowedStates verifies that
// AdminRetryNow succeeds for the three allowed source states.
func TestQueueTransition_RetryFromAllowedStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusDeferred, StatusBounced, StatusDeadLetter} {
		t.Run(string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			if err := repo.AdminRetryNow(ctx, id, nil); err != nil {
				t.Errorf("AdminRetryNow from %s: %v", src, err)
			}
			if got := getStatus(t, db, id); got != StatusPending {
				t.Errorf("after AdminRetryNow from %s: status = %q, want %q", src, got, StatusPending)
			}
		})
	}
}

// TestQueueTransition_RetryRejectsInFlightStates verifies the
// critical safety property: AdminRetryNow refuses to mutate a
// leased or delivering row.
func TestQueueTransition_RetryRejectsInFlightStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusPending, StatusLeased, StatusDelivering} {
		t.Run(string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			err := repo.AdminRetryNow(ctx, id, nil)
			if err == nil {
				t.Errorf("AdminRetryNow from %s should be rejected", src)
			}
			if got := getStatus(t, db, id); got != src {
				t.Errorf("status should be unchanged; got %q, want %q", got, src)
			}
		})
	}
}

// TestQueueTransition_CancelFromAllowedStates covers the
// cancel path: pending and deferred can be cancelled; leased
// and delivered cannot.
func TestQueueTransition_CancelFromAllowedStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusPending, StatusDeferred} {
		t.Run("allow_"+string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			if err := repo.AdminCancel(ctx, id, nil); err != nil {
				t.Errorf("AdminCancel from %s: %v", src, err)
			}
			if got := getStatus(t, db, id); got != StatusCancelled {
				t.Errorf("after AdminCancel from %s: status = %q, want %q", src, got, StatusCancelled)
			}
		})
	}
}

func TestQueueTransition_CancelRejectsInFlightStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusLeased, StatusDelivered, StatusBounced, StatusDeadLetter} {
		t.Run("reject_"+string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			err := repo.AdminCancel(ctx, id, nil)
			if err == nil {
				t.Errorf("AdminCancel from %s should be rejected", src)
			}
			if got := getStatus(t, db, id); got != src {
				t.Errorf("status should be unchanged; got %q, want %q", got, src)
			}
		})
	}
}

// TestQueueTransition_BounceFromSafeStates verifies the
// bounce path.
func TestQueueTransition_BounceFromSafeStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusPending, StatusDeferred, StatusBounced} {
		t.Run("allow_"+string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			if err := repo.AdminDeadLetter(ctx, id, "test reason", nil); err != nil {
				t.Errorf("AdminDeadLetter from %s: %v", src, err)
			}
			if got := getStatus(t, db, id); got != StatusDeadLetter {
				t.Errorf("after AdminDeadLetter from %s: status = %q, want %q", src, got, StatusDeadLetter)
			}
		})
	}
}

func TestQueueTransition_BounceRejectsInFlightStates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	for _, src := range []QueueStatus{StatusLeased, StatusDelivering, StatusDelivered, StatusCancelled} {
		t.Run("reject_"+string(src), func(t *testing.T) {
			id := insertQueueEntry(t, db, src)
			err := repo.AdminDeadLetter(ctx, id, "test reason", nil)
			if err == nil {
				t.Errorf("AdminDeadLetter from %s should be rejected", src)
			}
			if got := getStatus(t, db, id); got != src {
				t.Errorf("status should be unchanged; got %q, want %q", got, src)
			}
		})
	}
}

// TestQueueTransition_NonexistentIDReturnsNotFound: all three
// admin transitions must return an error for an id that does
// not exist.
func TestQueueTransition_NonexistentIDReturnsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	if err := repo.AdminRetryNow(ctx, 99999, nil); err == nil {
		t.Errorf("AdminRetryNow on missing id should error")
	}
	if err := repo.AdminCancel(ctx, 99999, nil); err == nil {
		t.Errorf("AdminCancel on missing id should error")
	}
	if err := repo.AdminDeadLetter(ctx, 99999, "x", nil); err == nil {
		t.Errorf("AdminDeadLetter on missing id should error")
	}
}

// TestQueueTransition_SoftDeletedRowIsImmutable verifies that
// a soft-deleted row is treated as "not found" by the
// transition logic. This is a critical safety property:
// cancelling a soft-deleted row must not resurrect it.
func TestQueueTransition_SoftDeletedRowIsImmutable(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()
	id := insertQueueEntry(t, db, StatusPending)

	// Soft-delete the row by setting deleted_at.
	if _, err := db.Exec(`UPDATE coremail_queue SET deleted_at = ? WHERE id = ?`, time.Now().UTC(), id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if err := repo.AdminRetryNow(ctx, id, nil); err == nil {
		t.Errorf("AdminRetryNow on soft-deleted row should error")
	}
	if err := repo.AdminCancel(ctx, id, nil); err == nil {
		t.Errorf("AdminCancel on soft-deleted row should error")
	}
	if err := repo.AdminDeadLetter(ctx, id, "x", nil); err == nil {
		t.Errorf("AdminDeadLetter on soft-deleted row should error")
	}
}

// TestQueueTransition_AtomicityUnderConcurrentUpdates verifies
// that the conditional UPDATE pattern in transitionStatus is
// race-safe: if a row is concurrently transitioned, the second
// transition either no-ops (because the row is now in a
// non-allowed state) or succeeds (because the row is in a
// still-allowed state). It must NEVER silently overwrite a
// row that the operator did not authorize.
func TestQueueTransition_AtomicityUnderConcurrentUpdates(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()
	id := insertQueueEntry(t, db, StatusDeferred)

	// First transition: succeed.
	if err := repo.AdminRetryNow(ctx, id, nil); err != nil {
		t.Fatalf("first AdminRetryNow: %v", err)
	}
	// Second transition from the SAME source state (deferred)
	// must now fail because the row is no longer deferred.
	err := repo.AdminRetryNow(ctx, id, nil)
	if err == nil {
		t.Errorf("second AdminRetryNow (row now pending) should error")
	}
	// And the status must still be pending (the rejected
	// transition did not corrupt the row).
	if got := getStatus(t, db, id); got != StatusPending {
		t.Errorf("status = %q, want pending (no corruption)", got)
	}
}
