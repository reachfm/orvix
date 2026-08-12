// Package kernel provides the small set of cross-cutting primitives every
// platform bounded context depends on: typed API errors, pagination,
// idempotency, a transactional outbox, a clock abstraction, ID generation,
// optimistic-concurrency helpers, soft-deletion helpers, and redaction
// helpers. It intentionally does NOT own any domain logic — bounded
// contexts under internal/platform/<context>/ import this package, never
// the other way around.
package kernel

import "fmt"

// ErrorCode is a stable, machine-readable identifier every typed API error
// carries. Clients (frontend, CLI, third-party API consumers) branch on
// this, never on the human-readable Message.
type ErrorCode string

const (
	ErrCodeNotFound         ErrorCode = "NOT_FOUND"
	ErrCodeConflict         ErrorCode = "CONFLICT"
	ErrCodeValidation       ErrorCode = "VALIDATION_FAILED"
	ErrCodeUnauthorized     ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden        ErrorCode = "FORBIDDEN"
	ErrCodeQuotaExceeded    ErrorCode = "QUOTA_EXCEEDED"
	ErrCodeStateTransition  ErrorCode = "INVALID_STATE_TRANSITION"
	ErrCodePreconditionFail ErrorCode = "PRECONDITION_FAILED" // optimistic-concurrency version mismatch
	ErrCodeIdempotencyReuse ErrorCode = "IDEMPOTENCY_KEY_REUSE_MISMATCH"
	ErrCodeUnavailable      ErrorCode = "UNAVAILABLE"
	ErrCodeInternal         ErrorCode = "INTERNAL"
)

// httpStatusByCode is the exact, single source of truth for HTTP status
// mapping. Handlers must translate a *Error through this table rather than
// re-deriving a status code themselves, so the mapping cannot drift.
var httpStatusByCode = map[ErrorCode]int{
	ErrCodeNotFound:         404,
	ErrCodeConflict:         409,
	ErrCodeValidation:       400,
	ErrCodeUnauthorized:     401,
	ErrCodeForbidden:        403,
	ErrCodeQuotaExceeded:    409,
	ErrCodeStateTransition:  409,
	ErrCodePreconditionFail: 412,
	ErrCodeIdempotencyReuse: 409,
	ErrCodeUnavailable:      503,
	ErrCodeInternal:         500,
}

// Error is the one typed error shape every service-layer function in
// internal/platform/* returns for expected failure modes. Handlers map it
// to an HTTP response via HTTPStatus(); they never forward a raw error's
// Error() string to a client (that string may carry a SQL/DSN/provider
// detail — see Redact in redact.go for the client-safe boundary).
type Error struct {
	Code    ErrorCode
	Message string            // safe to show a human operator; never a DSN/SQL/provider detail
	Fields  map[string]string // field -> reason, for ErrCodeValidation
	cause   error             // never exposed to the client; safe to log server-side
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap lets callers use errors.Is/errors.As against the underlying cause
// (e.g. a *sql.DB error) without ever leaking that cause to a client.
func (e *Error) Unwrap() error { return e.cause }

// HTTPStatus returns the exact status code a handler must use for this
// error. Unknown codes fail closed to 500 rather than guessing.
func (e *Error) HTTPStatus() int {
	if e == nil {
		return 200
	}
	if s, ok := httpStatusByCode[e.Code]; ok {
		return s
	}
	return 500
}

func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap attaches a server-side-only cause to a typed error. The cause is
// available to server logging via errors.Unwrap/errors.As but is never
// serialized in an API response.
func Wrap(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func ValidationError(fields map[string]string) *Error {
	return &Error{Code: ErrCodeValidation, Message: "one or more fields are invalid", Fields: fields}
}

func NotFound(resource string) *Error {
	return &Error{Code: ErrCodeNotFound, Message: resource + " not found"}
}

func Conflict(message string) *Error {
	return &Error{Code: ErrCodeConflict, Message: message}
}

func Forbidden(message string) *Error {
	return &Error{Code: ErrCodeForbidden, Message: message}
}

func QuotaExceeded(message string) *Error {
	return &Error{Code: ErrCodeQuotaExceeded, Message: message}
}

func InvalidStateTransition(from, to string) *Error {
	return &Error{Code: ErrCodeStateTransition, Message: fmt.Sprintf("cannot transition from %q to %q", from, to)}
}

// AsAPIError extracts a *Error from err, or synthesizes a redacted
// ErrCodeInternal error if err is not already typed. Handlers call this
// exactly once at the HTTP boundary so an un-typed repository/provider
// error (which may carry a raw SQL/DSN detail in its Error() string) can
// never reach a client response body.
func AsAPIError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if as, ok := err.(*Error); ok {
		e = as
	} else {
		e = &Error{Code: ErrCodeInternal, Message: "an internal error occurred", cause: err}
	}
	return e
}
