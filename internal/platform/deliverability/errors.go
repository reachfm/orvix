package deliverability

import "errors"

var (
	ErrSuppressionNotFound  = errors.New("suppression not found")
	ErrSuppressionNotActive = errors.New("suppression is not active")
	ErrSuppressionActive    = errors.New("suppression is already active")
	ErrAddressSuppressed    = errors.New("address is suppressed")
	ErrInvalidSignalType    = errors.New("invalid signal type")
	ErrInvalidWindow        = errors.New("invalid time window")
	ErrInvalidEventFilter   = errors.New("invalid event filter")
)
