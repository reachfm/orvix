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

	// Class H — Route lives under router.go:1123 admin group which does not
	// admit RoleTenantAdmin. Migrate when PR #58 splits the group.
	"internal/api/handlers/admin_domain_advanced_test.go":             "Class H: /admin/domains routes under legacy admin group",
	"internal/api/handlers/admin_mailing_public_folder_patch_test.go": "Class H: /admin/mailing-lists + /admin/public-folders under legacy admin group",
	"internal/api/handlers/domain_list_isolation_test.go":             "Class H: /api/v1/domains under legacy admin group",
	"internal/api/handlers/enterprise_mutation_smoke_test.go":         "Class H: TestAdminMailboxesRoute hits /api/v1/mailboxes under legacy admin group",
	"internal/api/handlers/mailbox_export_isolation_test.go":          "Class H: mailbox/domain export endpoints under legacy admin group",
	"internal/api/handlers/tenant_isolation_test.go":                  "Class H: cross-tenant isolation test hits /admin/* routes",
	"internal/api/handlers/tenant_isolation_aliases_groups_test.go":   "Class H: alias/group isolation under /admin/* routes",
	"internal/api/handlers/tenant_isolation_matrix_test.go":           "Class H: cross-tenant matrix hits /admin/* routes",

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

	// Class H — authentication-flow tests (not authorization); role
	// choice is incidental. Migrate to canonical helper when PR #58 lands.
	"internal/api/handlers/webmail_user_test.go":    "Class H: auth-flow test seeds a users row; role choice incidental",
	"internal/api/handlers/rehash_on_login_test.go": "Class H: password-rehash-on-login auth flow; role choice incidental",

	// Mixed-fixture file: also contains canonical seed calls; the raw
	// legacy insert here plants a tenant_id=0 row that intentionally
	// exercises unresolved-tenant behavior. Cannot use the helper because
	// the helper does not permit tenant_id=0 (zero-value uint).
	"internal/api/handlers/domain_list_isolation_test.go": "Class H: legacy admin with tenant_id=0 for unresolved-tenant test",

	// Class H (retained after Defect 1 re-audit) — handlers reference
	// h.tenantID(c), so canonical PSA with tenant_id NULL cannot reach
	// them; tenant_admin is not admitted by the top-level admin router
	// group. Retained until PR #58 splits the group.
	"internal/api/handlers/enterprise_admin_test.go": "Class H: CreateAccountClass/CreateDomainGroup/CreateMailingList handlers use h.tenantID(c) — need tenant-bound legacy identity",
	"internal/api/handlers/ops_layer_v2_test.go":    "Class H: enterprise ops-layer v2 routes are tenant-scoped via h.tenantID(c)",

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
