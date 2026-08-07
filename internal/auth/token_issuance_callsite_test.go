package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from the package working directory
// (internal/auth -> repository root).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// wd = .../internal/auth -> root = .../
	return filepath.Dir(filepath.Dir(wd))
}

// scanProductionFiles walks non-test Go files under root and returns their
// parsed ASTs keyed by repo-relative path.
func scanProductionFiles(t *testing.T, root string) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		out[path] = f
		return nil
	})
	return out
}

// callsInFile reports whether file calls fnName as an expression statement
// or in an assignment.
func callsInFile(f *ast.File, fnName string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == fnName {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestProductionLoginFlowsUseSnapshotIssuance verifies that the production
// login/MFA/webmail handlers call GenerateAccessTokenForUserWithJTI and do not
// directly call the low-level GenerateAccessTokenWithJTI.
func TestProductionLoginFlowsUseSnapshotIssuance(t *testing.T) {
	root := repoRoot(t)
	files := scanProductionFiles(t, root)

	handlersDir := filepath.Join(root, "internal", "api", "handlers")
	for _, name := range []string{"handlers.go", "admin_mfa.go", "webmail_auth.go"} {
		path := filepath.Join(handlersDir, name)
		f, ok := files[path]
		if !ok {
			t.Fatalf("%s not found in production scan", name)
		}
		if !callsInFile(f, "GenerateAccessTokenForUserWithJTI") {
			t.Errorf("%s must call GenerateAccessTokenForUserWithJTI", name)
		}
		if callsInFile(f, "GenerateAccessTokenWithJTI") {
			t.Errorf("%s must not call the low-level GenerateAccessTokenWithJTI", name)
		}
	}
}

// TestNoProductionDeprecatedRoleWrites verifies no production (non-test) file
// writes deprecated role literals admin/superadmin/operator/readonly in an
// INSERT/UPDATE statement.
func TestNoProductionDeprecatedRoleWrites(t *testing.T) {
	root := repoRoot(t)
	files := scanProductionFiles(t, root)

	// Migration normalization files are explicitly allowlisted.
	allowedMigration := []string{
		"internal/models/models.go",
	}
	deprecated := []string{"admin", "superadmin", "super_admin", "super-admin", "operator", "readonly"}

	for path, f := range files {
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		skip := false
		for _, m := range allowedMigration {
			if rel == m {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			val := strings.Trim(bl.Value, "`")
			val = strings.Trim(val, `"`)
			// Only flag an assignment writing an EXACT deprecated role.
			// Patterns: "role = '<dep>'", "role='<dep>'", "role = \"<dep>\"",
			// "VALUES (... '<dep>' ...)". Where predicates and canonical names
			// that merely CONTAIN a deprecated substring are ignored.
			for _, d := range deprecated {
				for _, pat := range []string{
					"role = '" + d + "'",
					"role='" + d + "'",
					"role = \"" + d + "\"",
					"role=\"" + d + "\"",
					", '" + d + "', ",
				} {
					if strings.Contains(val, pat) &&
						(strings.Contains(val, "INSERT") || strings.Contains(val, "UPDATE") || strings.Contains(val, "SET ")) {
						t.Errorf("%s: deprecated role write %q in %s", rel, d, bl.Value)
					}
				}
			}
			return true
		})
	}
}
