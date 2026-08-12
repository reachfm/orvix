package observability

import "errors"

var (
	ErrRuleNotFound    = errors.New("alert rule not found")
	ErrAlertNotFound   = errors.New("alert not found")
	ErrInvalidRule     = errors.New("invalid alert rule")
	ErrNotFiring       = errors.New("alert is not in a state that allows this action")
	ErrVersionConflict = errors.New("alert was modified concurrently")
)
