// Command seed-fixture creates one tenant + one user directly in a SQLite
// database file, before or shortly after the orvix server that owns it has
// created the schema. It exists ONLY for test/playwright/portal.spec.ts's
// portal fixtures and is never invoked in production.
//
// Rationale: the E2E harness needs two kinds of tenant identities that the
// public API cannot create in its hermetic environment:
//
//   - a tenant_admin (the canonical Organization owner role that public
//     signup now assigns server-side) — seeded with role "tenant_admin";
//   - a RoleUser webmail end-user (which public signup must NEVER create,
//     because an Organization owner is tenant_admin) — seeded with role
//     "user" to prove /me fails closed for a mailbox end-user.
//
// This mirrors the same gap the Go test suite already works around
// hermetically (internal/api/router_test.go, cmd/orvix/admin_recovery_test.go
// both seed role rows directly into their own disposable, ephemeral SQLite
// test databases via direct SQL INSERT before the server under test ever
// reads them). This program does the identical thing for the Playwright E2E
// harness's own temp-file SQLite database — never a live, production, or
// VPS database — using the SAME password-hashing function
// (auth.HashPassword) the real login path verifies against, so the seeded
// account behaves exactly like a normal production login.
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
	// usage: seed-fixture <sqlite-dsn> <email> <password> <tenant-name> [role]
	// role defaults to "tenant_admin"; pass "user" to seed a RoleUser
	// webmail end-user for fail-closed portal tests.
	if len(os.Args) != 5 && len(os.Args) != 6 {
		fmt.Fprintln(os.Stderr, "usage: seed-fixture <sqlite-dsn> <email> <password> <tenant-name> [role]")
		os.Exit(2)
	}
	dsn, email, password, tenantName := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	role := "tenant_admin"
	if len(os.Args) == 6 {
		role = os.Args[5]
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer db.Close()

	now := time.Now().UTC()

	// Reuse an existing tenant with the same domain when present (the
	// tenant-admin and RoleUser fixtures must live in the SAME tenant —
	// a second call with the same tenant-name seeds a second identity
	// into the first tenant instead of failing on the UNIQUE domain).
	domain := tenantName + ".test"
	var tenantID int64
	lookupErr := db.QueryRow(`SELECT id FROM tenants WHERE domain = ? AND deleted_at IS NULL`, domain).Scan(&tenantID)
	if lookupErr == sql.ErrNoRows {
		res, insertErr := db.Exec(
			`INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at)
			 VALUES (?, ?, ?, 'smb', 10, 500, ?, ?)`,
			tenantName, tenantName, domain, now, now,
		)
		if insertErr != nil {
			fmt.Fprintln(os.Stderr, "insert tenant:", insertErr)
			os.Exit(1)
		}
		tenantID, insertErr = res.LastInsertId()
		if insertErr != nil {
			fmt.Fprintln(os.Stderr, "tenant id:", insertErr)
			os.Exit(1)
		}
	} else if lookupErr != nil {
		fmt.Fprintln(os.Stderr, "lookup tenant:", lookupErr)
		os.Exit(1)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}

	if _, err := db.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, ?, ?, ?, ?, 1, 1)`,
		now, now, email, hash, role, tenantID,
	); err != nil {
		fmt.Fprintln(os.Stderr, "insert user:", err)
		os.Exit(1)
	}

	fmt.Println("OK")
}
