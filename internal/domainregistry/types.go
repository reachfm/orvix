package domainregistry

import "time"

// DomainStatus represents the operational state of a domain.
type DomainStatus string

const (
	DomainActive    DomainStatus = "active"
	DomainSuspended DomainStatus = "suspended"
	DomainDisabled  DomainStatus = "disabled"
)

func (s DomainStatus) IsValid() bool {
	switch s {
	case DomainActive, DomainSuspended, DomainDisabled:
		return true
	default:
		return false
	}
}

// Domain is the central domain model used by all platform protocols.
type Domain struct {
	ID        uint         `json:"id"`
	Name      string       `json:"name"`
	Status    DomainStatus `json:"status"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// CreateDomainRequest is the input for creating a domain.
type CreateDomainRequest struct {
	Name string `json:"name"`
}

// UpdateDomainRequest is the input for updating a domain.
type UpdateDomainRequest struct {
	Name   *string       `json:"name,omitempty"`
	Status *DomainStatus `json:"status,omitempty"`
}
