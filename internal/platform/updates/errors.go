package updates

import "errors"

var (
	ErrUnsigned          = errors.New("update artifact manifest is unsigned")
	ErrInvalidSignature  = errors.New("update artifact manifest signature is invalid")
	ErrHashMismatch      = errors.New("update artifact hash does not match the signed manifest (tampered artifact)")
	ErrVersionMismatch   = errors.New("update artifact version does not match the expected next version")
	ErrPlatformMismatch  = errors.New("update artifact platform/arch does not match this deployment")
	ErrRecordNotFound    = errors.New("staged update record not found")
	ErrInvalidTransition = errors.New("update record is not in a state that allows this operation")
	ErrNoCoordinator     = errors.New("no update-apply coordinator is installed; artifact remains staged")
)
