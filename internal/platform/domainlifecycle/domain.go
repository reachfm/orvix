// Package domainlifecycle implements Feature 5's domain state machine:
// the 8-state lifecycle (pending, pending_dns, verifying, active,
// degraded, suspended, deleting, deleted, failed) that governs a
// customer domain from creation through deletion.
//
// This is deliberately separate from internal/domainregistry, which
// owns the simple 3-value (active/suspended/disabled) flag that mail
// protocol servers read on every connection — that table must stay
// fast and stable. This package owns the richer admin/provisioning
// lifecycle and, on each transition, projects the coarse protocol-
// facing flag via Sync so protocol servers see the right value without
// depending on this package's schema.
package domainlifecycle

import "time"

type State string

const (
	StatePending    State = "pending"
	StatePendingDNS State = "pending_dns"
	StateVerifying  State = "verifying"
	StateActive     State = "active"
	StateDegraded   State = "degraded"
	StateSuspended  State = "suspended"
	StateDeleting   State = "deleting"
	StateDeleted    State = "deleted"
	StateFailed     State = "failed"
)

func (s State) IsValid() bool {
	switch s {
	case StatePending, StatePendingDNS, StateVerifying, StateActive, StateDegraded, StateSuspended, StateDeleting, StateDeleted, StateFailed:
		return true
	default:
		return false
	}
}

// transitions is the allowlist of valid state changes. Any pair not
// listed here is rejected by Service.Transition — the state machine is
// closed, not permissive-by-default.
var transitions = map[State]map[State]bool{
	StatePending:    {StatePendingDNS: true, StateFailed: true, StateDeleting: true},
	StatePendingDNS: {StateVerifying: true, StateFailed: true, StateDeleting: true},
	StateVerifying:  {StateActive: true, StatePendingDNS: true, StateFailed: true, StateDeleting: true},
	StateActive:     {StateDegraded: true, StateSuspended: true, StateDeleting: true},
	StateDegraded:   {StateActive: true, StateSuspended: true, StateDeleting: true},
	StateSuspended:  {StateActive: true, StateDeleting: true},
	StateDeleting:   {StateDeleted: true, StateFailed: true},
	StateFailed:     {StatePendingDNS: true, StateDeleting: true},
	StateDeleted:    {},
}

func CanTransition(from, to State) bool {
	next, ok := transitions[from]
	if !ok {
		return false
	}
	return next[to]
}

type Domain struct {
	ID        uint
	TenantID  uint
	Name      string
	State     State
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}
