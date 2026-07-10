package dbdialect

import (
	"testing"
)

func TestFromDriver(t *testing.T) {
	cases := []struct {
		driver   string
		expected Dialect
	}{
		{"postgres", Postgres},
		{"postgresql", Postgres},
		{"sqlite", SQLite},
		{"sqlite3", SQLite},
		{"unknown", SQLite},
	}
	for _, tc := range cases {
		info := FromDriver(tc.driver)
		if info.Dialect != tc.expected {
			t.Errorf("FromDriver(%q) dialect=%v want %v", tc.driver, info.Dialect, tc.expected)
		}
	}
}

func TestPlaceholder(t *testing.T) {
	pg := &Info{Dialect: Postgres}
	if got := pg.Placeholder(3); got != "$3" {
		t.Errorf("postgres placeholder 3 = %q want $3", got)
	}
	sq := &Info{Dialect: SQLite}
	if got := sq.Placeholder(3); got != "?" {
		t.Errorf("sqlite placeholder 3 = %q want ?", got)
	}
}

func TestPlaceholders(t *testing.T) {
	pg := &Info{Dialect: Postgres}
	if got := pg.Placeholders(3); got != "$1, $2, $3" {
		t.Errorf("postgres placeholders = %q", got)
	}
	sq := &Info{Dialect: SQLite}
	if got := sq.Placeholders(3); got != "?, ?, ?" {
		t.Errorf("sqlite placeholders = %q", got)
	}
}

func TestNowExpr(t *testing.T) {
	pg := &Info{Dialect: Postgres}
	if got := pg.NowExpr(); got != "NOW()" {
		t.Errorf("postgres now = %q", got)
	}
	sq := &Info{Dialect: SQLite}
	if got := sq.NowExpr(); got != "datetime('now')" {
		t.Errorf("sqlite now = %q", got)
	}
}

func TestUpsert(t *testing.T) {
	pg := &Info{Dialect: Postgres}
	got := pg.Upsert("coremail_lockouts", []string{"key", "expires_at", "created_at"}, []string{"key"}, []string{"expires_at", "created_at"})
	want := "INSERT INTO coremail_lockouts (key, expires_at, created_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO UPDATE SET expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at"
	if got != want {
		t.Errorf("postgres upsert =\n%q\nwant\n%q", got, want)
	}

	sq := &Info{Dialect: SQLite}
	got = sq.Upsert("coremail_lockouts", []string{"key", "expires_at", "created_at"}, []string{"key"}, []string{"expires_at", "created_at"})
	want = "INSERT OR REPLACE INTO coremail_lockouts (key, expires_at, created_at) VALUES (?, ?, ?)"
	if got != want {
		t.Errorf("sqlite upsert =\n%q\nwant\n%q", got, want)
	}
}

func TestUpsertDoNothing(t *testing.T) {
	pg := &Info{Dialect: Postgres}
	got := pg.Upsert("coremail_lockouts", []string{"key", "expires_at"}, []string{"key"}, nil)
	want := "INSERT INTO coremail_lockouts (key, expires_at) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING"
	if got != want {
		t.Errorf("postgres upsert do nothing =\n%q\nwant\n%q", got, want)
	}
}
