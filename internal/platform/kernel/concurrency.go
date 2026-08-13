package kernel

import "database/sql"

// CheckVersionedUpdate turns a *sql.Result from an
// "UPDATE ... WHERE id = ? AND version = ?" statement into the correct
// typed outcome. Every platform mutation that must be concurrency-safe
// (organization status transitions, quota reservation, node placement)
// uses this exact pattern: the WHERE clause's version predicate is what
// makes two concurrent writers race safely — the loser's UPDATE affects 0
// rows instead of silently clobbering the winner's write.
func CheckVersionedUpdate(res sql.Result, resource string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return Wrap(ErrCodeInternal, "check rows affected", err)
	}
	if n == 0 {
		return NewError(ErrCodePreconditionFail, resource+" was modified by another request — reload and retry")
	}
	return nil
}

// CheckExistenceUpdate is CheckVersionedUpdate's sibling for updates that
// key only on id (no version column) — e.g. a state-machine transition
// guarded by "WHERE id = ? AND status = ?". Zero rows affected still means
// "someone else changed it first, or it doesn't exist" and must not be
// silently swallowed.
func CheckExistenceUpdate(res sql.Result, resource string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return Wrap(ErrCodeInternal, "check rows affected", err)
	}
	if n == 0 {
		return NotFound(resource)
	}
	return nil
}
