package kernel

import "strings"

// IsUniqueViolation reports whether err is a unique-constraint/index
// violation from either supported driver: modernc.org/sqlite (message
// contains "UNIQUE constraint failed") or pgx/lib-pq against PostgreSQL
// ("duplicate key value violates unique constraint", the standard text
// for SQLSTATE 23505). Mirrors the same portable string-match already
// established in internal/billing/setup.go's unexported isUniqueViolation
// — promoted here so every bounded context can translate a check-then-
// insert race's losing INSERT into a stable Conflict/AlreadyExists error
// instead of a raw 500, without each package re-deriving the same two
// driver-specific substrings.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint")
}
