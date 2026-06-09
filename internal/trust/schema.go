package trust

// Tables returns DDL statements for trust persistence.
func Tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_lockouts (
			key TEXT PRIMARY KEY,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_trust_scores (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			target TEXT NOT NULL,
			score INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			UNIQUE(scope, target)
		)`,
	}
}
