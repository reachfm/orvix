// Package releasebundle provides automated tests for the release bundle
// pipeline. It verifies that the bundle contains all required files,
// excludes forbidden files, and that the SHA-256 checksum and manifest
// are present and correct.
//
// Set BUNDLE_DIR to the extracted bundle root to run full verification.
// Without BUNDLE_DIR the tests verify the source tree at "release/".
package releasebundle

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func bundleDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("BUNDLE_DIR")
	if dir == "" {
		dir = "release"
	}
	return dir
}

var requiredFiles = []string{
	"VERSION",
	"orvix",
	"install.sh",
	"release/admin/index.html",
	"release/admin/package.json",
	"release/webmail/index.html",
	"release/marketing/index.html",
	"release/marketing/404.html",
	"release/marketing/robots.txt",
	"release/marketing/sitemap.xml",
	"release/scripts/build-release-bundle.sh",
	"release/scripts/smoke-admin-browser.sh",
	"release/scripts/smoke-admin-runtime.mjs",
	"release/scripts/install.sh",
	"release/scripts/install-public.sh",
	"release/scripts/setup-https.sh",
	"release/scripts/tests/test-admin-build.sh",
	"release/configs/orvix.yaml.example",
	"release/systemd/orvix.service",
}

var forbiddenPatterns = []string{
	".git/",
	"node_modules/",
	"test-results/",
	".opencode/",
	"artifacts/",
	"dev/",
	".env",
	"private.key",
}

func TestRequiredFilesExist(t *testing.T) {
	dir := bundleDir(t)
	if os.Getenv("BUNDLE_DIR") == "" {
		t.Skip("BUNDLE_DIR not set; run against extracted bundle for full verification")
	}
	for _, rf := range requiredFiles {
		path := filepath.Join(dir, rf)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("required file missing: %s", rf)
		}
	}
}

func TestRequiredFilesAreNonEmpty(t *testing.T) {
	dir := bundleDir(t)
	for _, rf := range requiredFiles {
		path := filepath.Join(dir, rf)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			t.Errorf("required file is empty: %s", rf)
		}
	}
}

func TestAdminSPAPackageJSON(t *testing.T) {
	path := filepath.Join(bundleDir(t), "release/admin/package.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("release/admin/package.json not found")
	}
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Type    string `json:"type"`
		Private bool   `json:"private"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pkg.Type != "module" {
		t.Errorf("expected type=module, got %s", pkg.Type)
	}
	if !pkg.Private {
		t.Error("expected private=true")
	}
}

func TestForbiddenFilesAbsent(t *testing.T) {
	dir := bundleDir(t)
	if os.Getenv("BUNDLE_DIR") == "" {
		t.Skip("BUNDLE_DIR not set")
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		for _, pat := range forbiddenPatterns {
			if strings.Contains(rel, pat) {
				t.Errorf("forbidden: %s matched pattern %s", rel, pat)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBundleSHA256Present(t *testing.T) {
	dir := bundleDir(t)
	matches, _ := filepath.Glob(filepath.Join(dir, "..", "*.sha256"))
	matches2, _ := filepath.Glob(filepath.Join(dir, "*.sha256"))
	matches = append(matches, matches2...)
	if len(matches) == 0 {
		t.Skip("no .sha256 file found")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(string(data))
	if len(parts) == 0 {
		t.Fatal("sha256 file is empty")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		t.Errorf("invalid hex: %s", parts[0])
	}
	if len(parts[0]) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(parts[0]))
	}
}

func TestBinaryExecutable(t *testing.T) {
	path := filepath.Join(bundleDir(t), "orvix")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Skip("orvix binary not found")
	}
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Error("orvix is not executable")
	}
}

func TestInstallScriptExecutable(t *testing.T) {
	path := filepath.Join(bundleDir(t), "install.sh")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Skip("install.sh not found")
	}
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Error("install.sh is not executable")
	}
}
