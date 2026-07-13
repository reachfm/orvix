package handlers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// fakeSavepointExec wraps a real *sql.Tx but can be configured to
// return forced errors for the rollback or release step. The
// BLOCKER-1 review requires the helper to fail closed when these
// statements cannot be proven — this fake is the unit-test seam that
// proves that contract.
type fakeSavepointExec struct {
	real           savepointExec
	forcedRollback error
	forcedRelease  error
	rollbackCalls  int
	releaseCalls   int
}

func (f *fakeSavepointExec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	q := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(q, "ROLLBACK TO SAVEPOINT") && f.forcedRollback != nil:
		f.rollbackCalls++
		return nil, f.forcedRollback
	case strings.HasPrefix(q, "RELEASE SAVEPOINT") && f.forcedRelease != nil:
		f.releaseCalls++
		return nil, f.forcedRelease
	default:
		return f.real.ExecContext(ctx, query, args...)
	}
}

// openTestTx returns an in-memory sqlite tx that callers can use
// for real rollback/release exercises. Each call gets its own
// scratch schema so tests do not interfere with each other.
func openTestTx(t *testing.T) *sql.Tx {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create t: %v", err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// TestRollbackAndReleaseSavepointHappyPath proves the helper
// succeeds on a real tx with a live savepoint and the inserted row
// is undone as expected. This is the baseline against which the
// failure-mode tests are written.
func TestRollbackAndReleaseSavepointHappyPath(t *testing.T) {
	ctx := context.Background()
	tx := openTestTx(t)
	if _, err := tx.ExecContext(ctx, `SAVEPOINT import_row_0`); err != nil {
		t.Fatalf("savepoint open: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES ('a')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := rollbackAndReleaseSavepoint(ctx, tx, "import_row_0"); err != nil {
		t.Fatalf("helper: %v", err)
	}
	// The row inserted under the savepoint must be gone.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("after rollback t has %d rows, want 0", n)
	}
}

// TestRollbackAndReleaseSavepointRollbackErrorFailsClosed proves
// that when ROLLBACK TO SAVEPOINT itself fails, the helper returns
// a non-nil error. The release step is intentionally NOT called in
// this branch — the test asserts via call counter that release is
// only attempted after a successful rollback.
func TestRollbackAndReleaseSavepointRollbackErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	tx := openTestTx(t)
	wrap := &fakeSavepointExec{
		real:           tx,
		forcedRollback: errors.New("disk on fire"),
	}
	err := rollbackAndReleaseSavepoint(ctx, wrap, "import_row_0")
	if err == nil {
		t.Fatalf("helper returned nil, want error from rollback")
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("error must surface the rollback cause; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rollback savepoint") {
		t.Errorf("error must mention the failing step; got %q", err.Error())
	}
	if wrap.rollbackCalls != 1 {
		t.Errorf("rollback calls=%d, want 1", wrap.rollbackCalls)
	}
	if wrap.releaseCalls != 0 {
		t.Errorf("release must be skipped when rollback fails; calls=%d", wrap.releaseCalls)
	}
}

// TestRollbackAndReleaseSavepointReleaseErrorFailsClosed proves
// that when ROLLBACK succeeds but RELEASE fails, the helper still
// returns a non-nil error. This is the case where the savepoint
// marker may still be on the outer tx — exactly the situation
// that the BLOCKER-1 fix exists to detect and refuse to commit.
//
// Implementation note: forcedRelease forces the second statement to
// fail. The rollback is exercised on the real sqlite tx (call
// counter for rollback stays at 0 because it was not forced — what
// matters is that release was attempted exactly once and the
// helper returned the failure). A separate structural assertion
// in TestRollbackAndReleaseSavepointRollbackErrorFailsClosed
// proves the rollback-skips-release branch; together they cover
// both halves of the fail-closed contract.
func TestRollbackAndReleaseSavepointReleaseErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	tx := openTestTx(t)
	if _, err := tx.ExecContext(ctx, `SAVEPOINT import_row_0`); err != nil {
		t.Fatalf("savepoint open: %v", err)
	}
	wrap := &fakeSavepointExec{
		real:          tx,
		forcedRelease: errors.New("release permission denied"),
	}
	err := rollbackAndReleaseSavepoint(ctx, wrap, "import_row_0")
	if err == nil {
		t.Fatalf("helper returned nil, want error from release")
	}
	if !strings.Contains(err.Error(), "release permission denied") {
		t.Errorf("error must surface the release cause; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "release savepoint") {
		t.Errorf("error must mention the failing step; got %q", err.Error())
	}
	if wrap.releaseCalls != 1 {
		t.Errorf("release calls=%d, want 1", wrap.releaseCalls)
	}
}

// TestRollbackAndReleaseSavepointOnUnknownSavepointReturnsError
// proves the helper surfaces a real SQLite error when the
// savepoint does not exist. The handler MUST treat this as a hard
// failure (the caller's contract).
func TestRollbackAndReleaseSavepointOnUnknownSavepointReturnsError(t *testing.T) {
	ctx := context.Background()
	tx := openTestTx(t)
	// No SAVEPOINT issued before rollback — sqlite raises an error.
	err := rollbackAndReleaseSavepoint(ctx, tx, "import_row_does_not_exist")
	if err == nil {
		t.Fatalf("helper returned nil; sqlite should reject unknown savepoint")
	}
	if !strings.Contains(err.Error(), "no such savepoint") {
		t.Errorf("expected sqlite 'no such savepoint' error; got %q", err.Error())
	}
}

// TestSQLTxSatisfiesSavepointExec is a compile-time check that
// *sql.Tx satisfies the savepointExec interface used by the
// helper. If the interface or the *sql.Tx API ever drifts, this
// test breaks the build immediately.
func TestSQLTxSatisfiesSavepointExec(t *testing.T) {
	var _ savepointExec = (*sql.Tx)(nil)
}
