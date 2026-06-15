package updater

import (
	"context"
	"strings"
	"testing"
)

// TestBuildSystemctlStartArgs_AreFixedAndBounded pins the exact
// argv shape that the web process ever issues to systemctl for
// an update run. The whole point of the systemd-oneshot design is
// that the web process's command to systemctl is BOUNDED: the
// verb "start" and the unit name. No flags, no env, no
// arbitrary args. If a refactor ever widens the argv the test
// must fail.
func TestBuildSystemctlStartArgs_AreFixedAndBounded(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"canonical", DefaultUpdateHelperUnit, []string{"start", DefaultUpdateHelperUnit}},
		{"empty coerced", "", []string{"start", DefaultUpdateHelperUnit}},
		{"non-canonical coerced", "evil-unit.service",
			[]string{"start", DefaultUpdateHelperUnit}},
		{"shell metacharacters coerced", "orvix-update.service; rm -rf /",
			[]string{"start", DefaultUpdateHelperUnit}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSystemctlStartArgs(c.in)
			if len(got) != 2 {
				t.Fatalf("argv length = %d, want 2; got %v", len(got), got)
			}
			if got[0] != "start" {
				t.Errorf("argv[0] = %q, want %q", got[0], "start")
			}
			if got[1] != DefaultUpdateHelperUnit {
				t.Errorf("argv[1] = %q, want %q", got[1], DefaultUpdateHelperUnit)
			}
		})
	}
}

// TestBuildSystemctlShowArgs_AreFixedAndBounded pins the argv
// shape for `systemctl show <unit> --property=Result,ExecMainStatus`.
// Same hardening intent as TestBuildSystemctlStartArgs_AreFixedAndBounded.
func TestBuildSystemctlShowArgs_AreFixedAndBounded(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"canonical", DefaultUpdateHelperUnit},
		{"empty coerced", ""},
		{"non-canonical coerced", "evil-unit.service"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSystemctlShowArgs(c.in)
			if len(got) != 4 {
				t.Fatalf("argv length = %d, want 4; got %v", len(got), got)
			}
			if got[0] != "show" {
				t.Errorf("argv[0] = %q, want %q", got[0], "show")
			}
			if got[1] != DefaultUpdateHelperUnit {
				t.Errorf("argv[1] = %q, want %q (coercion failed)", got[1], DefaultUpdateHelperUnit)
			}
			// The property list and --no-pager are hardcoded;
			// nothing user-controlled ever reaches this argv.
			if got[2] != "--property=Result,ExecMainStatus" {
				t.Errorf("argv[2] = %q, want %q", got[2], "--property=Result,ExecMainStatus")
			}
			if got[3] != "--no-pager" {
				t.Errorf("argv[3] = %q, want %q", got[3], "--no-pager")
			}
		})
	}
}

// TestBuildSystemctlIsActiveArgs_CoercesNonCanonical pins that
// `systemctl is-active` uses the same canonicalisation as
// buildSystemctlStartArgs and buildSystemctlShowArgs. A configured
// unit name that is not the canonical default must be coerced so the
// web process never queries an attacker-supplied unit.
func TestBuildSystemctlIsActiveArgs_CoercesNonCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"canonical", DefaultUpdateHelperUnit},
		{"empty coerced", ""},
		{"non-canonical coerced", "evil-unit.service"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSystemctlIsActiveArgs(c.in)
			if len(got) != 2 {
				t.Fatalf("argv length = %d, want 2; got %v", len(got), got)
			}
			if got[0] != "is-active" {
				t.Errorf("argv[0] = %q, want %q", got[0], "is-active")
			}
			if got[1] != DefaultUpdateHelperUnit {
				t.Errorf("argv[1] = %q, want %q (coercion failed)", got[1], DefaultUpdateHelperUnit)
			}
		})
	}
}

// TestNewRuntimeService_DefaultsHelperUnit verifies that a
// freshly-constructed service has the canonical helper unit name
// in cfg.UpdateHelperUnit. This is the defensive fallback that
// buildSystemctlStartArgs relies on.
func TestNewRuntimeService_DefaultsHelperUnit(t *testing.T) {
	svc, _ := newService(t)
	if svc.cfg.UpdateHelperUnit == "" {
		t.Fatal("UpdateHelperUnit is empty after NewRuntimeService")
	}
	if svc.cfg.UpdateHelperUnit != DefaultUpdateHelperUnit {
		t.Errorf("UpdateHelperUnit = %q, want %q",
			svc.cfg.UpdateHelperUnit, DefaultUpdateHelperUnit)
	}
}

// TestIsHelperUnitInstalled_NoLeak verifies that the
// helper-unit-installed check never includes a path in its
// return value (only a bool). The check is best-effort: it
// walks a small fixed set of canonical install locations; if
// any of them exists it returns true. The function itself
// never echoes a path.
func TestIsHelperUnitInstalled_NoLeak(t *testing.T) {
	// The function returns a bool only. Verify it does not
	// panic on Windows (where /etc/systemd/system does not
	// exist) and does not produce any string output that
	// could leak a path.
	got := isHelperUnitInstalled()
	t.Logf("isHelperUnitInstalled on this OS = %v", got)
	// The function returns a bool; nothing to assert beyond
	// the type system. This test exists to pin the contract
	// and to fail loudly if a future refactor changes the
	// signature.
}

// TestPreflight_SystemdPath_ReportsMissingHelperUnit verifies
// that on a machine without the helper unit installed
// (always true on a Windows dev box and on a fresh Linux
// install), the systemd-path preflight gate fails with the
// safe "update helper not installed" message and refuses the
// run.
func TestPreflight_SystemdPath_ReportsMissingHelperUnit(t *testing.T) {
	svc, _ := newService(t)
	svc.cfg.UpdateHelperUnit = DefaultUpdateHelperUnit
	pf := svc.Preflight(context.Background())
	if pf.Pass {
		t.Fatalf("expected preflight to fail when systemd helper unit is not installed; got pass=%+v", pf)
	}
	found := false
	for _, c := range pf.Checks {
		if c.Name == "update_helper_unit" && c.Status == "fail" {
			found = true
			// The detail field must be the safe generic
			// message; it must NOT include the absolute
			// path of any candidate unit file location.
			if c.Detail != "update helper not installed" {
				t.Errorf("helper-unit detail = %q, want %q", c.Detail, "update helper not installed")
			}
			for _, banned := range []string{"/etc/systemd/system/", "/lib/systemd/system/", "/usr/lib/systemd/system/"} {
				if strings.Contains(c.Detail, banned) {
					t.Errorf("helper-unit detail leaks candidate path %q: %q", banned, c.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected update_helper_unit check to fail, got %+v", pf.Checks)
	}
}

// TestPreflight_DoesNotLeakPrivatePathInDetail scans every
// preflight check's Detail field for absolute paths or any
// of the standard "private" tokens. The Detail strings are
// rendered into the admin UI; they must remain safe.
func TestPreflight_DoesNotLeakPrivatePathInDetail(t *testing.T) {
	svc, _ := newService(t)
	pf := svc.Preflight(context.Background())
	banned := []string{
		"/etc/systemd/system/",
		"/lib/systemd/system/",
		"/usr/lib/systemd/system/",
		"/etc/",
		"password=",
		"Bearer ",
		"PRIVATE KEY",
		"AKIA",
		"x-api-key",
	}
	for _, c := range pf.Checks {
		for _, b := range banned {
			if strings.Contains(c.Detail, b) {
				t.Errorf("preflight check %q detail leaks %q: %q", c.Name, b, c.Detail)
			}
		}
	}
}

// TestConfigRejectsNonCanonicalHelperUnit verifies that
// buildSystemctlStartArgs coerces any non-canonical unit name
// back to the canonical default. The intent is that even if
// a future refactor wires a wrong value into Config, the
// only unit the web process will ever start is
// DefaultUpdateHelperUnit.
func TestConfigRejectsNonCanonicalHelperUnit(t *testing.T) {
	svc, _ := newService(t)
	svc.cfg.UpdateHelperUnit = "evil-unit.service"
	got := buildSystemctlStartArgs(svc.cfg.UpdateHelperUnit)
	if got[1] != DefaultUpdateHelperUnit {
		t.Errorf("expected non-canonical unit to be coerced to %q, got %q",
			DefaultUpdateHelperUnit, got[1])
	}
}

// TestRun_SystemdPath_RefusedByPreflightWhenHelperMissing is
// the end-to-end test for the systemd path: with the helper
// unit NOT installed (always the case on Windows and on a
// fresh Linux box), Run() refuses the run with the safe
// preflight_failed code, never starts a script, never exec's
// systemctl. We must see exactly one preflight refusal.
func TestRun_SystemdPath_RefusedByPreflightWhenHelperMissing(t *testing.T) {
	svc, _ := newService(t)
	svc.cfg.UpdateHelperUnit = DefaultUpdateHelperUnit
	row, err := svc.Run(context.Background(), "user:1")
	if err == nil {
		t.Fatal("expected Run to refuse when helper unit is not installed")
	}
	if row != nil {
		t.Errorf("expected nil history row on preflight refusal, got %+v", row)
	}
	ue, ok := err.(*UpdateError)
	if !ok {
		t.Fatalf("expected *UpdateError, got %T (%v)", err, err)
	}
	if ue.Code != ErrCodePreflightFailed {
		t.Errorf("expected preflight_failed, got %q", ue.Code)
	}
	// The Error() string must be the safe code, never the
	// internal error text.
	if err.Error() != string(ErrCodePreflightFailed) {
		t.Errorf("Error() = %q, want %q (path leak?)", err.Error(), ErrCodePreflightFailed)
	}
}


