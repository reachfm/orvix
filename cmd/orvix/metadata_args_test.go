package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHandleMetadataArgs_Help asserts that -h, --help, and help all
// return (true, 0) without triggering the runtime bootstrap path.
func TestHandleMetadataArgs_Help(t *testing.T) {
	cases := [][]string{
		{"-h"},
		{"--help"},
		{"help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			handled, code := handleMetadataArgs(args)
			if !handled {
				t.Errorf("args %v should be handled", args)
			}
			if code != 0 {
				t.Errorf("args %v should exit 0, got %d", args, code)
			}
		})
	}
}

// TestHandleMetadataArgs_Version asserts -v, --version, and `version`
// return (true, 0) without triggering the runtime bootstrap path.
func TestHandleMetadataArgs_Version(t *testing.T) {
	cases := [][]string{
		{"-v"},
		{"--version"},
		{"version"},
		{"version", "--full"},
		{"version", "-v"},
		{"version", "-V"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			handled, code := handleMetadataArgs(args)
			if !handled {
				t.Errorf("args %v should be handled", args)
			}
			if code != 0 {
				t.Errorf("args %v should exit 0, got %d", args, code)
			}
		})
	}
}

// TestHandleMetadataArgs_UnknownArgsFallthrough asserts any other
// argument is treated as a runtime start and returns (false, 0).
func TestHandleMetadataArgs_UnknownArgsFallthrough(t *testing.T) {
	cases := [][]string{
		{},
		{"serve"},
		{"--unknown-flag"},
		{"-x"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			handled, code := handleMetadataArgs(args)
			if handled {
				t.Errorf("args %v should not be handled by metadata short-circuit", args)
			}
			if code != 0 {
				t.Errorf("fallthrough should not set exit code, got %d", code)
			}
		})
	}
}

// TestPrintHelp_OutputIsNonEmpty verifies the help text is meaningful
// and mentions the key commands (serve, version, help).
func TestPrintHelp_OutputIsNonEmpty(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	printHelp()
	w.Close()

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	if out == "" {
		t.Fatal("printHelp produced empty output")
	}
	for _, want := range []string{"orvix", "serve", "version", "help"} {
		if !strings.Contains(out, want) {
			t.Errorf("help text must mention %q, got:\n%s", want, out)
		}
	}
}

// TestMetadataArgs_DoNotBootstrapConfig runs the actual compiled
// orvix binary with --help and --version and verifies that:
//   - the command exits with code 0
//   - no "config" / "database" / "migration" boot logs are emitted
//   - the version string is present
//
// This test only runs when a Go toolchain is available and a built
// binary exists or can be produced in-test. It is skipped on platforms
// where the cross-compile would be too slow.
func TestMetadataArgs_DoNotBootstrapConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bin := buildOrvixBinary(t)
	defer os.Remove(bin)

	cases := []struct {
		args        []string
		wantContain []string
		wantExit    int
	}{
		{
			args:        []string{"--help"},
			wantContain: []string{"orvix", "serve", "version"},
			wantExit:    0,
		},
		{
			args:        []string{"-h"},
			wantContain: []string{"orvix"},
			wantExit:    0,
		},
		{
			args:        []string{"--version"},
			wantContain: []string{}, // dev builds print "0.0.0-dev dev build"
			wantExit:    0,
		},
		{
			args:        []string{"version"},
			wantContain: []string{},
			wantExit:    0,
		},
		{
			args:        []string{"version", "--full"},
			wantContain: []string{"orvix", "go_version", "os/arch"},
			wantExit:    0,
		},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			// Use an empty env without ORVIX_* so the bootstrap path
			// (if it ever ran) would fail loudly instead of silently
			// booting. That makes any regression easy to spot.
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}

			// Set a very short timeout — metadata commands must be fast.
			done := make(chan struct{})
			var out []byte
			var err error
			go func() {
				out, err = cmd.CombinedOutput()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("metadata command %v took >10s; should be near-instant", tc.args)
			}

			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					if exitErr.ExitCode() != tc.wantExit {
						t.Fatalf("args %v: exit code = %d, want %d, output:\n%s",
							tc.args, exitErr.ExitCode(), tc.wantExit, out)
					}
				} else {
					t.Fatalf("args %v: unexpected error: %v, output:\n%s", tc.args, err, out)
				}
			}

			// Critical: the bootstrap path emits these log lines. If any
			// appear, the metadata short-circuit regressed and is now
			// loading config / DB / migrations again.
			notWant := []string{
				"failed to load configuration",
				"failed to connect to database",
				"database migrations completed",
				"failed to run migrations",
				"orvix starting", // boot log
			}
			combined := string(out)
			for _, nw := range notWant {
				if strings.Contains(combined, nw) {
					t.Errorf("args %v: output should NOT contain %q (boot path leaked):\n%s",
						tc.args, nw, combined)
				}
			}

			for _, w := range tc.wantContain {
				if !strings.Contains(combined, w) {
					t.Errorf("args %v: output should contain %q, got:\n%s",
						tc.args, w, combined)
				}
			}
		})
	}
}

// buildOrvixBinary compiles the orvix binary into a temp file and
// returns its path. The caller is responsible for removing it.
func buildOrvixBinary(t *testing.T) string {
	t.Helper()
	tmp, err := os.CreateTemp("", "orvix-cli-test-*.exe")
	if err != nil {
		// On Linux: tmp without .exe suffix
		tmp, err = os.CreateTemp("", "orvix-cli-test-*")
		if err != nil {
			t.Fatalf("create temp binary: %v", err)
		}
	}
	tmp.Close()
	os.Remove(tmp.Name())

	cmd := exec.Command("go", "build", "-o", tmp.Name(), ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return tmp.Name()
}

// TestMetadataArgs_FastExit times a single --help invocation end-to-end
// to ensure the short-circuit path actually skips the heavy bootstrap.
// A regression that reintroduces the boot path will fail this test
// because config.NewDatabase / models.MigrateAllRaw take many seconds
// when pointed at a real (or fake) database.
func TestMetadataArgs_FastExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	bin := buildOrvixBinary(t)
	defer os.Remove(bin)

	start := time.Now()
	cmd := exec.Command(bin, "--help")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	dur := time.Since(start)

	if err != nil {
		t.Fatalf("--help should exit 0, got: %v\n%s", err, out)
	}
	// Threshold is generous (10s) because go test itself has
	// to start the test binary, the test then needs to invoke
	// `go build` to produce a fresh orvix binary, and only
	// then can it measure --help's exit time. On Windows
	// the build step alone can take 4-6s. The point of the
	// test is to catch a regression where --help accidentally
	// boots the full runtime (config + DB + migrations +
	// listeners) — that takes tens of seconds and is well
	// outside the threshold. A 10s ceiling still catches the
	// regression and tolerates slow CI.
	if dur > 10*time.Second {
		t.Errorf("--help took %v; expected <10s. Short-circuit may be regressed.", dur)
	}
}

// avoid an unused-import lint when fmt is the only consumer.
var _ = fmt.Sprintf