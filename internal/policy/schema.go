package policy

// Tables returns DDL statements for policy persistence.
func Tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			mode INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL,
			UNIQUE(scope, target)
		)`,
	}
}

// Indexes returns index DDL statements.
func Indexes() []string {
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_policies_scope ON coremail_policies(scope, target)`,
	}
}
