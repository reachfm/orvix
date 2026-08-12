package configtruth

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Service is the configuration truth model service.
type Service struct {
	repo     *Repository
	defaults map[string]Field
	mu       sync.RWMutex
}

// Field describes a known configuration field.
type Field struct {
	Key             string
	Section         string
	Type            string
	RestartRequired bool
	Immutable       bool
	Secret          bool
}

// NewService returns a configuration truth model Service.
func NewService(repo *Repository) *Service {
	return &Service{
		repo:     repo,
		defaults: map[string]Field{},
	}
}

// RegisterField registers a known configuration field.
func (s *Service) RegisterField(f Field) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[f.Key] = f
}

// Get returns the authoritative view of one setting.
func (s *Service) Get(ctx context.Context, key string) (*Setting, error) {
	s.mu.RLock()
	f, known := s.defaults[key]
	s.mu.RUnlock()
	if !known {
		return nil, fmt.Errorf("unknown setting: %s", key)
	}
	stored, err := s.repo.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		// Not yet persisted; return default view.
		return s.defaultView(f), nil
	}
	if stored.Secret {
		stored.Value = "REDACTED"
	} else {
		stored.Value = stored.EffectiveValue
	}
	return stored, nil
}

// List returns all known settings in authoritative view.
func (s *Service) List(ctx context.Context) ([]Setting, error) {
	s.mu.RLock()
	keys := make([]string, 0, len(s.defaults))
	for k := range s.defaults {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	sort.Strings(keys)
	out := make([]Setting, 0, len(keys))
	for _, k := range keys {
		setting, err := s.Get(ctx, k)
		if err != nil {
			continue
		}
		out = append(out, *setting)
	}
	return out, nil
}

// Mutate validates and applies a mutation, returning the result.
func (s *Service) Mutate(ctx context.Context, key string, req MutationRequest) (*MutationResult, error) {
	s.mu.RLock()
	f, known := s.defaults[key]
	s.mu.RUnlock()
	if !known {
		return nil, fmt.Errorf("unknown setting: %s", key)
	}
	if f.Immutable {
		return nil, fmt.Errorf("setting %s is immutable", key)
	}
	if err := validateValue(f.Type, req.Value); err != nil {
		return nil, err
	}
	// Ensure field metadata is persisted before mutation.
	if err := s.upsertField(ctx, f); err != nil {
		return nil, err
	}
	applied := !f.RestartRequired
	result, err := s.repo.Mutate(ctx, key, req, applied)
	if err != nil {
		return nil, err
	}
	if result.Secret {
		result.Value = "REDACTED"
	} else {
		result.Value = result.EffectiveValue
	}
	state := StateApplied
	if !applied {
		state = StatePending
	}
	return &MutationResult{
		Setting: *result,
		Applied: applied,
		State:   state,
	}, nil
}

// upsertField ensures the field metadata is persisted.
func (s *Service) upsertField(ctx context.Context, f Field) error {
	existing, err := s.repo.Get(ctx, f.Key)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	now := time.Now().UTC()
	return s.repo.Upsert(ctx, &Setting{
		Key:             f.Key,
		Section:         f.Section,
		Type:            f.Type,
		Source:          SourceDefault,
		State:           StateApplied,
		DefaultValue:    nil,
		RestartRequired: f.RestartRequired,
		Immutable:       f.Immutable,
		Secret:          f.Secret,
		Version:         0,
		UpdatedAt:       now,
	})
}

// defaultView returns the default view for a field.
func (s *Service) defaultView(f Field) *Setting {
	now := time.Now().UTC()
	setting := &Setting{
		Key:             f.Key,
		Section:         f.Section,
		Type:            f.Type,
		Source:          SourceDefault,
		State:           StateApplied,
		EffectiveValue:  nil,
		DefaultValue:    nil,
		RestartRequired: f.RestartRequired,
		Immutable:       f.Immutable,
		Secret:          f.Secret,
		Version:         0,
		UpdatedAt:       now,
	}
	if f.Immutable {
		setting.State = StateImmutable
	}
	if f.Secret {
		setting.Value = "REDACTED"
	}
	return setting
}

// validateValue checks that the value matches the field's type.
func validateValue(typ string, v any) error {
	switch typ {
	case "bool":
		switch v.(type) {
		case bool:
			return nil
		default:
			return fmt.Errorf("field requires bool value")
		}
	case "int":
		switch v.(type) {
		case int, int64, float64:
			return nil
		default:
			return fmt.Errorf("field requires int value")
		}
	case "string":
		switch v.(type) {
		case string:
			return nil
		default:
			return fmt.Errorf("field requires string value")
		}
	}
	return nil
}
