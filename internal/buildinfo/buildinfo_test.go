package buildinfo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGet_DevBuildSafeDefaults(t *testing.T) {
	// When the package vars are at their zero-dev defaults, Get() must
	// still return non-empty fields and IsDev must be true.
	info := Get()
	if info.Version == "" {
		t.Errorf("Version must not be empty")
	}
	if info.Commit == "" {
		t.Errorf("Commit must not be empty")
	}
	if info.BuildTime == "" {
		t.Errorf("BuildTime must not be empty")
	}
	if info.GoVersion == "" {
		t.Errorf("GoVersion must not be empty")
	}
	if info.OS == "" || info.Arch == "" {
		t.Errorf("OS/Arch must not be empty")
	}
	if !info.IsDev {
		t.Errorf("IsDev should be true for default package vars")
	}
}

func TestGet_ReleaseBuildIsDevFalse(t *testing.T) {
	// Simulate a release build by mutating the package vars, then restoring.
	origVersion, origCommit, origBuildTime, origChannel := Version, Commit, BuildTime, Channel
	defer func() {
		Version, Commit, BuildTime, Channel = origVersion, origCommit, origBuildTime, origChannel
	}()

	Version = "1.0.0-rc-test"
	Commit = "0dcdacd6b5d58c242eb5be9ef6d160ba92e4377e"
	BuildTime = "2026-07-01T18:00:00Z"
	Channel = "rc"

	info := Get()
	if info.IsDev {
		t.Errorf("IsDev must be false when Commit and BuildTime are set to non-dev values")
	}
	if info.Version != "1.0.0-rc-test" {
		t.Errorf("Version = %q, want 1.0.0-rc-test", info.Version)
	}
	if info.Commit != "0dcdacd6b5d58c242eb5be9ef6d160ba92e4377e" {
		t.Errorf("Commit not preserved")
	}
	if info.Channel != "rc" {
		t.Errorf("Channel = %q, want rc", info.Channel)
	}
	if info.Tag != "" {
		// We did not set Tag in this test.
		t.Errorf("Tag = %q, want empty", info.Tag)
	}
}

func TestGet_TagTrimmed(t *testing.T) {
	orig := Tag
	defer func() { Tag = orig }()
	Tag = "  v1.0.0  "
	info := Get()
	if info.Tag != "v1.0.0" {
		t.Errorf("Tag should be trimmed, got %q", info.Tag)
	}
}

func TestGet_EmptyCommitFallsBackToNotReported(t *testing.T) {
	orig := Commit
	defer func() { Commit = orig }()
	// Set BuildTime to something non-dev so IsDev depends on Commit.
	origBT := BuildTime
	defer func() { BuildTime = origBT }()
	BuildTime = "2026-07-01T18:00:00Z"
	Commit = ""

	info := Get()
	if info.Commit != "not reported" {
		t.Errorf("Empty commit must fall back to 'not reported', got %q", info.Commit)
	}
}

func TestShort_DevBuildMentionsDevBuild(t *testing.T) {
	// Ensure the short summary is reasonable in dev mode.
	s := Short()
	if s == "" {
		t.Fatal("Short must not be empty")
	}
	// Should mention "dev build" since BuildTime is "development" by default.
	if !strings.Contains(s, "dev build") && BuildTime == "development" {
		t.Errorf("Dev Short() should mention 'dev build', got %q", s)
	}
}

func TestShort_ReleaseBuildShowsCommit(t *testing.T) {
	origV, origC, origBT, origCh := Version, Commit, BuildTime, Channel
	defer func() { Version, Commit, BuildTime, Channel = origV, origC, origBT, origCh }()
	Version = "1.2.3"
	Commit = "0dcdacd6b5d58c242eb5be9ef6d160ba92e4377e"
	BuildTime = "2026-07-01T18:00:00Z"
	Channel = "rc"

	s := Short()
	if !strings.Contains(s, "1.2.3") {
		t.Errorf("Short must include version, got %q", s)
	}
	// Commit is truncated to 12 chars in Short()
	if !strings.Contains(s, "0dcdacd6b5d5") {
		t.Errorf("Short must include short commit hash, got %q", s)
	}
	if !strings.Contains(s, "channel: rc") {
		t.Errorf("Short must include channel, got %q", s)
	}
}

func TestLong_ContainsAllFields(t *testing.T) {
	l := Long()
	for _, want := range []string{"orvix", "commit:", "channel:", "build_time:", "go_version:", "os/arch:"} {
		if !strings.Contains(l, want) {
			t.Errorf("Long() must contain %q, got:\n%s", want, l)
		}
	}
}

func TestInfo_JSON(t *testing.T) {
	info := Get()
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(b), `"version"`) {
		t.Errorf("JSON must contain version field, got %s", b)
	}
	if !strings.Contains(string(b), `"commit"`) {
		t.Errorf("JSON must contain commit field")
	}
	if !strings.Contains(string(b), `"is_dev"`) {
		t.Errorf("JSON must contain is_dev field")
	}
}

func TestSafeNonEmpty(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "fallback"},
		{"   ", "fallback"},
		{"value", "value"},
		{"  value  ", "value"},
	}
	for _, c := range cases {
		got := safeNonEmpty(c.in, "fallback")
		if got != c.want {
			t.Errorf("safeNonEmpty(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanizeSince_InvalidReturnsEmpty(t *testing.T) {
	if got := humanizeSince("not a timestamp"); got != "" {
		t.Errorf("humanizeSince on invalid input should return empty, got %q", got)
	}
}

func TestHumanizeSince_RecentBuilds(t *testing.T) {
	// A timestamp from "now" must parse and return a non-empty string.
	if got := humanizeSince("2026-07-01T18:00:00Z"); got == "" {
		t.Errorf("humanizeSince on a valid timestamp should return non-empty")
	}
}
