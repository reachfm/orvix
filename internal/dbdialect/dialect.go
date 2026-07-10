// Package dbdialect provides driver-aware SQL generation helpers for
// applications that must run against both SQLite and PostgreSQL.
//
// It does NOT wrap GORM. GORM-generated queries are already translated by
// the driver. This package is for raw database/sql strings that cannot be
// expressed through GORM.
package dbdialect

import (
	"database/sql"
	"fmt"
	"strings"
)

// Dialect identifies the SQL dialect in use.
type Dialect int

const (
	// SQLite dialect (modernc.org/sqlite).
	SQLite Dialect = iota
	// Postgres dialect.
	Postgres
)

// Info holds dialect-aware helpers.
type Info struct {
	Dialect Dialect
}

// Detect determines the dialect of db by running a small driver-specific probe.
// It prefers Postgres detection first because SELECT version() also works on
// SQLite but returns a version string without "PostgreSQL".
func Detect(db *sql.DB) (*Info, error) {
	if db == nil {
		return nil, fmt.Errorf("dbdialect: nil db")
	}
	var version string
	// PostgreSQL-specific function. This fails on SQLite.
	if err := db.QueryRow("SELECT version()").Scan(&version); err == nil {
		if strings.Contains(strings.ToLower(version), "postgresql") {
			return &Info{Dialect: Postgres}, nil
		}
	}
	// SQLite-specific function.
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err == nil {
		return &Info{Dialect: SQLite}, nil
	}
	return nil, fmt.Errorf("dbdialect: could not detect database dialect")
}

// FromDriver returns an Info for a known driver name.
func FromDriver(driver string) *Info {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql":
		return &Info{Dialect: Postgres}
	case "sqlite", "sqlite3":
		return &Info{Dialect: SQLite}
	default:
		return &Info{Dialect: SQLite}
	}
}

// IsPostgres reports whether the dialect is PostgreSQL.
func (d *Info) IsPostgres() bool { return d.Dialect == Postgres }

// Placeholder returns the n-th positional placeholder (1-indexed).
func (d *Info) Placeholder(n int) string {
	if d.Dialect == Postgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// Placeholders returns a comma-separated list of count placeholders.
func (d *Info) Placeholders(count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = d.Placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}

// Tuple returns an SQL tuple of the given arity using the correct
// placeholder style, e.g. "($1,$2,$3)" or "(?,?,?)".
func (d *Info) Tuple(arity int) string {
	parts := make([]string, arity)
	for i := 0; i < arity; i++ {
		parts[i] = d.Placeholder(i + 1)
	}
	return "(" + strings.Join(parts, ",") + ")"
}

// NowExpr returns the SQL expression for the current UTC timestamp.
func (d *Info) NowExpr() string {
	if d.Dialect == Postgres {
		return "NOW()"
	}
	return "datetime('now')"
}

// TimestampType returns the preferred timestamp column type.
func (d *Info) TimestampType() string {
	if d.Dialect == Postgres {
		return "TIMESTAMP"
	}
	return "DATETIME"
}

// BooleanType returns the preferred boolean column type.
func (d *Info) BooleanType() string {
	if d.Dialect == Postgres {
		return "BOOLEAN"
	}
	return "INTEGER"
}

// AutoIncrement returns the primary-key autoincrement clause.
func (d *Info) AutoIncrement() string {
	if d.Dialect == Postgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

// Upsert returns an upsert statement for the given table.
//
// columns are the column names being inserted.
// conflictColumns are the unique columns that may conflict.
// updateColumns are the columns to update on conflict (usually excluding
// the conflict columns and any create-time columns).
func (d *Info) Upsert(table string, columns, conflictColumns, updateColumns []string) string {
	var b strings.Builder
	colList := strings.Join(columns, ", ")
	phList := d.Placeholders(len(columns))
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s)", table, colList, phList)
	if d.Dialect == Postgres {
		conflict := strings.Join(conflictColumns, ", ")
		var sets []string
		for _, c := range updateColumns {
			sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", c, c))
		}
		if len(sets) > 0 {
			fmt.Fprintf(&b, " ON CONFLICT (%s) DO UPDATE SET %s", conflict, strings.Join(sets, ", "))
		} else {
			fmt.Fprintf(&b, " ON CONFLICT (%s) DO NOTHING", conflict)
		}
	} else {
		// SQLite: INSERT OR REPLACE updates the whole row.
		return fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)", table, colList, phList)
	}
	return b.String()
}

// CurrentTimestampFunction returns the dialect function name for now.
func (d *Info) CurrentTimestampFunction() string {
	if d.Dialect == Postgres {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}
