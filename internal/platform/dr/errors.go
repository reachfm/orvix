package dr

import "errors"

var (
	// ErrOperationInProgress means a backup or restore lease is
	// currently held by another caller/node — the operation must be
	// retried later, not queued or silently skipped.
	ErrOperationInProgress  = errors.New("a conflicting backup/restore operation is already in progress")
	ErrConfirmationRequired = errors.New("typed confirmation phrase is required for this operation")
)

// RestoreConfirmationPhrase is the typed-confirmation string a caller
// must present to CoordinatedRestore — a copy-paste-proof guard
// against triggering a restore by accident via a scripted or reused
// request.
const RestoreConfirmationPhrase = "RESTORE-THIS-BACKUP"
