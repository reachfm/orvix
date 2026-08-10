package domainlifecycle

import "errors"

var (
	ErrDomainNotFound    = errors.New("domain not found")
	ErrInvalidTransition = errors.New("invalid domain state transition")
	ErrVersionConflict   = errors.New("domain was modified concurrently")
	ErrDomainNameTaken   = errors.New("domain name already registered")
)
