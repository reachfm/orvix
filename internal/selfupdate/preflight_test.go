package selfupdate

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// ---------------------------------------------------------------------
// Individual check function tests
// ---------------------------------------------------------------------

func TestCheckUpdaterDaemonHealthy(t *testing.T) {
	r := checkUpdaterDaemonHealthy()
	if r.Status != CheckPass || !r.Mandatory {
		t.Fatalf("want pass+mandatory, got %+v", r)
	}
}

func TestCheckServiceActive(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		r := checkServiceActive(func() (string, error) { return "active", nil })
		if r.Status != CheckPass {
			t.Fatalf("want pass, got %+v", r)
		}
	})
	t.Run("inactive", func(t *testing.T) {
		r := checkServiceActive(func() (string, error) { return "inactive", errors.New("exit status 3") })
		if r.Status != CheckFail {
			t.Fatalf("want fail, got %+v", r)
		}
	})
	t.Run("not found", func(t *testing.T) {
		r := checkServiceActive(func() (string, error) {
			return "", errors.New("exec: \"systemctl\": executable file not found in $PATH")
		})
		if r.Status != CheckFail {
			t.Fatalf("want fail, got %+v", r)
		}
	})
}

func TestCheckSystemdAvailable(t *testing.T) {
	orig := systemctlLookPath
	defer func() { systemctlLookPath = orig }()

	systemctlLookPath = func(string) (string, error) { return "/usr/bin/systemctl", nil }
	if r := checkSystemdAvailable(); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}

	systemctlLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if r := checkSystemdAvailable(); r.Status != CheckWarn {
		t.Fatalf("want warn, got %+v", r)
	}
	if checkSystemdAvailable().Mandatory {
		t.Fatal("systemd_available must be advisory")
	}
}

func TestCheckInstalledVersionKnown(t *testing.T) {
	if r := checkInstalledVersionKnown("1.2.3"); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkInstalledVersionKnown(""); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
	if r := checkInstalledVersionKnown("not-a-version"); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
}

type fakePinger struct{ err error }

func (f fakePinger) PingContext(ctx context.Context) error { return f.err }

func TestCheckDatabaseReachable(t *testing.T) {
	if r := checkDatabaseReachable(context.Background(), fakePinger{}); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkDatabaseReachable(context.Background(), fakePinger{err: errors.New("connection refused")}); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
	if r := checkDatabaseReachable(context.Background(), nil); r.Status != CheckFail {
		t.Fatalf("want fail for nil db, got %+v", r)
	}
}

func TestCheckSupportedDialect(t *testing.T) {
	if r := checkSupportedDialect(dbdialect.FromDriver("sqlite")); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkSupportedDialect(dbdialect.FromDriver("postgres")); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkSupportedDialect(nil); r.Status != CheckFail {
		t.Fatalf("want fail for nil dialect, got %+v", r)
	}
}

func TestCheckFreeDiskSpace(t *testing.T) {
	dir := t.TempDir()
	r := checkFreeDiskSpace("free_disk_space_test", dir, MinFreeDiskBytes)
	if !diskSpaceCheckSupported {
		if r.Status != CheckWarn {
			t.Fatalf("want warn on unsupported platform, got %+v", r)
		}
		return
	}
	// On a supported platform we can't assert pass/fail deterministically
	// (depends on the test host's real free space), but an absurdly large
	// threshold should always fail, and a tiny one should always pass.
	if r2 := checkFreeDiskSpace("t", dir, 1); r2.Status != CheckPass {
		t.Fatalf("want pass for 1-byte threshold, got %+v", r2)
	}
	if r3 := checkFreeDiskSpace("t", dir, 1<<62); r3.Status != CheckFail {
		t.Fatalf("want fail for absurd threshold, got %+v", r3)
	}
}

func TestCheckDirWritable(t *testing.T) {
	dir := t.TempDir()
	if r := checkDirWritable("update_dir_writable", dir); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkDirWritable("update_dir_writable", ""); r.Status != CheckFail {
		t.Fatalf("want fail for empty dir, got %+v", r)
	}
	// A path under a nonexistent, unwritable parent (best-effort: use a
	// file as a "directory" to force MkdirAll to fail).
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "child")
	if r := checkDirWritable("update_dir_writable", bad); r.Status != CheckFail {
		t.Fatalf("want fail for path under a file, got %+v", r)
	}
}

func TestCheckReleaseDownloadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	allowAll := func(*url.URL) error { return nil }
	r := checkReleaseDownloadable(context.Background(), srv.Client(), srv.URL+"/asset.tar.gz", allowAll)
	if r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}

	denyAll := func(*url.URL) error { return errors.New("host not allowed") }
	r2 := checkReleaseDownloadable(context.Background(), srv.Client(), srv.URL+"/asset.tar.gz", denyAll)
	if r2.Status != CheckFail {
		t.Fatalf("want fail when validator rejects, got %+v", r2)
	}

	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srvDown.Close()
	r3 := checkReleaseDownloadable(context.Background(), srvDown.Client(), srvDown.URL+"/asset.tar.gz", allowAll)
	if r3.Status != CheckFail {
		t.Fatalf("want fail for 404, got %+v", r3)
	}

	// HEAD not allowed -> falls back to ranged GET.
	srvHeadDenied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer srvHeadDenied.Close()
	r4 := checkReleaseDownloadable(context.Background(), srvHeadDenied.Client(), srvHeadDenied.URL+"/asset.tar.gz", allowAll)
	if r4.Status != CheckPass {
		t.Fatalf("want pass via ranged GET fallback, got %+v", r4)
	}
}

func TestCheckVerifiedSurfaceChecks(t *testing.T) {
	if r := checkChecksumVerified(nil); r.Status != CheckFail {
		t.Fatalf("want fail for nil bundle, got %+v", r)
	}
	if r := checkSignatureVerified(nil); r.Status != CheckFail {
		t.Fatalf("want fail for nil bundle, got %+v", r)
	}
	if r := checkManifestVerified(nil); r.Status != CheckFail {
		t.Fatalf("want fail for nil bundle, got %+v", r)
	}
	vb := &VerifiedBundle{SHA256: "abc", Version: "1.2.3", Commit: "deadbeef"}
	if r := checkChecksumVerified(vb); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkSignatureVerified(vb); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkManifestVerified(vb); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
}

func TestCheckArchitectureCompatible(t *testing.T) {
	if r := checkArchitectureCompatible(ReleaseInfoFull{Architecture: wantArchLabel}); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkArchitectureCompatible(ReleaseInfoFull{Architecture: "windows-amd64"}); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
	if r := checkArchitectureCompatible(ReleaseInfoFull{}); r.Status != CheckFail {
		t.Fatalf("want fail for empty architecture, got %+v", r)
	}
}

func TestCheckUpgradePathSupported(t *testing.T) {
	if r := checkUpgradePathSupported("1.0.0", "1.1.0"); r.Status != CheckPass {
		t.Fatalf("want pass for forward upgrade, got %+v", r)
	}
	if r := checkUpgradePathSupported("1.1.0", "1.0.0"); r.Status != CheckFail {
		t.Fatalf("want fail for downgrade, got %+v", r)
	}
	if r := checkUpgradePathSupported("1.1.0", "1.1.0"); r.Status != CheckFail {
		t.Fatalf("want fail for same version, got %+v", r)
	}
	if r := checkUpgradePathSupported("bogus", "1.1.0"); r.Status != CheckFail {
		t.Fatalf("want fail for invalid installed version, got %+v", r)
	}
}

func TestCheckConfigCompatible(t *testing.T) {
	if r := checkConfigCompatible(func() error { return nil }); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkConfigCompatible(func() error { return errors.New("bad yaml") }); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
	if r := checkConfigCompatible(nil); r.Status != CheckWarn {
		t.Fatalf("want warn for unconfigured loader, got %+v", r)
	}
}

func TestCheckMigrationsCompatible(t *testing.T) {
	if r := checkMigrationsCompatible(true, func() error { return nil }); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkMigrationsCompatible(false, func() error { return errors.New("missing runner") }); r.Status != CheckFail {
		t.Fatalf("want fail, got %+v", r)
	}
	if r := checkMigrationsCompatible(false, nil); r.Status != CheckWarn {
		t.Fatalf("want warn for unconfigured loader, got %+v", r)
	}
}

func TestCheckNoActiveJob(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	if r := checkNoActiveJob(store, "self"); r.Status != CheckPass {
		t.Fatalf("want pass with no jobs, got %+v", r)
	}
	if r := checkNoActiveJob(nil, "self"); r.Status != CheckFail {
		t.Fatalf("want fail for nil store, got %+v", r)
	}

	job, err := store.CreateJob(Job{IdempotencyKey: "k1", Kind: JobKindInstall})
	if err != nil {
		t.Fatal(err)
	}
	if r := checkNoActiveJob(store, job.ID); r.Status != CheckPass {
		t.Fatalf("want pass when the active job is self, got %+v", r)
	}
	if r := checkNoActiveJob(store, "someone-else"); r.Status != CheckFail {
		t.Fatalf("want fail when a different job is active, got %+v", r)
	}
}

func TestCheckUpgradeScriptPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "release", "upgrade.sh"), []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := checkUpgradeScriptPresent(root); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkUpgradeScriptPresent(t.TempDir()); r.Status != CheckWarn {
		t.Fatalf("want warn when missing, got %+v", r)
	}
	if checkUpgradeScriptPresent(root).Mandatory {
		t.Fatal("upgrade_script_present must be advisory")
	}
}

func TestCheckRollbackSnapshotPossible(t *testing.T) {
	pass := CheckResult{Status: CheckPass}
	fail := CheckResult{Status: CheckFail}
	if r := checkRollbackSnapshotPossible(pass, pass); r.Status != CheckPass {
		t.Fatalf("want pass, got %+v", r)
	}
	if r := checkRollbackSnapshotPossible(fail, pass); r.Status != CheckFail {
		t.Fatalf("want fail when writability failed, got %+v", r)
	}
	if r := checkRollbackSnapshotPossible(pass, fail); r.Status != CheckFail {
		t.Fatalf("want fail when disk space failed, got %+v", r)
	}
}

func TestCheckHTTPHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := checkHTTPHealth(context.Background(), "admin_console_health", srv.Client(), srv.URL)
	if r.Status != CheckPass || r.Mandatory {
		t.Fatalf("want advisory pass, got %+v", r)
	}

	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvBad.Close()
	r2 := checkHTTPHealth(context.Background(), "admin_console_health", srvBad.Client(), srvBad.URL)
	if r2.Status != CheckWarn || r2.Mandatory {
		t.Fatalf("want advisory warn (never fail) for unhealthy endpoint, got %+v", r2)
	}

	r3 := checkHTTPHealth(context.Background(), "admin_console_health", http.DefaultClient, "")
	if r3.Status != CheckWarn {
		t.Fatalf("want warn for unconfigured endpoint, got %+v", r3)
	}

	r4 := checkHTTPHealth(context.Background(), "admin_console_health", http.DefaultClient, "http://127.0.0.1:1/nope")
	if r4.Status != CheckWarn {
		t.Fatalf("want warn for unreachable endpoint, got %+v", r4)
	}
}

// ---------------------------------------------------------------------
// Integration: full suite
// ---------------------------------------------------------------------

func fullPassConfig(t *testing.T, store Store, jobID string) PreflightConfig {
	t.Helper()
	updateDir := t.TempDir()
	backupDir := t.TempDir()
	installRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(installRoot, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installRoot, "release", "upgrade.sh"), []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(assetSrv.Close)

	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(healthSrv.Close)

	verified := &VerifiedBundle{SHA256: "deadbeef", Version: "1.1.0", Commit: "abc123"}

	return PreflightConfig{
		JobID: jobID,
		Release: ReleaseInfoFull{
			ReleaseInfo:      ReleaseInfo{Tag: "v1.1.0"},
			AvailableVersion: "1.1.0",
			Architecture:     wantArchLabel,
			NeedsMigration:   false,
		},
		Verified:            verified,
		AssetURL:            assetSrv.URL + "/asset.tar.gz",
		InstalledVersion:    "1.0.0",
		DB:                  fakePinger{},
		Dialect:             dbdialect.FromDriver("sqlite"),
		Store:               store,
		UpdateDir:           updateDir,
		BackupDir:           backupDir,
		InstallRoot:         installRoot,
		HTTPClient:          assetSrv.Client(),
		ValidateURL:         func(*url.URL) error { return nil },
		AdminHealthURL:      healthSrv.URL,
		WebmailHealthURL:    healthSrv.URL,
		APIHealthURL:        healthSrv.URL,
		LoadConfig:          func() error { return nil },
		LoadMigrationRunner: func() error { return nil },
		ServiceActiveRunner: func() (string, error) { return "active", nil },
	}
}

func TestRunPreflight_AllPass(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateJob(Job{IdempotencyKey: "k1", Kind: JobKindInstall})
	if err != nil {
		t.Fatal(err)
	}

	if !diskSpaceCheckSupported {
		t.Skip("skipping all-pass assertion on a platform without real disk-space support (Windows dev machine) — see TestRunPreflight_MandatoryFailure for the failure path, which is platform independent")
	}

	cfg := fullPassConfig(t, store, job.ID)
	result := RunPreflight(context.Background(), cfg)

	if !result.OverallPass {
		t.Fatalf("want overall pass, got failed mandatory checks: %v\nchecks: %+v", result.FailedMandatory, result.Checks)
	}
	if len(result.FailedMandatory) != 0 {
		t.Fatalf("want no failed mandatory checks, got %v", result.FailedMandatory)
	}
	if result.ReleaseTag != "v1.1.0" || result.ReleaseSHA256 != "deadbeef" {
		t.Fatalf("release identity not recorded: %+v", result)
	}
}

func TestRunPreflight_MandatoryFailure(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateJob(Job{IdempotencyKey: "k1", Kind: JobKindInstall})
	if err != nil {
		t.Fatal(err)
	}

	cfg := fullPassConfig(t, store, job.ID)
	// Break one mandatory check: DB unreachable.
	cfg.DB = fakePinger{err: errors.New("connection refused")}

	result := RunPreflight(context.Background(), cfg)
	if result.OverallPass {
		t.Fatal("want overall fail when a mandatory check fails")
	}
	found := false
	for _, name := range result.FailedMandatory {
		if name == "database_reachable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want database_reachable in failed mandatory list, got %v", result.FailedMandatory)
	}
}

func TestRunPreflight_AdvisoryFailureDoesNotBlock(t *testing.T) {
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateJob(Job{IdempotencyKey: "k1", Kind: JobKindInstall})
	if err != nil {
		t.Fatal(err)
	}

	if !diskSpaceCheckSupported {
		t.Skip("skipping all-pass assertion on a platform without real disk-space support (Windows dev machine)")
	}

	cfg := fullPassConfig(t, store, job.ID)
	// Break only advisory checks: health endpoints unreachable, no upgrade
	// script.
	cfg.AdminHealthURL = "http://127.0.0.1:1/nope"
	cfg.WebmailHealthURL = "http://127.0.0.1:1/nope"
	cfg.APIHealthURL = "http://127.0.0.1:1/nope"
	cfg.InstallRoot = t.TempDir() // no release/upgrade.sh here

	result := RunPreflight(context.Background(), cfg)
	if !result.OverallPass {
		t.Fatalf("advisory-only failures must not block overall pass, got failed mandatory: %v\nchecks: %+v", result.FailedMandatory, result.Checks)
	}
}

func TestPreflightResult_Expired(t *testing.T) {
	now := time.Now().UTC()
	r := PreflightResult{CreatedAt: now, ExpiresAt: now.Add(PreflightTTL)}
	if r.Expired(now) {
		t.Fatal("must not be expired immediately after creation")
	}
	if !r.Expired(now.Add(PreflightTTL + time.Second)) {
		t.Fatal("must be expired after TTL elapses")
	}
}

func TestPreflightResult_MatchesRelease(t *testing.T) {
	r := PreflightResult{ReleaseTag: "v1.1.0", ReleaseSHA256: "deadbeef"}
	if !r.MatchesRelease("v1.1.0", "deadbeef") {
		t.Fatal("want match")
	}
	if r.MatchesRelease("v1.2.0", "deadbeef") {
		t.Fatal("want no match on different tag")
	}
	if r.MatchesRelease("v1.1.0", "other-sha") {
		t.Fatal("want no match on different sha")
	}
}

// ---------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------

func TestPreflightStore_SaveAndGet(t *testing.T) {
	db, _ := newTestDB(t)
	if err := CreatePreflightTable(db); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	pstore, ok := store.(PreflightStore)
	if !ok {
		t.Fatal("sqlStore must implement PreflightStore")
	}

	job, err := store.CreateJob(Job{IdempotencyKey: "k1", Kind: JobKindInstall})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := pstore.GetPreflightResult(job.ID); err != nil || ok {
		t.Fatalf("want no result yet, got ok=%v err=%v", ok, err)
	}

	now := time.Now().UTC()
	result := PreflightResult{
		JobID:         job.ID,
		ReleaseTag:    "v1.1.0",
		ReleaseSHA256: "deadbeef",
		CreatedAt:     now,
		ExpiresAt:     now.Add(PreflightTTL),
		OverallPass:   true,
		Checks: []CheckResult{
			{Name: "updater_daemon_healthy", Status: CheckPass, Mandatory: true},
		},
	}
	saved, err := pstore.SavePreflightResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("want an assigned ID")
	}

	got, ok, err := pstore.GetPreflightResult(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want a result")
	}
	if got.ReleaseTag != "v1.1.0" || got.ReleaseSHA256 != "deadbeef" || !got.OverallPass {
		t.Fatalf("round-tripped result mismatch: %+v", got)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "updater_daemon_healthy" {
		t.Fatalf("checks not round-tripped: %+v", got.Checks)
	}

	// Save a second, newer result — Get must return the newest.
	time.Sleep(10 * time.Millisecond)
	result2 := result
	result2.OverallPass = false
	result2.CreatedAt = time.Now().UTC()
	result2.ExpiresAt = result2.CreatedAt.Add(PreflightTTL)
	if _, err := pstore.SavePreflightResult(result2); err != nil {
		t.Fatal(err)
	}
	got2, ok, err := pstore.GetPreflightResult(job.ID)
	if err != nil || !ok {
		t.Fatalf("want a result, ok=%v err=%v", ok, err)
	}
	if got2.OverallPass {
		t.Fatal("want the newest (failing) result returned")
	}
}

func TestPreflightStore_SaveRequiresJobID(t *testing.T) {
	db, _ := newTestDB(t)
	if err := CreatePreflightTable(db); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	pstore := store.(PreflightStore)
	if _, err := pstore.SavePreflightResult(PreflightResult{}); err == nil {
		t.Fatal("want error for missing job id")
	}
}

var _ = sql.ErrNoRows // keep database/sql import used if scanning helpers change
