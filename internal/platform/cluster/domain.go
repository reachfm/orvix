// Package cluster implements Feature 13 (Milestone 10): a real
// cluster control plane — node registry, heartbeat/liveness, cordon/
// drain operator commands, capability-aware work placement, and
// fenced service-ownership leases for split-brain-safe failover. A
// fresh install starts with exactly one self-registered node and
// behaves identically to a single-node deployment; nothing here
// pretends a single node is a cluster or fabricates additional nodes.
package cluster

import "time"

// NodeStatus is the liveness state derived from heartbeat recency
// (Alive/Suspect/Unavailable) combined with operator intent
// (Draining/Cordoned) — the two axes are independent: an operator can
// cordon a perfectly healthy node.
type NodeStatus string

const (
	NodeAlive       NodeStatus = "alive"
	NodeSuspect     NodeStatus = "suspect"
	NodeUnavailable NodeStatus = "unavailable"
	NodeDraining    NodeStatus = "draining"
	NodeCordoned    NodeStatus = "cordoned"
)

// Node is one registered cluster member.
type Node struct {
	ID           string     `json:"id"` // stable identity, caller-supplied at enrollment (e.g. hostname-derived UUID)
	Role         string     `json:"role"`
	Capabilities []string   `json:"capabilities"` // e.g. "smtp", "delivery_worker", "imap"
	Version      string     `json:"version"`
	Build        string     `json:"build"`
	Region       string     `json:"region,omitempty"`
	Zone         string     `json:"zone,omitempty"`
	Status       NodeStatus `json:"status"`
	// MaintenanceReason/MaintenanceUntil are set when Status is
	// Draining or Cordoned via an operator command — always paired
	// with a reason, never a bare state flip.
	MaintenanceReason string     `json:"maintenance_reason,omitempty"`
	MaintenanceUntil  *time.Time `json:"maintenance_until,omitempty"`
	LastHeartbeatAt   time.Time  `json:"last_heartbeat_at"`
	LeaseExpiresAt    time.Time  `json:"lease_expires_at"`
	RowVersion        int        `json:"-"` // optimistic-concurrency guard; never serialized
	CreatedAt         time.Time  `json:"created_at"`
}

// HasCapability reports whether a node advertises a given capability.
func (n Node) HasCapability(cap string) bool {
	for _, c := range n.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// IsPlaceable reports whether work may be scheduled on this node
// right now — the single predicate every placement decision must go
// through, so "which states allow placement" is defined in exactly
// one place.
func (n Node) IsPlaceable() bool {
	return n.Status == NodeAlive
}

// LeaseHolder is a fenced ownership record for one exclusively-owned
// service/resource (e.g. "smtp-listener:25", a specific cron job).
// FenceToken is monotonically increasing per resource — a stale
// holder (one that acquired an earlier token) can never successfully
// act as owner again even if its own lease row hasn't expired yet,
// because every mutation an owner performs must be conditioned on
// presenting the CURRENT fence token, and only the database's atomic
// compare-and-set can hand out a new one.
type LeaseHolder struct {
	ResourceKey string    `json:"resource_key"`
	NodeID      string    `json:"node_id"`
	FenceToken  int64     `json:"fence_token"`
	AcquiredAt  time.Time `json:"acquired_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
