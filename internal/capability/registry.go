// Package capability is a runtime capability registry that produces an
// auditable map of feature availability from actual registered services,
// routes, and runtime modules — never from a static list.
package capability

import (
	"sort"
	"sync"
)

// Availability describes whether a capability is usable.
type Availability string

const (
	Available    Availability = "available"
	Unavailable  Availability = "unavailable"
	Degraded     Availability = "degraded"
	NotInstalled Availability = "not_installed"
)

// Permission required to access this capability.
type Permission string

const (
	PermPlatformAdmin Permission = "platform_admin"
	PermTenantAdmin   Permission = "tenant_admin"
	PermAnyAuth       Permission = "any_authenticated"
	PermPublic        Permission = "public"
)

// Entry describes one registered capability.
type Entry struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Availability Availability `json:"availability"`
	Reason       string       `json:"reason,omitempty"`
	Version      string       `json:"version,omitempty"`
	Mutable      bool         `json:"mutable"`
	Permission   Permission   `json:"permission"`
	DependsOn    []string     `json:"depends_on,omitempty"`
}

// Registry is the thread-safe runtime capability registry.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// New returns an empty Registry.
func New() *Registry { return &Registry{entries: map[string]*Entry{}} }

// Register adds or replaces a capability entry.
func (r *Registry) Register(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Availability == "" {
		e.Availability = Available
	}
	r.entries[e.ID] = &e
}

// SetAvailability updates the availability of a registered capability.
func (r *Registry) SetAvailability(id string, a Availability, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok {
		e.Availability = a
		e.Reason = reason
	}
}

// Snapshot returns all registered capabilities sorted by ID.
func (r *Registry) Snapshot() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns one capability by ID.
func (r *Registry) Get(id string) (*Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, false
	}
	return e, true
}
