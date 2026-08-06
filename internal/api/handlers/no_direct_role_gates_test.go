package handlers

// Static regression: fails if a new direct legacy-role comparison
// appears in production code outside the explicitly allowed
// normalization sites. Prevents regression of the queueAdminGate
// class of defect (raw `role == auth.RoleAdmin` style authz).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allowedRelPaths — files that legitimately name legacy role
// strings (the normalization layer and quarantined legacy
// packages). Paths are relative to the repo root, forward-slash.
var allowedRelPaths = map[string]bool{
	"internal/auth/auth.go":           true, // NormalizeRole — the one canonical name-them-here site
	"internal/adminapi/types.go":      true, // legacy adminapi package (build-tagged quarantine)
	"internal/adminapi/auth.go":       true, // legacy adminapi package (build-tagged quarantine)
	"internal/auth/rbac/rbac.go":      true, // RBAC rolePermissions map keyed by legacy role constants
	"internal/auth/rbac/rbac_test.go": true,
}

// normalizeRoleAllowlist — files permitted to call auth.NormalizeRole.
// NormalizeRole is a session-establishment / login-flow helper; it
// must not be invoked from request handlers to make authorization
// decisions (see PR #60 rationale: NormalizeRole silently maps
// deprecated aliases and, on a base where the RBAC map still grants
// broad perms to RoleAdmin, would restore the RoleAdmin escalation
// path this PR closes).
var normalizeRoleAllowlist = map[string]bool{
	"internal/auth/auth.go": true, // definition
	"cmd/orvix/main.go":     true, // bootstrap normalization
	// Any additional entry MUST be justified inline and MUST NOT be
	// under internal/api/handlers/ or any request-handler package.
}

// forbidden — patterns that identify a legacy-role authz gate.
// These are strict textual patterns: any hit outside allowedRelPaths
// is a regression.
var forbidden = []*regexp.Regexp{
	regexp.MustCompile(`role\s*==\s*auth\.RoleAdmin\b`),
	regexp.MustCompile(`role\s*==\s*auth\.RoleSuperAdmin\b`),
	regexp.MustCompile(`role\s*:=\s*auth\.RoleAdmin\b`),
	regexp.MustCompile(`==\s*"admin"\s*\|\|\s*[a-zA-Z_.]+\s*==\s*"superadmin"`),
	regexp.MustCompile(`==\s*"superadmin"\s*\|\|`),
}

// normalizeRoleCall — detects auth.NormalizeRole( invocation. Checked
// separately because it has its OWN allowlist (normalizeRoleAllowlist).
var normalizeRoleCall = regexp.MustCompile(`auth\.NormalizeRole\s*\(`)

func TestNoDirectLegacyRoleComparisonInProductionCode(t *testing.T) {
	// Walk up to the repo root: this test file lives at
	// internal/api/handlers, so ../../.. is the module root.
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	var violations []string
	walkDirs := []string{"internal", "cmd"}
	for _, top := range walkDirs {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err = filepath.Walk(base, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if allowedRelPaths[rel] {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			for lineNo, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				// NormalizeRole ban: independent of the legacy-role
				// gate check, calling auth.NormalizeRole outside the
				// enumerated allowlist is itself a violation.
				if !normalizeRoleAllowlist[rel] && normalizeRoleCall.MatchString(line) {
					violations = append(violations,
						rel+":"+itoa(lineNo+1)+"  "+strings.TrimSpace(line)+
							"   [pattern: auth.NormalizeRole( outside allowlist]")
				}
				// Migration-window pattern: a line that names BOTH the
				// canonical platform-super-admin role AND the legacy
				// alias is an explicit dual-accept, not a legacy-only
				// gate. Do not flag.
				if strings.Contains(line, "RolePlatformSuperAdmin") &&
					strings.Contains(line, "RoleSuperAdmin") {
					continue
				}
				for _, re := range forbidden {
					if re.MatchString(line) {
						violations = append(violations,
							rel+":"+itoa(lineNo+1)+"  "+strings.TrimSpace(line)+
								"   [pattern: "+re.String()+"]")
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("legacy role comparison detected in production code (%d hit(s)):\n  %s\n\n"+
			"To fix: use authrbac.HasPermission(role, PermXxx) or the canonical role\n"+
			"constants (auth.RolePlatformSuperAdmin / auth.RoleTenantAdmin). If a new\n"+
			"file must genuinely name a legacy role string (e.g. a normalization/migration\n"+
			"helper), add its relative path to allowedRelPaths in this test with a comment\n"+
			"explaining why.",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
