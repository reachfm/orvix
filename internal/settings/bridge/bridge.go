// Package bridge loads admin-supplied protocol settings
// from the admin_settings table at boot and applies them
// to the live *config.Config. The bridge does NOT
// restart listeners — listener port / bind address
// changes remain restart-required and the PATCH endpoint
// must surface that contract honestly.
//
// Runtime-applied settings fall into two classes:
//
//   - Hot-applied (no restart): security.password_min_len,
//     monitoring.disk_usage_warning_pct, outbound.prefer_ipv4,
//     dns.namecheap_enable_apply. The runtime reads these
//     from the auth / monitoring / dns / outbound packages,
//     so the bridge writes them through the canonical
//     setters / refresh paths the runtime already exposes.
//
//   - Restart-required: every listener.* port + bind,
//     coremail.smtp_*, coremail.imap_*, coremail.pop3_*,
//     coremail.jmap_*. The bridge records the pending
//     value so a fresh process picks it up; in-process
//     listeners do NOT rebind on the fly because the
//     brief explicitly bans that path.
//
// The bridge surfaces a per-protocol readiness matrix
// the admin panel uses to render "live" / "pending
// restart" badges. The matrix lives in the loaded
// in-memory copy the api handler reads through
// admin_settings/proto/:protocol — the bridge does not
// add a third source of truth.
package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

// Bridge is the boot-time loader. Construct once
// (cfg.Load completes) and call Apply once. The result
// is read-only from the PATCH endpoint's perspective.
type Bridge struct {
	db     *sql.DB
	cfg    *config.Config
	logger *zap.Logger

	mu       sync.Mutex
	loaded   map[string]any
	applied  []string
	pending  []string
	loadedAt time.Time
}

// New constructs a Bridge. The cfg argument is mutated
// in place for hot-applied settings only. restart-
// required settings are kept in the Bridge's "pending"
// list so the admin UI / observability can surface them.
func New(cfg *config.Config, db *sql.DB, logger *zap.Logger) *Bridge {
	return &Bridge{cfg: cfg, db: db, logger: logger, loaded: map[string]any{}}
}

// Apply reads every row in admin_settings and either
// applies it to the live config (hot) or records it as
// pending restart. The function is idempotent — call it
// from boot AND after every admin write to keep the
// in-memory copy in sync. The latter is hooked by the
// PATCH handler indirectly through SettingsStore.
func (b *Bridge) Apply(ctx context.Context) (summary Summary, err error) {
	if b.db == nil {
		return Summary{}, errors.New("bridge: nil db")
	}
	rows, err := b.db.QueryContext(ctx, `SELECT key, value, section, requires_restart
		FROM admin_settings
		ORDER BY key ASC`)
	if err != nil {
		return Summary{}, fmt.Errorf("bridge: load: %w", err)
	}
	defer rows.Close()

	b.mu.Lock()
	defer b.mu.Unlock()

	b.loaded = map[string]any{}
	b.applied = nil
	b.pending = nil
	b.loadedAt = time.Now().UTC()

	for rows.Next() {
		var (
			key, raw string
			section  string
			restart  int
		)
		if err := rows.Scan(&key, &raw, &section, &restart); err != nil {
			return Summary{}, err
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			// Reject malformed JSON silently — the
			// settings package's PATCH handler is the
			// only writer and we trust it to write
			// well-formed JSON. A bad row is logged and
			// skipped.
			if b.logger != nil {
				b.logger.Warn("bridge: skip malformed row",
					zap.String("key", key), zap.Error(err))
			}
			continue
		}
		b.loaded[key] = value
		if restart == 1 {
			b.pending = append(b.pending, key)
			continue
		}
		if err := b.applyHot(key, value); err != nil {
			if b.logger != nil {
				b.logger.Warn("bridge: hot-apply failed",
					zap.String("key", key), zap.Error(err))
			}
			b.pending = append(b.pending, key)
			continue
		}
		b.applied = append(b.applied, key)
	}
	return Summary{
		Loaded:      len(b.loaded),
		Applied:     len(b.applied),
		Pending:     len(b.pending),
		LoadedAt:    b.loadedAt,
		AppliedKeys: append([]string(nil), b.applied...),
		PendingKeys: append([]string(nil), b.pending...),
	}, rows.Err()
}

// Summary is the operator-visible result of one Apply
// call. It is also the shape the admin status
// endpoint uses for the "settings bridge" panel that
// tells operators which of their PATCHes took effect
// in-process vs which are pending a restart.
type Summary struct {
	Loaded      int       `json:"loaded"`
	Applied     int       `json:"applied"`
	Pending     int       `json:"pending"`
	LoadedAt    time.Time `json:"loaded_at"`
	AppliedKeys []string  `json:"applied_keys"`
	PendingKeys []string  `json:"pending_keys"`
}

// applyHot writes one hot-setting into the live cfg
// using the field-by-field setters the runtime exposes.
// Settings whose field path does not exist in cfg are
// silently skipped — that is the bridging contract
// every operator can rely on: an outdated PATCH remains
// pending forever rather than silently breaking.
func (b *Bridge) applyHot(key string, value any) error {
	if b.cfg == nil {
		return errors.New("nil cfg")
	}
	// We dispatch the handful of hot keys by exact
	// name. Anything not listed here is treated as
	// restart-required — the boot-time load will
	// already have routed it to b.pending because
	// "requires_restart" was set on the row. We do
	// not re-classify here; rewriting the live cfg
	// from cold paths is dangerous.
	switch key {
	case "security.password_min_length":
		n, err := coerceInt(value)
		if err != nil {
			return err
		}
		b.cfg.Auth.PasswordMinLen = n
	case "monitoring.disk_usage_warning_pct":
		n, err := coerceInt(value)
		if err != nil {
			return err
		}
		b.cfg.Monitoring.DiskUsageWarningPct = n
	case "monitoring.disk_usage_critical_pct":
		n, err := coerceInt(value)
		if err != nil {
			return err
		}
		b.cfg.Monitoring.DiskUsageCriticalPct = n
	case "monitoring.queue_depth_warning":
		n, err := coerceInt(value)
		if err != nil {
			return err
		}
		b.cfg.Monitoring.QueueDepthWarning = n
	case "monitoring.queue_depth_critical":
		n, err := coerceInt(value)
		if err != nil {
			return err
		}
		b.cfg.Monitoring.QueueDepthCritical = n
	case "outbound.prefer_ipv4":
		v, err := coerceBool(value)
		if err != nil {
			return err
		}
		b.cfg.Outbound.PreferIPv4 = v
	case "dns.namecheap_enable_apply":
		v, err := coerceBool(value)
		if err != nil {
			return err
		}
		b.cfg.DNS.NamecheapEnableApply = v
	default:
		// Anything else is treated as pending.
		return fmt.Errorf("not in hot-applied allowlist")
	}
	return nil
}

// coerceInt / coerceBool keep the allowlist type-safe.
func coerceInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case string:
		n, err := strconv.Atoi(x)
		return n, err
	}
	return 0, fmt.Errorf("not an int")
}

func coerceBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case float64:
		return x != 0, nil
	case int:
		return x != 0, nil
	case int64:
		return x != 0, nil
	case string:
		switch x {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
	}
	return false, fmt.Errorf("not a bool")
}

// Snapshot returns a copy of the summary that survives
// concurrent reloads. Used by the admin status endpoint.
func (b *Bridge) Snapshot() Summary {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Summary{
		Loaded:      len(b.loaded),
		Applied:     len(b.applied),
		Pending:     len(b.pending),
		LoadedAt:    b.loadedAt,
		AppliedKeys: append([]string(nil), b.applied...),
		PendingKeys: append([]string(nil), b.pending...),
	}
}
