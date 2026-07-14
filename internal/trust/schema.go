package trust

import "github.com/orvix/orvix/internal/dbdialect"

// Tables returns SQLite trust-persistence DDL. The CoreMail runtime trust
// store is SQLite, so this back-compat entry point stays SQLite-shaped.
func Tables() []string {
	return TablesForDialect(dbdialect.FromDriver("sqlite"))
}

// TablesForDialect returns dialect-appropriate DDL for the trust persistence
// tables (coremail_lockouts, coremail_trust_scores).
//
// On PostgreSQL these tables are owned by models.MigrateAllPostgres, so this
// CREATE TABLE IF NOT EXISTS no-ops. It MUST NOT emit SQLite-only DDL
// (INTEGER PRIMARY KEY AUTOINCREMENT / DATETIME) to PostgreSQL, which is a
// parse-time syntax error near "AUTOINCREMENT" that was previously logged and
// swallowed ("trust schema migration failed, falling back to in-memory").
func TablesForDialect(d *dbdialect.Info) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_lockouts (
			key TEXT PRIMARY KEY,
			expires_at ` + d.TimestampType() + ` NOT NULL,
			created_at ` + d.TimestampType() + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_trust_scores (
			id ` + d.AutoIncrement() + `,
			scope TEXT NOT NULL,
			target TEXT NOT NULL,
			score INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			updated_at ` + d.TimestampType() + ` NOT NULL,
			UNIQUE(scope, target)
		)`,
	}
}
