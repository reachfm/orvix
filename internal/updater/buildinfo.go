package updater

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BuildInfo is the current build's identity as exposed via
// /api/v1/update/status. All fields are SAFE to render in the
// admin UI: a version string, a 40-char git SHA, and a build
// timestamp. No env values, no tokens, no file contents, no
// private paths.
type BuildInfo struct {
	Version   string
	SHA       string
	BuildTime string
}

var (
	buildInfoMu  sync.RWMutex
	buildInfo    BuildInfo
	buildInfoSet bool
)

// SetBuildInfo overrides the cached build identity. This is the
// injection point for the `-ldflags` build pattern: a release build
// can do `updater.SetBuildInfo(BuildInfo{Version: "1.2.3",
// SHA: "...", BuildTime: "..."})` from an init() function compiled
// in via a release-only Go file. The runtime admin surface can then
// read it back via readBuildInfo().
func SetBuildInfo(b BuildInfo) {
	buildInfoMu.Lock()
	defer buildInfoMu.Unlock()
	buildInfo = b
	buildInfoSet = true
}

// readBuildInfo returns the cached build identity. If no override
// has been registered, it falls back to (a) the buildVersion /
// buildTime globals (set via -ldflags or defaults in this file),
// and (b) a best-effort git SHA lookup by reading .git/HEAD
// relative to the current working directory.
//
// The git SHA lookup NEVER panics, NEVER includes a private
// absolute path in any error message, and silently returns an
// empty SHA if the lookup fails. This is intentional: the admin
// UI must always render something, even on a machine that does
// not have a .git directory.
func readBuildInfo() BuildInfo {
	buildInfoMu.RLock()
	if buildInfoSet {
		defer buildInfoMu.RUnlock()
		return buildInfo
	}
	buildInfoMu.RUnlock()
	if buildVersionDefault == "" {
		buildVersionDefault = "development"
	}
	if buildTimeDefault == "" {
		buildTimeDefault = "development"
	}
	return BuildInfo{
		Version:   buildVersionDefault,
		SHA:       lookupGitShortSHA(),
		BuildTime: buildTimeDefault,
	}
}

// ReadBuildInfo returns the current build identity. Exported wrapper
// around readBuildInfo for use by other packages (e.g. backup).
func ReadBuildInfo() BuildInfo { return readBuildInfo() }

var (
	buildVersionDefault = ""
	buildTimeDefault    = ""
)

// lookupGitShortSHA reads .git/HEAD and resolves the SHA to a short
// 7-char form. Returns "" on any failure.
func lookupGitShortSHA() string {
	for _, base := range gitLookupBases() {
		head := filepath.Join(base, ".git", "HEAD")
		data, err := os.ReadFile(head)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "ref: ") {
			// Detached HEAD — line is the raw SHA.
			sha := strings.TrimSpace(strings.TrimPrefix(line, "ref: "))
			return shortSHA(sha)
		}
		refPath := strings.TrimPrefix(line, "ref: ")
		refFile := filepath.Join(base, ".git", refPath)
		data, err = os.ReadFile(refFile)
		if err != nil {
			// Try the packed-refs file.
			packed, perr := os.ReadFile(filepath.Join(base, ".git", "packed-refs"))
			if perr == nil {
				for _, l := range strings.Split(string(packed), "\n") {
					l = strings.TrimSpace(l)
					if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "^") {
						continue
					}
					fields := strings.Fields(l)
					if len(fields) == 2 && fields[1] == refPath {
						return shortSHA(fields[0])
					}
				}
			}
			continue
		}
		sha := strings.TrimSpace(string(data))
		return shortSHA(sha)
	}
	return ""
}

func gitLookupBases() []string {
	out := []string{}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, wd)
	}
	// Also try the parent of the executable, in case we are running
	// from a release install path.
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Dir(exe))
	}
	return out
}

func shortSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ── HTTP client shim (so we can unit-test without net/http) ──

// httpDoResult is the return shape of httpDo.
type httpDoResult struct {
	status int
	body   []byte
}

// httpNewRequest constructs an *http.Request bound to the given ctx.
func httpNewRequest(ctx context.Context, method, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, nil)
}

// httpDo performs the request with the given timeout and body cap.
// We use a fresh http.Client so we never reuse a Client that the
// caller may have configured differently.
func httpDo(req *http.Request, timeout time.Duration, maxBodyBytes int64) (httpDoResult, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return httpDoResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return httpDoResult{}, err
	}
	return httpDoResult{status: resp.StatusCode, body: body}, nil
}

// jsonUnmarshal decodes JSON into v, but limits input length to
// 1MB to defend against pathological servers.
func jsonUnmarshal(data []byte, v interface{}) error {
	if len(data) > 1<<20 {
		data = data[:1<<20]
	}
	return json.Unmarshal(data, v)
}

// ── Filesystem shim (so the runtime package compiles on Windows
// without dragging syscall.Statfs into the main file). ──

// statfsResult is the platform-agnostic statfs return shape. The
// implementation populates it on POSIX; on Windows it is left zeroed
// and the implementation returns an error.
type statfsResult struct {
	Bsize  int64
	Blocks uint64
	Bavail uint64
}

// statfsImpl is per-platform. On POSIX it calls statfs(2); on Windows
// it returns an error.
func statfsImpl(path string) (statfsResult, error) {
	return statfsPlatform(path)
}

// fileExists is a small helper.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// execLookPath is a thin wrapper around exec.LookPath to make the
// runtime package more testable.
func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// jsonStdMarshal is a thin wrapper around encoding/json to make
// tests self-contained.
func jsonStdMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
