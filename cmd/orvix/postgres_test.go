package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// parsePostgresDSN parses a PostgreSQL DSN in either keyword/value form
// (host=... port=...) or URL form (postgresql://...) using pgx.
func parsePostgresDSN(dsn string) (*pgconn.Config, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	return cfg, nil
}

// replaceDSNDatabase returns a new DSN identical to baseDSN but with the
// database name replaced by newDB. It works for both keyword and URL DSNs.
func replaceDSNDatabase(baseDSN, newDB string) (string, error) {
	if err := validateIdentifier(newDB); err != nil {
		return "", fmt.Errorf("invalid database name %q: %w", newDB, err)
	}

	cfg, err := parsePostgresDSN(baseDSN)
	if err != nil {
		return "", err
	}
	cfg.Database = newDB
	return buildKeywordDSN(cfg), nil
}

// buildKeywordDSN serializes a pgconn.Config to a keyword/value DSN.
// The output never includes the password when the caller should redact it;
// callers that need the password can append it separately or use the cfg.
func buildKeywordDSN(cfg *pgconn.Config) string {
	parts := []string{}
	if cfg.Host != "" {
		parts = append(parts, "host="+quoteDSNValue(cfg.Host))
	}
	if cfg.Port != 0 {
		parts = append(parts, "port="+strconv.Itoa(int(cfg.Port)))
	}
	if cfg.Database != "" {
		parts = append(parts, "dbname="+quoteDSNValue(cfg.Database))
	}
	if cfg.User != "" {
		parts = append(parts, "user="+quoteDSNValue(cfg.User))
	}
	if cfg.Password != "" {
		parts = append(parts, "password="+quoteDSNValue(cfg.Password))
	}
	for k, v := range cfg.RuntimeParams {
		if k == "dbname" || k == "user" || k == "password" || k == "host" || k == "port" {
			continue
		}
		parts = append(parts, k+"="+quoteDSNValue(v))
	}
	return strings.Join(parts, " ")
}

// quoteDSNValue quotes a keyword/value DSN component when necessary.
func quoteDSNValue(v string) string {
	if v == "" {
		return "''"
	}
	if strings.ContainsAny(v, " '\"") {
		v = strings.ReplaceAll(v, "\\", "\\\\")
		v = strings.ReplaceAll(v, "'", "\\'")
		return "'" + v + "'"
	}
	return v
}

// redactDSN returns a DSN string safe for logging (password removed).
func redactDSN(dsn string) string {
	cfg, err := parsePostgresDSN(dsn)
	if err != nil {
		// Fallback: scrub anything that looks like a password.
		return scrubDSNPassword(dsn)
	}
	cfg.Password = "***"
	return buildKeywordDSN(cfg)
}

// scrubDSNPassword is a best-effort fallback that removes password values
// from both keyword and URL DSNs.
func scrubDSNPassword(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err == nil && u.User != nil {
			u.User = url.UserPassword(u.User.Username(), "***")
			return u.String()
		}
	}
	parts := strings.Fields(dsn)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.HasPrefix(strings.ToLower(p), "password=") {
			out = append(out, "password=***")
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}

// findPGTool locates a PostgreSQL client tool using exec.LookPath.
func findPGTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s not found in PATH: %v", name, err)
	}
	return path
}

// generateTestDBName returns a safe, unique database name.
func generateTestDBName(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), fastRand())
}

// fastRand returns a small random integer from the monotonic clock to avoid
// collisions when two tests run in the same nanosecond.
func fastRand() int64 {
	return time.Now().UnixNano()
}

// TestParsePostgresDSNAndReplaceDB verifies DSN parsing and database-name
// replacement for both keyword/value and URL forms.
func TestParsePostgresDSNAndReplaceDB(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
	}{
		{
			name: "keyword",
			dsn:  "host=localhost port=5433 user=orvix password=secret dbname=orvix sslmode=disable",
		},
		{
			name: "url",
			dsn:  "postgresql://orvix:secret@localhost:5433/orvix?sslmode=disable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newDSN, err := replaceDSNDatabase(tc.dsn, "orvix_test_dst")
			if err != nil {
				t.Fatalf("replaceDSNDatabase: %v", err)
			}
			cfg, err := parsePostgresDSN(newDSN)
			if err != nil {
				t.Fatalf("parse new dsn: %v", err)
			}
			if cfg.Database != "orvix_test_dst" {
				t.Errorf("database = %q, want orvix_test_dst", cfg.Database)
			}
			if cfg.Host != "localhost" {
				t.Errorf("host = %q, want localhost", cfg.Host)
			}
			if cfg.Port != 5433 {
				t.Errorf("port = %d, want 5433", cfg.Port)
			}
			if cfg.User != "orvix" {
				t.Errorf("user = %q, want orvix", cfg.User)
			}
			if cfg.Password != "secret" {
				t.Errorf("password = %q, want secret", cfg.Password)
			}
			redacted := redactDSN(newDSN)
			if strings.Contains(redacted, "secret") {
				t.Errorf("redacted dsn leaked password: %s", redacted)
			}
		})
	}
}

// openPostgresConn connects to the postgres maintenance database using the
// configured server (DSN with database replaced by "postgres").
func openPostgresConn(t *testing.T, baseDSN string) *sql.DB {
	t.Helper()
	postgresDSN, err := replaceDSNDatabase(baseDSN, "postgres")
	if err != nil {
		t.Fatalf("build postgres maintenance dsn: %v", err)
	}
	db, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		t.Fatalf("open postgres maintenance connection: %v", err)
	}
	return db
}

// createTestDatabase creates a new database on the configured PostgreSQL server.
func createTestDatabase(t *testing.T, baseDSN, name string) {
	t.Helper()
	if err := validateIdentifier(name); err != nil {
		t.Fatalf("invalid test database name %q: %v", name, err)
	}
	db := openPostgresConn(t, baseDSN)
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "CREATE DATABASE "+quoteIdentifier(name)); err != nil {
		t.Fatalf("create database %q: %v", name, err)
	}
}

// dropTestDatabase terminates any open connections and drops the database.
// It logs cleanup errors but never masks an earlier test failure.
func dropTestDatabase(t *testing.T, baseDSN, name string) {
	t.Helper()
	if err := validateIdentifier(name); err != nil {
		t.Logf("dropTestDatabase: invalid test database name %q: %v", name, err)
		return
	}
	db := openPostgresConn(t, baseDSN)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	terminateSQL := `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1
		  AND pid <> pg_backend_pid()`
	if _, err := db.ExecContext(ctx, terminateSQL, name); err != nil {
		t.Logf("dropTestDatabase: terminate connections for %q: %v", name, err)
	}

	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdentifier(name)); err != nil {
		t.Logf("dropTestDatabase: drop database %q: %v", name, err)
	}
}
