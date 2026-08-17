package handlers_test

// Static regression: fails if a new test file adopts the deprecated
// `role='admin'` fixture (via seedLegacyAdminForMigrationTest or via
// a raw INSERT INTO users with role='admin') without an explicit
// entry in the named allowlists below.
//
// This is the executable counterpart to
// docs/deployment/canonical-role-fixtures-allowlist.md. The prose doc
// classifies existing occurrences; this test enforces that the set does
// not silently grow. Every allowlist entry is a specific file (no
// directory-wide or wildcard exemptions).
//
// Post-PR #58 follow-up: when PR #58 splits the top-level admin route
// group into strict platform-only and tenant-admin sub-groups, most
// Class-H entries here should be removed one by one as their tests
// migrate to canonical `seedTenantAdmin` / `seedPlatformSuperAdmin`.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// legacyHelperCallers — files permitted to invoke
// seedLegacyAdminForMigrationTest[WithPassword]. Every entry MUST have a
// one-line justification comment. This list is intentionally file-scoped,
// not directory-scoped, so adding a new caller requires a review edit here.
var legacyHelperCallers = map[string]string{
	// Definition site — the helper file itself contains the function
	// definition, which the regex counts as a "caller".
	"internal/api/handlers/testhelpers_role_test.go": "Helper definition site",

	// MIGRATED to seedTenantAdminWithPassword — removed from allowlist:
	//   admin_domain_advanced_test.go
	//   admin_mailing_public_folder_patch_test.go
	//   domain_list_isolation_test.go
	//   enterprise_mutation_smoke_test.go
	//   mailbox_export_isolation_test.go
	//   tenant_isolation_test.go
	//   tenant_isolation_aliases_groups_test.go
	//   tenant_isolation_matrix_test.go

	// Class C — canonical role denial tests intentionally plant legacy
	// admin to prove the migration helper does not accidentally grant
	// canonical permissions.
	"internal/api/handlers/canonical_role_denial_test.go": "Class C: legacy fixture used in denial regression",
}

// rawLegacyUsersAdminInsert — files permitted to contain raw
// INSERT INTO users ... role ... 'admin' ... fixtures (not via the
// helper). Every entry MUST have a one-line justification. The list
// covers both genuine legacy fixtures and unavoidable false-positive
// pattern hits (e.g. a scratch-directory path segment named "admin"
// nearby an unrelated INSERT).
var rawLegacyUsersAdminInsert = map[string]string{
	// Class C+ — self-reference: this file contains the literal regex
	// pattern strings and would match itself.
	"internal/api/handlers/legacy_fixture_allowlist_test.go": "Static scanner self-reference (pattern source strings)",

	// webmail_user_test.go          → migrated to canonical tenant_admin
	// rehash_on_login_test.go       → migrated to canonical tenant_admin
	// domain_list_isolation_test.go → migrated to canonical tenant_admin (tenant_id=0)

	// Class H (retained after Defect 1 re-audit) — handlers reference
	// h.tenantID(c), so canonical PSA with tenant_id NULL cannot reach
	// them; tenant_admin is not admitted by the top-level admin router
	// group. Retained until PR #58 splits the group.
	// enterprise_admin_test.go: migrated to canonical tenant_admin (newEnterpriseRouter and malformed variant now seed tenants + tenant_admin).

	// MIGRATED after Defect 1 re-audit — no longer in allowlist:
	//   backups_test.go            → seedPlatformSuperAdminWithPassword (backup handlers have no tenantID)
	//   update_test.go             → seedPlatformSuperAdminWithPassword (updater handlers are platform-only)
	//   admin_queue_operations_test.go → seedPlatformSuperAdmin (PR#60's queueAdminGate is platform-only)

	// False positives — 'admin' is a filepath segment nearby an
	// unrelated INSERT (INSERT itself uses canonical role or 'user').
	// Kept in the allowlist so the pattern can stay broad; each entry
	// is annotated as a false positive for reviewer clarity.
	"internal/api/handlers/admin_mfa_test.go":                         "False positive: adminDir=\"admin\" path segment near INSERT that plants 'user' role",
	"internal/api/handlers/admin_domain_advanced_test.go":             "False positive: adminDir=\"admin\" path segment (also legacy helper caller — see legacyHelperCallers above)",
	"internal/api/handlers/admin_settings_test.go":                    "False positive: adminDir=\"admin\" path segment nearby seedPlatformSuperAdminWithPassword call",
	"internal/api/handlers/admin_mailing_public_folder_patch_test.go": "False positive: adminDir=\"admin\" path segment nearby INSERT that plants 'user' role",

	// Class C — Webmail migration-window regression fixtures intentionally
	// seed legacy 'admin' to prove (a) the strict role state machine
	// reconciles admin→tenant_admin atomically with a token_version bump
	// and (b) role-change token-revocation. See
	// TestWebmailLegacyAdminReconciliation and
	// TestWebmailGetOrCreateUserRoleChangeBumpsVersionAndRevokesToken.
	"internal/api/handlers/webmail_auth_revocation_test.go": "Class C: legacy 'admin' seed proves migration-window reconciliation + token revocation",

	// me_portal_role_test.go — TestCountAdmins_LegacyAdminRowsStillCounted
	// intentionally seeds a legacy 'admin' row to prove the migration
	// window: CountAdmins keeps counting pre-normalization admin rows until
	// the startup normalizer rewrites them to canonical roles.
	"internal/api/handlers/me_portal_role_test.go": "Class C: legacy 'admin' seed proves CountAdmins migration-window counting",
}

// helperCallPattern matches seedLegacyAdminForMigrationTest and its
// WithPassword variant. Word-boundary anchored so a partial-name new
// helper would not accidentally slip in.
var helperCallPattern = regexp.MustCompile(`\bseedLegacyAdminForMigrationTest(?:WithPassword)?\s*\(`)

// rawUsersInsertPattern matches an INSERT into the users table that
// carries the string literal 'admin' on the same statement. Kept
// intentionally loose so any refactor that keeps the shape is still
// caught; false positives are handled by the exclusion allowlist.
var rawUsersInsertPattern = regexp.MustCompile(`INSERT\s+INTO\s+users\b[\s\S]{0,400}?['\"]admin['\"]`)

func TestLegacyFixtureAllowlistIsExhaustive(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	handlersDir := filepath.Join(root, "internal", "api", "handlers")

	var unauthorisedHelperCallers []string
	var unauthorisedRawInserts []string

	err = filepath.Walk(handlersDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Repo-relative path with forward slashes for stable map keys
		// across Windows and Linux CI.
		rel = filepath.ToSlash(rel)

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)

		if helperCallPattern.MatchString(src) {
			if _, ok := legacyHelperCallers[rel]; !ok {
				unauthorisedHelperCallers = append(unauthorisedHelperCallers, rel)
			}
		}

		if rawUsersInsertPattern.MatchString(src) {
			if _, ok := rawLegacyUsersAdminInsert[rel]; !ok {
				unauthorisedRawInserts = append(unauthorisedRawInserts, rel)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", handlersDir, err)
	}

	// The static enforcement flips both ways: any allowlist entry that
	// no longer contains the pattern is stale and must be cleaned up.
	stale := stalenessCheck(t, root, legacyHelperCallers, helperCallPattern)
	staleRaw := stalenessCheck(t, root, rawLegacyUsersAdminInsert, rawUsersInsertPattern)

	if len(unauthorisedHelperCallers) > 0 {
		t.Errorf(
			"unauthorised seedLegacyAdminForMigrationTest[WithPassword] callers — add an entry to legacyHelperCallers or migrate to a canonical helper:\n  %s",
			strings.Join(unauthorisedHelperCallers, "\n  "),
		)
	}
	if len(unauthorisedRawInserts) > 0 {
		t.Errorf(
			"unauthorised raw INSERT INTO users ... 'admin' ... fixtures — add an entry to rawLegacyUsersAdminInsert or migrate to a canonical helper:\n  %s",
			strings.Join(unauthorisedRawInserts, "\n  "),
		)
	}
	if len(stale) > 0 {
		t.Errorf(
			"stale entries in legacyHelperCallers (file no longer contains the pattern — remove):\n  %s",
			strings.Join(stale, "\n  "),
		)
	}
	if len(staleRaw) > 0 {
		t.Errorf(
			"stale entries in rawLegacyUsersAdminInsert (file no longer contains the pattern — remove):\n  %s",
			strings.Join(staleRaw, "\n  "),
		)
	}
}

// stalenessCheck returns allowlist entries whose files either no longer
// exist or no longer contain the pattern. Prevents the allowlist from
// silently growing over time as files are cleaned up.
func stalenessCheck(t *testing.T, root string, allowlist map[string]string, pattern *regexp.Regexp) []string {
	t.Helper()
	var stale []string
	for rel := range allowlist {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			stale = append(stale, rel+" (file missing or unreadable)")
			continue
		}
		if !pattern.MatchString(string(data)) {
			stale = append(stale, rel+" (pattern absent)")
		}
	}
	return stale
}
