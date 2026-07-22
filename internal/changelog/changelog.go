package changelog

import "time"

type Entry struct {
	Version       string    `json:"version"`
	Date          time.Time `json:"date"`
	Added         []string  `json:"added"`
	Improved      []string  `json:"improved"`
	Fixed         []string  `json:"fixed"`
	Security      []string  `json:"security"`
	Compatibility string    `json:"compatibility"`
	Migration     string    `json:"migration"`
	Rollback      string    `json:"rollback"`
}

var Changelog = []Entry{
	{
		Version: "0.1.0",
		Date:    time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Added: []string{
			"Initial MVP foundation",
			"Project structure and config system",
			"Database layer with SQLite/PostgreSQL",
			"License engine with JWT RS256 validation",
			"Feature flags system with tier gating",
			"Stalwart integration skeletons",
			"REST API foundation (health, version, license, features)",
			"Auth system with Argon2id and JWT tokens",
			"Metrics with Prometheus",
			"Additive-only migration system",
			"Code watermarking",
		},
		Compatibility: "Requires Go 1.23+",
		Migration:     "Initial schema — additive only",
	},
}
