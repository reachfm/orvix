// Command seed-fixture creates one tenant + one tenant_admin user directly
// in a SQLite database file, before or shortly after the orvix server that
// owns it has created the schema. It exists ONLY for
// test/playwright/portal.spec.ts's Organization-portal fixture and is never
// invoked in production.
//
// Rationale: as of this repository's current state, POST /api/v1/auth/signup
// always creates the signing-up user as canonical RoleUser (see
// internal/api/handlers/customer_auth.go), and there is no platformMW-gated
// "create organization" endpoint that could provision a tenant's first
// tenant_admin either — so there is currently no supported, non-SQL
// production API path to bootstrap the first tenant_admin of a brand-new
// tenant. This mirrors that same gap the Go test suite already works around
// hermetically (internal/api/router_test.go, cmd/orvix/admin_recovery_test.go
// both seed role rows directly into their own disposable, ephemeral SQLite
// test databases via direct SQL INSERT before the server under test ever
// reads them). This program does the identical thing for the Playwright E2E
// harness's own temp-file SQLite database — never a live, production, or
// VPS database — using the SAME password-hashing function
// (auth.HashPassword) the real login path verifies against, so the seeded
// account behaves exactly like a normal production tenant_admin login.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/orvix/orvix/internal/auth"
	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: seed-fixture <sqlite-dsn> <email> <password> <tenant-name>")
		os.Exit(2)
	}
	dsn, email, password, tenantName := os.Args[1], os.Args[2], os.Args[3], os.Args[4]

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer db.Close()

	now := time.Now().UTC()
	res, err := db.Exec(
		`INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at)
		 VALUES (?, ?, ?, 'smb', 10, 500, ?, ?)`,
		tenantName, tenantName, tenantName+".test", now, now,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "insert tenant:", err)
		os.Exit(1)
	}
	tenantID, err := res.LastInsertId()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tenant id:", err)
		os.Exit(1)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}

	if _, err := db.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, ?, ?, 'tenant_admin', ?, 1, 1)`,
		now, now, email, hash, tenantID,
	); err != nil {
		fmt.Fprintln(os.Stderr, "insert user:", err)
		os.Exit(1)
	}

	fmt.Println("OK")
}
