package security

import "errors"

var (
	ErrInvalidCategory = errors.New("invalid security event category")
	ErrDetailTooLong   = errors.New("event detail exceeds the maximum allowed length")
)

// MaxDetailLength bounds Event.Detail so a caller can never smuggle an
// entire message body or header dump into what's meant to be a short
// operator-facing summary.
const MaxDetailLength = 500
