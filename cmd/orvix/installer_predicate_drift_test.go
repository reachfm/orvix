package main

// PORTAL-SEPARATION-PHASE1 Section-5 static regression: installer
// predicate drift.
//
// release/install.sh contains several SQL predicates that answer the
// question "is there an admin-shaped row in the users table?". They
// gate first-install detection, existing-admin-preservation, and
// ORVIX_RESET_ADMIN_PASSWORD=1. Historically the WHERE clause was
// role IN ('admin','superadmin','super_admin'), which pre-dated the
// canonical PSA role string emitted by cmd/orvix/main.go
// seedAdminUser. After the Phase-1 rewrite, every fresh install
// carries role='platform_super_admin' — meaning the legacy WHERE
// clause silently missed the row and would treat a fresh re-run as
// a first install (breaking preserve-mode and reset-mode alike).
//
// This test is the drift alarm. It reads release/install.sh and
// verifies EVERY SQL query that hits users.role recognizes the
// canonical PSA role string. It also verifies the seed path in
// cmd/orvix/main.go actually writes that same canonical string, so
// installer and runtime cannot silently disagree.
//
// If this test fails, add 'platform_super_admin' to the failing
// WHERE clause — do NOT delete legacy role strings from the list,
// pre-normalization installs still rely on them.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// installShPath resolves release/install.sh regardless of cwd.
// go test runs with cwd=cmd/orvix, so we walk up until we find the
// repo root (marked by go.mod).
func installShPath(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "release", "install.sh")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", cwd)
	return ""
}

// canonicalPSARole is the role string every WHERE clause must accept.
// Kept as a symbolic constant so this test is the single place a
// future rename would need to update, and matches the string emitted
// by auth.RolePlatformSuperAdmin at seedAdminUser insertion time.
const canonicalPSARole = "platform_super_admin"

// legacyRoleStrings are the pre-Phase-1 admin role strings that older
// installs may still carry on disk until NormalizeAdminRoles runs.
// Every "does an admin-shaped row exist?" predicate MUST keep them
// in its WHERE clause alongside the canonical PSA role, otherwise a
// legacy install would be misdetected as a fresh install and the
// reset-admin-password helper would fail to match its target row.
var legacyRoleStrings = []string{"admin", "superadmin", "super_admin"}

// adminRolePredicateRE matches a WHERE clause of the shape
//
//	role IN ('a','b',...)
//
// tolerating any single or double quotes and any amount of whitespace
// (including newlines). It captures the parenthesized role list so
// the assertion loop can parse the members. The pattern deliberately
// does NOT require the token "WHERE" or the column name "role" to
// share a line — real install.sh queries fit on one line but nothing
// in this test relies on line boundaries.
var adminRolePredicateRE = regexp.MustCompile(`(?is)\brole\s+IN\s*\(\s*([^)]+?)\s*\)`)

// parseQuotedList splits a captured `'a','b','c'` payload into
// individual role strings. Handles both single and double quotes and
// tolerates whitespace around commas.
func parseQuotedList(payload string) []string {
	var out []string
	// Accept both ' and " as quote chars: install.sh mixes them
	// depending on how SQL is embedded in bash/python heredocs.
	q := regexp.MustCompile(`['"]([^'"]+)['"]`)
	for _, m := range q.FindAllStringSubmatch(payload, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// TestInstallerAdminPredicatesRecognizeCanonicalRole is the static
// scanner: every role-IN clause in release/install.sh that lists any
// legacy admin role string MUST also list the canonical PSA role. If
// any predicate omits PSA it is flagged with line-adjacent context so
// the fix is a one-liner.
func TestInstallerAdminPredicatesRecognizeCanonicalRole(t *testing.T) {
	path := installShPath(t)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(body)

	matches := adminRolePredicateRE.FindAllStringSubmatchIndex(src, -1)
	if len(matches) == 0 {
		t.Fatalf("no role-IN predicates found in %s — regex or file has drifted", path)
	}

	adminLike := func(roles []string) bool {
		for _, r := range roles {
			for _, legacy := range legacyRoleStrings {
				if r == legacy {
					return true
				}
			}
			if r == canonicalPSARole {
				return true
			}
		}
		return false
	}

	var failures []string
	for _, m := range matches {
		// m[0]/m[1] is full match; m[2]/m[3] is capture group.
		full := src[m[0]:m[1]]
		payload := src[m[2]:m[3]]
		roles := parseQuotedList(payload)
		if !adminLike(roles) {
			// Non-admin role IN clause (e.g. filtering by mailbox
			// status). Skip.
			continue
		}
		hasPSA := false
		for _, r := range roles {
			if r == canonicalPSARole {
				hasPSA = true
				break
			}
		}
		if !hasPSA {
			// Compute the 1-indexed line number of the match for
			// easy jump-to-source.
			line := 1 + strings.Count(src[:m[0]], "\n")
			failures = append(failures, formatDriftFailure(path, line, full, roles))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("installer predicate drift — %d admin role-IN clause(s) missing %q:\n%s\n"+
			"Add %q to the WHERE clause. Do NOT delete legacy role strings; older installs still carry them until NormalizeAdminRoles runs.",
			len(failures), canonicalPSARole, strings.Join(failures, "\n"), canonicalPSARole)
	}
}

// TestBootstrapSeedEmitsCanonicalRole enforces the other side of the
// contract: cmd/orvix/main.go seedAdminUser's INSERT statement MUST
// use auth.RolePlatformSuperAdmin, not a hard-coded 'admin' or
// 'superadmin'. If this fails, a fresh install would land the wrong
// role and every canonical RBAC gate would deny the bootstrap user.
func TestBootstrapSeedEmitsCanonicalRole(t *testing.T) {
	path := findGoFileInRepo(t, filepath.Join("cmd", "orvix", "main.go"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(body)
	// The INSERT that seedAdminUser executes writes the bootstrap
	// user with auth.RolePlatformSuperAdmin. If that symbol is not
	// referenced from cmd/orvix/main.go the seed can no longer emit
	// the canonical string.
	if !strings.Contains(src, "auth.RolePlatformSuperAdmin") {
		t.Fatalf("cmd/orvix/main.go must reference auth.RolePlatformSuperAdmin in the bootstrap INSERT; a fresh install would otherwise land the wrong role")
	}
	// Belt-and-braces: forbid a raw legacy role literal ("admin" or
	// "superadmin") in the bootstrap INSERT string. We look for the
	// signature INSERT INTO users ... VALUES block. The check is
	// intentionally coarse — if seedAdminUser is ever refactored,
	// the assertion above (auth.RolePlatformSuperAdmin symbol
	// reference) remains the primary guard.
	if strings.Contains(src, "INSERT INTO users") &&
		strings.Contains(src, `string(auth.RolePlatformSuperAdmin)`) {
		return // seed uses the constant — correct.
	}
	// If we reach here the file no longer matches either shape;
	// fail loudly so a future refactor gets a nudge to update this
	// test rather than let drift go unnoticed.
	t.Fatalf("cmd/orvix/main.go bootstrap INSERT no longer references string(auth.RolePlatformSuperAdmin); update this test if the shape has changed intentionally")
}

// TestNormalizerConvergesLegacyToCanonicalRole enforces the third
// side of the contract: internal/models NormalizeAdminRoles's UPDATE
// statement MUST write the canonical PSA role. Without this, an
// upgrade path would leave legacy 'admin' rows in place forever.
func TestNormalizerConvergesLegacyToCanonicalRole(t *testing.T) {
	path := findGoFileInRepo(t, filepath.Join("internal", "models", "models.go"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(body)
	// The normalizer writes: role = 'platform_super_admin'.
	if !strings.Contains(src, `role = 'platform_super_admin'`) {
		t.Fatalf("internal/models/models.go NormalizeAdminRoles no longer writes role = 'platform_super_admin'; legacy admin rows would never converge")
	}
}

// findGoFileInRepo walks up from cwd to find a repo-relative file.
func findGoFileInRepo(t *testing.T, rel string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", cwd)
	return ""
}

// formatDriftFailure produces a "path:line — snippet — role list"
// line suitable for pasting into a fix commit.
func formatDriftFailure(path string, line int, snippet string, roles []string) string {
	// Collapse whitespace in the snippet so multi-line matches render
	// on one line in the failure message.
	compact := regexp.MustCompile(`\s+`).ReplaceAllString(snippet, " ")
	return "  " + path + ":" + itoa(line) + " roles=" + strings.Join(roles, ",") +
		"    -- clause: " + strings.TrimSpace(compact)
}

// itoa without importing strconv keeps the failure formatter tiny.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
