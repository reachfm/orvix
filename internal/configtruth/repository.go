package configtruth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Source describes where a configuration value originated.
type Source string

const (
	SourceDefault Source = "default"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceDB      Source = "database"
)

// State describes the lifecycle state of a setting.
type State string

const (
	StateApplied         State = "applied"
	StatePending         State = "pending"
	StateRestartRequired State = "restart_required"
	StateImmutable       State = "immutable"
)

// Setting is the authoritative view of one configuration value.
type Setting struct {
	Key             string    `json:"key"`
	Section         string    `json:"section"`
	Type            string    `json:"type"`
	Source          Source    `json:"source"`
	State           State     `json:"state"`
	EffectiveValue  any       `json:"effective_value"`
	ConfiguredValue any       `json:"configured_value,omitempty"`
	PendingValue    any       `json:"pending_value,omitempty"`
	DefaultValue    any       `json:"default_value"`
	RestartRequired bool      `json:"restart_required"`
	Immutable       bool      `json:"immutable"`
	Secret          bool      `json:"secret"`
	Value           any       `json:"value,omitempty"`
	Version         int       `json:"version"`
	ValidationError string    `json:"validation_error,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MutationRequest is a request to change a setting.
type MutationRequest struct {
	Value   any    `json:"value"`
	Version int    `json:"version"`
	ActorID uint   `json:"actor_id"`
	Reason  string `json:"reason"`
}

// MutationResult reports the outcome of a mutation.
type MutationResult struct {
	Setting Setting `json:"setting"`
	Applied bool    `json:"applied"`
	State   State   `json:"state"`
}

// Repository is the persistence layer for the configuration truth model.
type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewRepository returns a configuration truth Repository.
func NewRepository(db *sql.DB) *Repository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

// EnsureSchema creates the configuration truth table.
func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_config_truth (
		id `+autoInc+`,
		key TEXT NOT NULL UNIQUE,
		section TEXT NOT NULL,
		type TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'default',
		state TEXT NOT NULL DEFAULT 'applied',
		effective_value TEXT NOT NULL DEFAULT '',
		configured_value TEXT NOT NULL DEFAULT '',
		pending_value TEXT NOT NULL DEFAULT '',
		default_value TEXT NOT NULL DEFAULT '',
		restart_required INTEGER NOT NULL DEFAULT 0,
		immutable INTEGER NOT NULL DEFAULT 0,
		secret INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		updated_at `+ts+` NOT NULL
	)`)
	return err
}

// Get returns one setting by key.
func (r *Repository) Get(ctx context.Context, key string) (*Setting, error) {
	var s Setting
	var effective, configured, pending, defaultVal string
	var restart, immutable, secret int
	err := r.db.QueryRowContext(ctx,
		`SELECT key, section, type, source, state, effective_value, configured_value, pending_value, default_value, restart_required, immutable, secret, version, updated_at
		FROM platform_config_truth WHERE key=?`, key).
		Scan(&s.Key, &s.Section, &s.Type, &s.Source, &s.State, &effective, &configured, &pending, &defaultVal, &restart, &immutable, &secret, &s.Version, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.EffectiveValue = unmarshalValue(effective)
	s.ConfiguredValue = unmarshalValue(configured)
	s.PendingValue = unmarshalValue(pending)
	s.DefaultValue = unmarshalValue(defaultVal)
	s.RestartRequired = restart == 1
	s.Immutable = immutable == 1
	s.Secret = secret == 1
	if s.Secret {
		s.Value = "REDACTED"
	} else {
		s.Value = s.EffectiveValue
	}
	return &s, nil
}

// List returns all settings.
func (r *Repository) List(ctx context.Context) ([]Setting, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT key, section, type, source, state, effective_value, configured_value, pending_value, default_value, restart_required, immutable, secret, version, updated_at
		FROM platform_config_truth ORDER BY key ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var s Setting
		var effective, configured, pending, defaultVal string
		var restart, immutable, secret int
		if err := rows.Scan(&s.Key, &s.Section, &s.Type, &s.Source, &s.State, &effective, &configured, &pending, &defaultVal, &restart, &immutable, &secret, &s.Version, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.EffectiveValue = unmarshalValue(effective)
		s.ConfiguredValue = unmarshalValue(configured)
		s.PendingValue = unmarshalValue(pending)
		s.DefaultValue = unmarshalValue(defaultVal)
		s.RestartRequired = restart == 1
		s.Immutable = immutable == 1
		s.Secret = secret == 1
		if s.Secret {
			s.Value = "REDACTED"
		} else {
			s.Value = s.EffectiveValue
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Upsert inserts or updates a setting.
func (r *Repository) Upsert(ctx context.Context, s *Setting) error {
	now := time.Now().UTC()
	s.UpdatedAt = now
	effective := marshalValue(s.EffectiveValue)
	configured := marshalValue(s.ConfiguredValue)
	pending := marshalValue(s.PendingValue)
	defaultVal := marshalValue(s.DefaultValue)
	restart := 0
	if s.RestartRequired {
		restart = 1
	}
	immutable := 0
	if s.Immutable {
		immutable = 1
	}
	secret := 0
	if s.Secret {
		secret = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_config_truth (key, section, type, source, state, effective_value, configured_value, pending_value, default_value, restart_required, immutable, secret, version, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			section=excluded.section, type=excluded.type, source=excluded.source, state=excluded.state,
			effective_value=excluded.effective_value, configured_value=excluded.configured_value,
			pending_value=excluded.pending_value, default_value=excluded.default_value,
			restart_required=excluded.restart_required, immutable=excluded.immutable, secret=excluded.secret,
			version=excluded.version, updated_at=excluded.updated_at`,
		s.Key, s.Section, s.Type, s.Source, s.State, effective, configured, pending, defaultVal, restart, immutable, secret, s.Version, s.UpdatedAt)
	return err
}

// Mutate applies a change with optimistic concurrency.
func (r *Repository) Mutate(ctx context.Context, key string, req MutationRequest, applied bool) (*Setting, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current Versioned
	if err := tx.QueryRowContext(ctx,
		`SELECT key, section, type, source, state, effective_value, configured_value, pending_value, default_value, restart_required, immutable, secret, version, updated_at
		FROM platform_config_truth WHERE key=?`, key).
		Scan(&current.Key, &current.Section, &current.Type, &current.Source, &current.State, &current.EffectiveValue, &current.ConfiguredValue, &current.PendingValue, &current.DefaultValue, &current.RestartRequired, &current.Immutable, &current.Secret, &current.Version, &current.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			// Setting not yet persisted; the service should have upserted
			// field metadata first. Return an error if still missing.
			return nil, errors.New("setting metadata not found; call upsert first")
		}
		return nil, err
	}
	immutable := 0
	if current.Immutable == 1 {
		immutable = 1
	}
	if immutable == 1 {
		return nil, errors.New("setting is immutable")
	}
	if req.Version != 0 && req.Version != current.Version {
		return nil, errors.New("stale version: please retry")
	}
	newVersion := current.Version + 1
	now := time.Now().UTC()
	newState := StateApplied
	newEffective := current.EffectiveValue
	newConfigured := marshalValue(req.Value)
	newPending := current.PendingValue
	if applied {
		newEffective = marshalValue(req.Value)
		newPending = ""
	} else {
		newPending = marshalValue(req.Value)
		newState = StatePending
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE platform_config_truth SET state=?, effective_value=?, configured_value=?, pending_value=?, version=?, updated_at=?
		WHERE id=(SELECT id FROM platform_config_truth WHERE key=?)`,
		newState, newEffective, newConfigured, newPending, newVersion, now, key)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, key)
}

// Versioned is the raw row representation.
type Versioned struct {
	Key             string
	Section         string
	Type            string
	Source          string
	State           string
	EffectiveValue  string
	ConfiguredValue string
	PendingValue    string
	DefaultValue    string
	RestartRequired int
	Immutable       int
	Secret          int
	Version         int
	UpdatedAt       time.Time
}

func marshalValue(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalValue(s string) any {
	if s == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}
