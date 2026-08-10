package retention

import "errors"

var (
	ErrLegalHoldActive      = errors.New("purge blocked: an active legal hold covers this scope")
	ErrHoldNotFound         = errors.New("legal hold not found")
	ErrConfirmationRequired = errors.New("typed confirmation phrase is required to execute a purge")
	ErrInvalidPolicy        = errors.New("invalid retention policy")
)

// PurgeConfirmationPhrase must be presented verbatim to ExecutePurge —
// a copy-paste-proof guard, same pattern as internal/platform/dr's
// restore confirmation.
const PurgeConfirmationPhrase = "PURGE-ELIGIBLE-DATA"
