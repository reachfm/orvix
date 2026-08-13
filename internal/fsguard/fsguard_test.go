package fsguard

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// H-8 confinement proofs. These run against real temporary directories on
// whatever host executes them, so the separator-boundary and symlink
// behaviour is exercised for real rather than asserted about.

func newTestGuard(t *testing.T) (*Guard, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "approved")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "nested.log"), []byte("nested"), 0o640); err != nil {
		t.Fatal(err)
	}
	// A sibling directory sharing the root's name prefix — the exact shape
	// the old strings.HasPrefix check let through.
	if err := os.MkdirAll(base+string(os.PathSeparator)+"approved-secrets", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "approved-secrets", "master.key"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := New(map[string]string{"logs": root})
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	return g, base
}

func TestResolve_FileInsideRootAllowed(t *testing.T) {
	g, _ := newTestGuard(t)
	res, err := g.Resolve("logs", "app.log")
	if err != nil {
		t.Fatalf("expected a file inside the root to resolve, got %v", err)
	}
	if res.Rel != "app.log" {
		t.Fatalf("expected relative path app.log, got %q", res.Rel)
	}
}

func TestResolve_NestedFileAllowed(t *testing.T) {
	g, _ := newTestGuard(t)
	if _, err := g.Resolve("logs", "sub/nested.log"); err != nil {
		t.Fatalf("expected a nested file to resolve, got %v", err)
	}
}

func TestResolve_RootItselfAllowed(t *testing.T) {
	g, _ := newTestGuard(t)
	res, err := g.Resolve("logs", "")
	if err != nil {
		t.Fatalf("expected the root itself to resolve, got %v", err)
	}
	if res.Rel != "." {
		t.Fatalf("expected relative path \".\" for the root, got %q", res.Rel)
	}
}

func TestResolve_UnknownRootRejected(t *testing.T) {
	g, _ := newTestGuard(t)
	if _, err := g.Resolve("nope", "app.log"); !errors.Is(err, ErrNoSuchRoot) {
		t.Fatalf("expected ErrNoSuchRoot, got %v", err)
	}
}

func TestResolve_TraversalRejected(t *testing.T) {
	g, _ := newTestGuard(t)
	for _, p := range []string{
		"../approved-secrets/master.key",
		"..",
		"sub/../../approved-secrets/master.key",
		`..\approved-secrets\master.key`,
		"sub/./../../etc/passwd",
	} {
		if _, err := g.Resolve("logs", p); !errors.Is(err, ErrPathEscapesRoot) {
			t.Fatalf("traversal %q must be rejected, got %v", p, err)
		}
	}
}

func TestResolve_AbsolutePathRejected(t *testing.T) {
	g, _ := newTestGuard(t)
	for _, p := range []string{"/etc/passwd", "/var/lib/orvix/orvix.db", `\windows\system32`} {
		if _, err := g.Resolve("logs", p); !errors.Is(err, ErrPathEscapesRoot) {
			t.Fatalf("absolute path %q must be rejected, got %v", p, err)
		}
	}
}

func TestResolve_DriveQualifiedPathRejected(t *testing.T) {
	g, _ := newTestGuard(t)
	for _, p := range []string{`C:\Windows\System32\config\SAM`, "D:/secrets.txt"} {
		if _, err := g.Resolve("logs", p); !errors.Is(err, ErrPathEscapesRoot) {
			t.Fatalf("drive-qualified path %q must be rejected, got %v", p, err)
		}
	}
}

func TestResolve_NulByteRejected(t *testing.T) {
	g, _ := newTestGuard(t)
	if _, err := g.Resolve("logs", "app.log\x00.txt"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("a NUL byte in the path must be rejected, got %v", err)
	}
}

// TestResolve_SiblingPrefixEscapeRejected is the direct regression for the
// H-8 prefix bug: "<base>/approved-secrets" shares a string prefix with the
// approved "<base>/approved" root but is NOT inside it.
func TestResolve_SiblingPrefixEscapeRejected(t *testing.T) {
	g, base := newTestGuard(t)
	// Confirm the fixture really is a sibling sharing the prefix.
	if _, err := os.Stat(filepath.Join(base, "approved-secrets", "master.key")); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := g.Resolve("logs", "../approved-secrets/master.key"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatal("a sibling directory sharing the root's name prefix must not be reachable")
	}
}

func TestWithinRoot_SiblingPrefixIsNotInside(t *testing.T) {
	root := filepath.Clean("/var/lib/orvix")
	sibling := filepath.Clean("/var/lib/orvix-secrets/master.key")
	if withinRoot(root, sibling) {
		t.Fatal("withinRoot must require a separator boundary; /var/lib/orvix-secrets is not under /var/lib/orvix")
	}
	inside := filepath.Join(root, "orvix.db")
	if !withinRoot(root, inside) {
		t.Fatal("a real child must be considered inside the root")
	}
	if !withinRoot(root, root) {
		t.Fatal("the root itself must be considered inside")
	}
}

func TestResolve_SymlinkFileEscapeRejected(t *testing.T) {
	g, base := newTestGuard(t)
	rootDir := filepath.Join(base, "approved")
	target := filepath.Join(base, "approved-secrets", "master.key")
	link := filepath.Join(rootDir, "innocent.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := g.Resolve("logs", "innocent.log"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatal("a symlink whose target is outside the root must be rejected")
	}
}

func TestResolve_SymlinkDirEscapeRejected(t *testing.T) {
	g, base := newTestGuard(t)
	rootDir := filepath.Join(base, "approved")
	link := filepath.Join(rootDir, "outside")
	if err := os.Symlink(filepath.Join(base, "approved-secrets"), link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := g.Resolve("logs", "outside/master.key"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatal("a symlinked directory pointing outside the root must be rejected")
	}
}

func TestResolve_SymlinkChainEscapeRejected(t *testing.T) {
	g, base := newTestGuard(t)
	rootDir := filepath.Join(base, "approved")
	hop := filepath.Join(base, "hop")
	if err := os.Symlink(filepath.Join(base, "approved-secrets"), hop); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if err := os.Symlink(hop, filepath.Join(rootDir, "chain")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := g.Resolve("logs", "chain/master.key"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatal("a symlink chain leaving the root must be rejected")
	}
}

// TestResolve_SymlinkInsideRootAllowed proves the guard is not simply
// banning symlinks: one that stays inside the root remains usable.
func TestResolve_SymlinkInsideRootAllowed(t *testing.T) {
	g, base := newTestGuard(t)
	rootDir := filepath.Join(base, "approved")
	if err := os.Symlink(filepath.Join(rootDir, "app.log"), filepath.Join(rootDir, "alias.log")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := g.Resolve("logs", "alias.log"); err != nil {
		t.Fatalf("a symlink that stays inside the root should resolve, got %v", err)
	}
}

func TestStatConfined_SpecialFileRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are not available on Windows")
	}
	g, base := newTestGuard(t)
	fifo := filepath.Join(base, "approved", "pipe")
	if err := makeFIFO(fifo); err != nil {
		t.Skipf("cannot create FIFO on this host: %v", err)
	}
	if _, _, err := g.StatConfined("logs", "pipe"); !errors.Is(err, ErrUnsupportedFileType) {
		t.Fatalf("a FIFO must be rejected as an unsupported file type, got %v", err)
	}
}

func TestNew_RejectsUnsafeRoots(t *testing.T) {
	if _, err := New(map[string]string{"bad": ""}); err == nil {
		t.Fatal("an empty root must be rejected")
	}
	if _, err := New(map[string]string{"bad": "relative/path"}); err == nil {
		t.Fatal("a relative root must be rejected")
	}
	if _, err := New(map[string]string{"bad": string(os.PathSeparator)}); err == nil {
		t.Fatal("the filesystem root must be rejected as a root")
	}
}

func TestNew_SkipsMissingRootsButRequiresOne(t *testing.T) {
	dir := t.TempDir()
	g, err := New(map[string]string{
		"present": dir,
		"missing": filepath.Join(dir, "does-not-exist"),
	})
	if err != nil {
		t.Fatalf("a missing root should be skipped, not fatal: %v", err)
	}
	if ids := g.RootIDs(); len(ids) != 1 || ids[0] != "present" {
		t.Fatalf("expected only the present root, got %v", ids)
	}
	if _, err := New(map[string]string{"missing": filepath.Join(dir, "nope")}); err == nil {
		t.Fatal("a guard with no resolvable root must fail closed")
	}
}
