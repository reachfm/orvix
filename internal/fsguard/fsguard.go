// Package fsguard provides strict filesystem confinement for operator-facing
// file browsing.
//
// It exists because the previous inline implementation (H-8) confined paths
// with a bare strings.HasPrefix against a cleaned root — with no separator
// boundary — so `/var/lib/orvix-secrets/...` passed the check for the
// `/var/lib/orvix` root, and it never resolved symlinks, so a link planted
// inside an approved root redirected reads to any file the service account
// could open.
//
// The contract here is deliberately narrow:
//
//   - The caller names an approved root by index/identifier; roots are
//     server-configured and never taken from the request.
//   - The caller supplies a RELATIVE path within that root. Absolute paths,
//     drive-qualified paths (Windows), and any ".." traversal are refused
//     outright rather than normalised into something that happens to land
//     inside the root.
//   - Confinement is verified AFTER symlink evaluation, and the comparison
//     requires either an exact root match or a root + separator prefix.
//   - Special files (devices, sockets, FIFOs) and non-regular files are
//     refused, so a read can never block on a FIFO or pull from /dev.
//   - Results are returned as root-relative paths; absolute host paths are
//     never handed back to a caller.
package fsguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	// ErrNoSuchRoot is returned when the requested root is not configured.
	ErrNoSuchRoot = errors.New("requested root is not an approved filesystem root")
	// ErrPathEscapesRoot is returned for absolute paths, drive-qualified
	// paths, traversal, or anything that resolves outside its root.
	ErrPathEscapesRoot = errors.New("path is outside the approved filesystem root")
	// ErrUnsupportedFileType is returned for devices, sockets, FIFOs, and
	// other non-regular files.
	ErrUnsupportedFileType = errors.New("unsupported file type")
	// ErrInvalidRoot is returned when a configured root is unusable.
	ErrInvalidRoot = errors.New("invalid approved filesystem root")
)

// Root is one approved, pre-resolved filesystem root.
type Root struct {
	// ID is the stable identifier a caller uses to select this root. It is
	// deliberately opaque so a request never carries a host path.
	ID string
	// abs is the absolute, cleaned, symlink-evaluated root directory.
	abs string
}

// ID-safe display of a root without leaking the host path.
func (r Root) String() string { return r.ID }

// Guard confines all access to a fixed set of approved roots.
type Guard struct {
	roots []Root
}

// New resolves each configured root and returns a Guard. A root that is
// empty, relative, or the filesystem root itself is rejected. Roots that do
// not exist on this host are skipped (a deployment may not have every
// directory), but at least one must resolve.
func New(specs map[string]string) (*Guard, error) {
	g := &Guard{}
	for id, dir := range specs {
		abs, err := resolveRoot(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidRoot, id, err)
		}
		g.roots = append(g.roots, Root{ID: id, abs: abs})
	}
	if len(g.roots) == 0 {
		return nil, fmt.Errorf("%w: no approved root resolved on this host", ErrInvalidRoot)
	}
	return g, nil
}

// resolveRoot validates and canonicalises one configured root.
func resolveRoot(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return "", errors.New("root is empty")
	}
	if !filepath.IsAbs(trimmed) {
		return "", errors.New("root must be absolute")
	}
	clean := filepath.Clean(trimmed)
	// Refuse a root-wide grant: "/" (or a bare volume root on Windows)
	// would make confinement meaningless.
	if clean == string(os.PathSeparator) || clean == filepath.VolumeName(clean)+string(os.PathSeparator) {
		return "", errors.New("root must not be the filesystem root")
	}
	// Evaluate symlinks so later comparisons are against the real path.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("root is not a directory")
	}
	return resolved, nil
}

// RootIDs lists the configured root identifiers.
func (g *Guard) RootIDs() []string {
	out := make([]string, 0, len(g.roots))
	for _, r := range g.roots {
		out = append(out, r.ID)
	}
	return out
}

func (g *Guard) lookupRoot(id string) (Root, error) {
	for _, r := range g.roots {
		if r.ID == id {
			return r, nil
		}
	}
	return Root{}, ErrNoSuchRoot
}

// validateRelative rejects any caller path that is not a plain relative path
// inside its root. This runs BEFORE any filesystem access, so a hostile path
// never reaches the OS.
func validateRelative(rel string) error {
	if rel == "" || rel == "." {
		return nil
	}
	if strings.ContainsRune(rel, 0) {
		return ErrPathEscapesRoot
	}
	// Absolute (POSIX or Windows) and drive-qualified paths are refused.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return ErrPathEscapesRoot
	}
	if filepath.VolumeName(rel) != "" {
		return ErrPathEscapesRoot
	}
	// UNC-ish or drive-letter forms that VolumeName may not catch when the
	// separator style does not match the host.
	if len(rel) >= 2 && rel[1] == ':' {
		return ErrPathEscapesRoot
	}
	// Reject traversal in either separator style, before and after cleaning.
	normalized := strings.ReplaceAll(rel, `\`, "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return ErrPathEscapesRoot
		}
	}
	return nil
}

// pathEqual compares two resolved paths, honouring case-insensitive
// filesystem semantics on hosts where that is the norm.
func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// withinRoot reports whether resolved is the root itself or lives beneath it.
// The separator boundary is what stops `/var/lib/orvix-secrets` matching the
// `/var/lib/orvix` root.
func withinRoot(root, resolved string) bool {
	if pathEqual(root, resolved) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.HasPrefix(strings.ToLower(resolved), strings.ToLower(prefix))
	}
	return strings.HasPrefix(resolved, prefix)
}

// Resolved is a successfully confined path.
type Resolved struct {
	// Abs is the absolute on-disk path. It is for server-side use only and
	// must never be echoed to a caller.
	Abs string
	// Rel is the root-relative path, safe to return in API responses.
	Rel string
	// RootID identifies which approved root it belongs to.
	RootID string
}

// Resolve confines rel within the approved root named by rootID.
//
// The returned path is guaranteed to be inside the root even in the presence
// of symlinks: confinement is checked against the symlink-evaluated path. For
// a path that does not exist yet, the nearest existing ancestor is evaluated
// instead, so a dangling name cannot be used to smuggle a link target in.
func (g *Guard) Resolve(rootID, rel string) (Resolved, error) {
	root, err := g.lookupRoot(rootID)
	if err != nil {
		return Resolved{}, err
	}
	if err := validateRelative(rel); err != nil {
		return Resolved{}, err
	}

	joined := filepath.Join(root.abs, filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")))
	// filepath.Join already cleans; re-assert containment on the lexical form
	// before touching the filesystem.
	if !withinRoot(root.abs, joined) {
		return Resolved{}, ErrPathEscapesRoot
	}

	resolved, err := evalSymlinksAllowingMissing(joined)
	if err != nil {
		return Resolved{}, err
	}
	// The decisive check: after following every link, are we still inside?
	if !withinRoot(root.abs, resolved) {
		return Resolved{}, ErrPathEscapesRoot
	}

	relOut, err := filepath.Rel(root.abs, resolved)
	if err != nil {
		return Resolved{}, ErrPathEscapesRoot
	}
	return Resolved{Abs: resolved, Rel: filepath.ToSlash(relOut), RootID: root.ID}, nil
}

// evalSymlinksAllowingMissing resolves symlinks for a path whose final
// components may not exist yet, by resolving the deepest existing ancestor
// and re-appending the remainder.
func evalSymlinksAllowingMissing(path string) (string, error) {
	remainder := ""
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remainder), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// StatConfined resolves rel and stats it WITHOUT following a final symlink,
// refusing anything that is not a regular file or directory.
func (g *Guard) StatConfined(rootID, rel string) (Resolved, os.FileInfo, error) {
	res, err := g.Resolve(rootID, rel)
	if err != nil {
		return Resolved{}, nil, err
	}
	// Lstat, not Stat: Resolve already proved the link target is inside the
	// root, and Lstat lets us reject a non-regular final component.
	info, err := os.Lstat(res.Abs)
	if err != nil {
		return Resolved{}, nil, err
	}
	if err := checkFileType(info); err != nil {
		return Resolved{}, nil, err
	}
	return res, info, nil
}

// checkFileType refuses devices, sockets, FIFOs, and other special files.
// Symlinks are allowed here only because Resolve has already verified the
// target stays inside the approved root.
func checkFileType(info os.FileInfo) error {
	mode := info.Mode()
	if mode.IsRegular() || mode.IsDir() {
		return nil
	}
	if mode&os.ModeSymlink != 0 {
		return nil
	}
	return ErrUnsupportedFileType
}
