package billing

import "errors"

var (
	ErrInvalidAmount    = errors.New("adjustment amount must be a positive integer number of minor units")
	ErrReasonRequired   = errors.New("a reason is required for a manual adjustment")
	ErrCurrencyMismatch = errors.New("adjustment currency does not match the tenant's existing balance currency")
)
