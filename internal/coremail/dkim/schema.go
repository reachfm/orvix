package dkim

// Tables returns DDL for DKIM configuration storage.
func Tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	}
}

// Indexes returns index DDL for DKIM tables.
func Indexes() []string {
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_dkim_domain ON coremail_dkim_config(domain)`,
	}
}
