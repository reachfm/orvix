// Package buildinfo exposes the build-time metadata that is injected
// into the binary via -ldflags. Dev builds (no -ldflags) return safe
// "development" / "not reported" values so the dashboard and CLI never
// print empty fields or fabricate SHAs.
//
// Recommended -ldflags invocation for production builds:
//
//	-X github.com/orvix/orvix/internal/buildinfo.Version=$(git describe --tags --always)
//	-X github.com/orvix/orvix/internal/buildinfo.Commit=$(git rev-parse HEAD)
//	-X github.com/orvix/orvix/internal/buildinfo.Tag=$(git describe --exact-match --tags HEAD 2>/dev/null || echo "")
//	-X github.com/orvix/orvix/internal/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)
//
// The variables are package-level so -ldflags can rewrite them at link
// time without touching any source file.
package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// These vars are intentionally package-level so they can be overridden
// via the Go linker's -X flag at build time. Do NOT rename without also
// updating the release/Makefile ldflags definitions.
var (
	// Version is the human-readable release or pre-release tag, e.g.
	// "1.0.0-rc-enterprise-sustainability-v1". Falls back to "0.0.0-dev".
	Version = "0.0.0-dev"

	// Commit is the git commit SHA the binary was built from. "not reported"
	// when the binary was built without -ldflags.
	Commit = "not reported"

	// Tag is the exact git tag if HEAD is tagged, otherwise empty string.
	// Empty means the binary was built from an untagged commit.
	Tag = ""

	// BuildTime is the UTC RFC3339 timestamp of the build, e.g.
	// "2026-07-01T18:00:00Z". "development" for dev builds.
	BuildTime = "development"

	// Channel is the release channel: "stable", "rc", "dev". Defaults to "stable"
	// when unset; -X github.com/orvix/orvix/internal/buildinfo.Channel=rc overrides.
	Channel = "stable"
)

// Info is the consolidated build metadata exposed by CLI, status, and
// admin settings endpoints. All fields are non-empty; missing data is
// reported as the explicit strings above rather than left blank.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Tag       string `json:"tag,omitempty"`
	BuildTime string `json:"build_time"`
	Channel   string `json:"channel"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// IsDev is true when the binary was built without ldflags injection.
	// The admin dashboard uses this to label the build appropriately.
	IsDev bool `json:"is_dev"`
}

// Get returns the resolved Info for the current process. Always returns
// a non-nil value with safe defaults; never panics on missing ldflags.
func Get() Info {
	isDev := BuildTime == "development" || Commit == "not reported"
	return Info{
		Version:   safeNonEmpty(Version, "0.0.0-dev"),
		Commit:    safeNonEmpty(Commit, "not reported"),
		Tag:       strings.TrimSpace(Tag),
		BuildTime: safeNonEmpty(BuildTime, "development"),
		Channel:   safeNonEmpty(Channel, "stable"),
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		IsDev:     isDev,
	}
}

// Short returns a one-line summary suitable for `orvix version` output.
//
//	orvix 1.0.0-rc-enterprise-sustainability-v1 (commit: 0dcdacd6, channel: rc, built: 2026-07-01T18:00:00Z)
func Short() string {
	info := Get()
	parts := []string{info.Version}
	if info.Commit != "not reported" && info.Commit != "" {
		short := info.Commit
		if len(short) > 12 {
			short = short[:12]
		}
		parts = append(parts, fmt.Sprintf("commit: %s", short))
	}
	if info.Channel != "" {
		parts = append(parts, fmt.Sprintf("channel: %s", info.Channel))
	}
	if info.BuildTime != "" && info.BuildTime != "development" {
		parts = append(parts, fmt.Sprintf("built: %s", info.BuildTime))
	} else {
		parts = append(parts, "dev build")
	}
	return strings.Join(parts, " ")
}

// Long returns a multi-line detail block for `orvix version --full`
// and the admin runtime telemetry endpoint.
func Long() string {
	info := Get()
	var b strings.Builder
	fmt.Fprintf(&b, "orvix %s\n", info.Version)
	if info.Tag != "" {
		fmt.Fprintf(&b, "  tag:        %s\n", info.Tag)
	}
	fmt.Fprintf(&b, "  commit:     %s\n", info.Commit)
	fmt.Fprintf(&b, "  channel:    %s\n", info.Channel)
	fmt.Fprintf(&b, "  build_time: %s\n", info.BuildTime)
	fmt.Fprintf(&b, "  go_version: %s\n", info.GoVersion)
	fmt.Fprintf(&b, "  os/arch:    %s/%s\n", info.OS, info.Arch)
	if info.IsDev {
		fmt.Fprintf(&b, "  note:       development build (no ldflags metadata injected)\n")
	} else {
		fmt.Fprintf(&b, "  note:       built %s ago\n", humanizeSince(info.BuildTime))
	}
	return b.String()
}

func safeNonEmpty(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func humanizeSince(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}