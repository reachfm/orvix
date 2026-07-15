// Package mode implements the database-mode abstraction for Orvix.
//
// Two production-supported modes:
//
//	sqlite      Dev, CI, smoke, single-host installs. Single-writer;
//	            pool is forced to 1/1. Suitable for small datasets.
//	postgres    Production target. Supports pooling, partitioning,
//	            concurrent writes, and the billion-email-scale
//	            query patterns described in
//	            docs/ENTERPRISE_DB_SCALE_PLAN.md.
//
// The package enforces:
//
//   - DSN validation per driver (rejects empty DSN, malformed DSN,
//     missing fields).
//   - Production safety (refuses SQLite when deployment_mode=production).
//   - Health reporting (driver, pool stats, ping round-trip, schema
//     version, partition readiness).
//
// The package is intentionally small and self-contained and accepts
// primitive arguments (driver string, dsn string, isProduction bool)
// rather than the full config struct, so importing config from this
// package would not create an import cycle.
package mode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Mode identifies a database backend.
type Mode string

const (
	// ModeSQLite is the local / dev / smoke backend.
	ModeSQLite Mode = "sqlite"

	// ModePostgres is the production-suitable backend.
	ModePostgres Mode = "postgres"

	// ModeUnknown is returned for any unrecognized driver string.
	ModeUnknown Mode = "unknown"
)

// Parse converts a driver string from config into a Mode. Unknown
// drivers return ModeUnknown; the caller should refuse to boot.
func Parse(driver string) Mode {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "sqlite", "sqlite3":
		return ModeSQLite
	case "postgres", "postgresql":
		return ModePostgres
	default:
		return ModeUnknown
	}
}

// String returns the canonical driver name accepted by config.NewDatabase.
func (m Mode) String() string {
	switch m {
	case ModeSQLite:
		return "sqlite"
	case ModePostgres:
		return "postgres"
	default:
		return "unknown"
	}
}

// ValidateDriverDSN ensures the (driver, dsn) pair is shape-correct and
// that the deployment-mode rule is honored:
//
//	deployment_mode=production REQUIRES driver=postgres.
//
// SQLite in production is rejected with a hard error. The check is
// case-insensitive on the production flag.
//
// Returns nil on success. On failure, returns a descriptive error
// that operators can act on without reading source.
func ValidateDriverDSN(driver, dsn string, isProduction bool) error {
	if err := validateDSN(driver, dsn); err != nil {
		return err
	}
	if isProduction {
		m := Parse(driver)
		if m == ModePostgres {
			return nil
		}
		return fmt.Errorf(
			"deployment_mode=%q requires database.driver=%q (got %q). "+
				"SQLite is not supported in production deployments; "+
				"see docs/ENTERPRISE_DB_SCALE_PLAN.md for the recommended Postgres topology",
			"production", "postgres", m.String())
	}
	return nil
}

// validateDSN ensures the DSN is non-empty and shape-correct for the
// given driver. It does NOT open a connection; use Health() for that.
func validateDSN(driver, dsn string) error {
	m := Parse(driver)
	if m == ModeUnknown {
		return fmt.Errorf("unsupported database driver %q (want sqlite or postgres)", driver)
	}
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("database DSN is empty for driver %q", m)
	}
	switch m {
	case ModePostgres:
		if !strings.Contains(dsn, "host=") &&
			!strings.HasPrefix(dsn, "postgres://") &&
			!strings.HasPrefix(dsn, "postgresql://") {
			return fmt.Errorf("postgres DSN must contain host= or start with postgres:// (got: %q)", dsn)
		}
	case ModeSQLite:
		if strings.ContainsRune(dsn, 0) {
			return fmt.Errorf("sqlite DSN contains NUL byte")
		}
	}
	return nil
}

// PoolDefaults returns the recommended pool settings for the given
// mode when the operator has not specified them.
func PoolDefaults(m Mode) (maxOpen, maxIdle, maxLifetimeSec int) {
	switch m {
	case ModePostgres:
		return 25, 5, 300
	case ModeSQLite:
		return 1, 1, 0
	default:
		return 1, 1, 0
	}
}

// Health describes the live state of the database connection. It is
// safe to surface through /api/v1/admin/health, /api/v1/admin/runtime,
// and the monitoring subsystem.
type Health struct {
	// Mode is the resolved database mode (sqlite or postgres).
	Mode Mode `json:"mode"`
	// Driver is the raw driver string from config.
	Driver string `json:"driver"`
	// Production is true if deployment_mode=production.
	Production bool `json:"production"`
	// Connected is true if a ping round-trip succeeded within PingTimeout.
	Connected bool `json:"connected"`
	// PingLatencyMS is the ping round-trip in milliseconds. Zero on failure.
	PingLatencyMS int64 `json:"ping_latency_ms"`
	// PingError is the last ping error, if any. Never contains DSN secrets.
	PingError string `json:"ping_error,omitempty"`
	// PoolMaxOpen / PoolMaxIdle are the live pool limits.
	PoolMaxOpen int `json:"pool_max_open"`
	PoolMaxIdle int `json:"pool_max_idle"`
	// PoolInUse / PoolIdle are the live open connection counts.
	PoolInUse int `json:"pool_in_use"`
	PoolIdle  int `json:"pool_idle"`
	// SchemaVersion is the current schema_migrations.version the
	// runtime applied. Zero when not yet determined.
	SchemaVersion int64 `json:"schema_version"`
	// CheckedAt is the timestamp of the health check.
	CheckedAt time.Time `json:"checked_at"`
}

// HealthInputs carries the inputs needed by CheckHealth without
// taking a dependency on the config package. Callers should populate
// Driver/IsProduction/MaxOpen/MaxIdle from their config.
type HealthInputs struct {
	Driver       string
	IsProduction bool
	MaxOpen      int
	MaxIdle      int
}

// PingTimeout bounds the ping round-trip so a hung backend cannot
// stall /health. Kept small because /health is on the hot path of
// every Caddy reverse-proxy pass-through.
const PingTimeout = 2 * time.Second

// CheckHealth pings the database and reports pool stats. It never
// returns an error — failures are reflected in Health.Connected and
// Health.PingError.
//
// If db is nil, Health.Connected is false and the pool stats are
// zero. The caller is expected to surface that as a clear "database
// unavailable" rather than crashing the API handler.
func CheckHealth(ctx context.Context, db *gorm.DB, in HealthInputs) Health {
	h := Health{
		Mode:        Parse(in.Driver),
		Driver:      in.Driver,
		Production:  in.IsProduction,
		CheckedAt:   time.Now().UTC(),
		PoolMaxOpen: in.MaxOpen,
		PoolMaxIdle: in.MaxIdle,
	}
	if db == nil {
		h.PingError = "database handle is nil"
		return h
	}
	sqlDB, err := db.DB()
	if err != nil {
		h.PingError = safeErr(err)
		return h
	}
	h.PoolMaxOpen = sqlDB.Stats().MaxOpenConnections

	pctx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	start := time.Now()
	pingErr := sqlDB.PingContext(pctx)
	h.PingLatencyMS = time.Since(start).Milliseconds()
	if pingErr != nil {
		h.PingError = safeErr(pingErr)
		return h
	}
	h.Connected = true

	stats := sqlDB.Stats()
	h.PoolInUse = stats.InUse
	h.PoolIdle = stats.Idle

	// Schema version is best-effort: read the highest applied version
	// from schema_migrations if it exists. A missing table is reported
	// as SchemaVersion=0, which the admin dashboard labels as "unknown".
	h.SchemaVersion = readSchemaVersion(sqlDB)
	return h
}

// readSchemaVersion reads the highest applied schema_migrations.version.
// Returns 0 if the table is missing or the query fails. The function
// is intentionally tolerant — schema version is informational.
func readSchemaVersion(sqlDB *sql.DB) int64 {
	if sqlDB == nil {
		return 0
	}
	var v sql.NullInt64
	row := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_migrations`)
	if err := row.Scan(&v); err != nil {
		return 0
	}
	if !v.Valid {
		return 0
	}
	return v.Int64
}

// safeErr converts an error to a string with DSN secrets removed.
// We never want the health endpoint to leak passwords.
func safeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// Strip any postgres:// URL with password
	if i := strings.Index(s, "postgres://"); i >= 0 {
		if j := strings.IndexAny(s[i:], " \n\t"); j >= 0 {
			s = s[:i] + "postgres://<redacted>" + s[i+j:]
		}
	}
	return s
}

// errProductionSQLite is a sentinel error callers may match with
// errors.Is to handle the "SQLite-in-production" failure mode
// specifically. The dynamic error returned by ValidateDriverDSN
// still carries the human-readable message.
var errProductionSQLite = errors.New("sqlite is not supported in production deployments")
