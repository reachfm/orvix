package modules

import (
	"fmt"
	"sort"
	"sync"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module is the interface every Orvix module must implement.
type Module interface {
	// ID returns a unique module identifier (e.g., "guardian-agent").
	ID() string

	// Version returns the semantic version of the module (e.g., "1.0.0").
	Version() string

	// Requires returns a list of module IDs this module depends on.
	Requires() []string

	// Init initializes the module with config and database access.
	Init(cfg *config.Config, db *gorm.DB) error

	// Start starts the module's background operations.
	Start() error

	// Stop gracefully stops the module.
	Stop() error

	// Migrate applies any additive database migrations.
	Migrate() error
}

// Registry manages all registered modules.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
	order   []string
	logger  *zap.Logger
}

// NewRegistry creates a new module registry.
func NewRegistry(logger *zap.Logger) *Registry {
	return &Registry{
		modules: make(map[string]Module),
		logger:  logger,
	}
}

// Register adds a module to the registry.
func (r *Registry) Register(mod Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := mod.ID()
	if _, exists := r.modules[id]; exists {
		return fmt.Errorf("module %s already registered", id)
	}

	r.modules[id] = mod
	r.order = append(r.order, id)

	r.logger.Info("module registered",
		zap.String("id", id),
		zap.String("version", mod.Version()),
	)

	return nil
}

// Get returns a module by ID.
func (r *Registry) Get(id string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mod, ok := r.modules[id]
	return mod, ok
}

// All returns all registered modules in registration order.
func (r *Registry) All() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Module, 0, len(r.order))
	for _, id := range r.order {
		if mod, ok := r.modules[id]; ok {
			result = append(result, mod)
		}
	}
	return result
}

// InitAll initializes all registered modules in dependency order.
func (r *Registry) InitAll(cfg *config.Config, db *gorm.DB) error {
	modules := r.All()
	sorted := r.topologicalSort(modules)

	for _, mod := range sorted {
		r.logger.Info("initializing module",
			zap.String("id", mod.ID()),
			zap.String("version", mod.Version()),
		)
		if err := mod.Init(cfg, db); err != nil {
			return fmt.Errorf("failed to init module %s: %w", mod.ID(), err)
		}
	}

	return nil
}

// MigrateAll runs migrations for all registered modules.
func (r *Registry) MigrateAll() error {
	for _, mod := range r.All() {
		if err := mod.Migrate(); err != nil {
			return fmt.Errorf("failed to migrate module %s: %w", mod.ID(), err)
		}
	}
	return nil
}

// StartAll starts all registered modules.
func (r *Registry) StartAll() error {
	for _, mod := range r.All() {
		if err := mod.Start(); err != nil {
			return fmt.Errorf("failed to start module %s: %w", mod.ID(), err)
		}
	}
	return nil
}

// StopAll gracefully stops all registered modules in reverse order.
func (r *Registry) StopAll() error {
	modules := r.All()
	for i := len(modules) - 1; i >= 0; i-- {
		if err := modules[i].Stop(); err != nil {
			r.logger.Error("failed to stop module",
				zap.String("id", modules[i].ID()),
				zap.Error(err),
			)
		}
	}
	return nil
}

// Version returns the registry version.
func (r *Registry) Version() string {
	return "1.0.0"
}

func (r *Registry) topologicalSort(modules []Module) []Module {
	graph := make(map[string][]string)
	for _, mod := range modules {
		graph[mod.ID()] = mod.Requires()
	}

	visited := make(map[string]bool)
	var sorted []Module
	moduleMap := make(map[string]Module)
	for _, mod := range modules {
		moduleMap[mod.ID()] = mod
	}

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, dep := range graph[id] {
			if _, ok := moduleMap[dep]; ok {
				visit(dep)
			}
		}
		if mod, ok := moduleMap[id]; ok {
			sorted = append(sorted, mod)
		}
	}

	ids := make([]string, 0, len(modules))
	for _, mod := range modules {
		ids = append(ids, mod.ID())
	}
	sort.Strings(ids)

	for _, id := range ids {
		visit(id)
	}

	return sorted
}
