package deliverability

import "errors"

var (
	ErrSuppressionNotFound = errors.New("suppression not found")
	ErrAddressSuppressed   = errors.New("address is suppressed")
	ErrInvalidSignalType   = errors.New("invalid signal type")
	ErrInvalidWindow       = errors.New("invalid time window")
)
