package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Execution interface {
	TenantID() uint
	Heartbeat(context.Context) error
	SetProgress(context.Context, int) error
	CancellationRequested(context.Context) (bool, error)
}

type Handler func(context.Context, Execution, json.RawMessage) (json.RawMessage, error)
type PayloadValidator func(json.RawMessage) error

type Definition struct {
	Type           string
	Scope          Scope
	PayloadVersion int
	Timeout        time.Duration
	Validate       PayloadValidator
	Handle         Handler
}

type Registry struct {
	mu          sync.RWMutex
	definitions map[string]Definition
}

func NewRegistry() *Registry { return &Registry{definitions: map[string]Definition{}} }

func (r *Registry) Register(definition Definition) error {
	if definition.Type == "" || definition.Handle == nil || definition.Validate == nil || definition.PayloadVersion <= 0 || (definition.Scope != ScopeTenant && definition.Scope != ScopePlatform) {
		return fmt.Errorf("invalid automation job definition")
	}
	if definition.Timeout <= 0 {
		definition.Timeout = 5 * time.Minute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[definition.Type]; exists {
		return fmt.Errorf("automation job type already registered: %s", definition.Type)
	}
	r.definitions[definition.Type] = definition
	return nil
}

func (r *Registry) Lookup(jobType string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, ok := r.definitions[jobType]
	return definition, ok
}
