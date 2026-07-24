// This file implements Phase F of the Admin Console self-update feature:
// a persistent, expiring preflight-check suite that runs before an install
// (or rollback) job is allowed to proceed past PhasePreflight.
//
// Every check is a small, independently testable function. None of them
// build a shell command from caller-controlled input — the one systemd
// interaction (checkServiceActive) only ever invokes the two fixed literal
// commands `systemctl is-active orvix` and `systemctl --version`/LookPath,
// never a caller-supplied string, matching the threat model documented at
// the top of store.go and discovery.go.
//
// Mandatory vs advisory: a check is MANDATORY if a false result means an
// install attempt would very likely corrupt state, brick the host, or is
// simply impossible (no disk space, DB unreachable, release not verified,
// version would downgrade, another job already running, and so on). A
// check is ADVISORY (warn-only) if failing it does not prevent a safe
// install, and — critically — the entire point of running an update can be
// to *fix* the very thing an advisory check is reporting as broken. Admin
// console health, webmail health, and API/JMAP health are the clearest
// example: an operator's most common reason to click "install now" is that
// production is already unhealthy, so gating the update on those endpoints
// being up would make the feature useless in exactly the situation it
// exists for. "Systemd available" and "official upgrade script present"
// are also advisory here because Phase F cannot fully confirm the
// production install layout from a dev/test environment; Phase G's actual
// install step re-verifies these mechanically before doing anything
// destructive.
package selfupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// PreflightTTL is how long a computed PreflightResult remains valid. Phase G
// must treat a preflight result older than this (or one computed against a
// different release tag/sha) as stale and require a fresh run before
// allowing an install to proceed.
const PreflightTTL = 5 * time.Minute

// MinFreeDiskBytes is the minimum free space Phase F requires on both the
// update working directory and the backup directory's filesystem. Chosen
// generously relative to maxArtifactBytes (500 MiB, see discovery.go) to
// leave headroom for the extracted binary, the previous binary being
// backed up, and the rollback snapshot living alongside it.
const MinFreeDiskBytes = 2 << 30 // 2 GiB

// CheckStatus is the outcome of a single named preflight check.
type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// CheckResult is the outcome of one named, independently testable preflight
// check.
type CheckResult struct {
	Name      string      `json:"name"`
	Status    CheckStatus `json:"status"`
	Detail    string      `json:"detail"`
	Mandatory bool        `json:"mandatory"`
}

// failed reports whether this check result should block an install: a
// mandatory check that did not pass, i.e. is CheckFail. A warn on a
// mandatory check is treated as a soft pass here deliberately — "warn"
// is reserved for advisory checks and for mandatory checks whose
// dependency could not be evaluated (e.g. disk-space unsupported on this
// platform); a genuinely blocking condition on a mandatory check must
// report CheckFail, not CheckWarn.
func (c CheckResult) failed() bool {
	return c.Mandatory && c.Status == CheckFail
}

// PreflightResult is the persisted, expiring outcome of a full preflight
// run for one candidate release. Store.SavePreflightResult persists it;
// Phase G looks it up by JobID and must reject an install if it is
// missing, expired (see Expired), has a mandatory check that failed, or
// was computed against a different release tag/sha than the one about to
// be installed (compare ReleaseTag/ReleaseSHA256 against the job's
// resolved release).
type PreflightResult struct {
	ID              string        `json:"id"`
	JobID           string        `json:"job_id"`
	ReleaseTag      string        `json:"release_tag"`
	ReleaseSHA256   string        `json:"release_sha256"`
	CreatedAt       time.Time     `json:"created_at"`
	ExpiresAt       time.Time     `json:"expires_at"`
	OverallPass     bool          `json:"overall_pass"`
	Checks          []CheckResult `json:"checks"`
	FailedMandatory []string      `json:"failed_mandatory,omitempty"`
}

// Expired reports whether this result is no longer usable as of now.
func (r PreflightResult) Expired(now time.Time) bool {
	return now.After(r.ExpiresAt)
}

// MatchesRelease reports whether this result was computed against the
// given release identity. Phase G must call this (in addition to Expired)
// before trusting a stored result: a preflight computed for one release
// must never be reused to authorize installing a different one.
func (r PreflightResult) MatchesRelease(tag, sha256 string) bool {
	return r.ReleaseTag == tag && r.ReleaseSHA256 == sha256
}

// ---------------------------------------------------------------------
// Individual check functions. Each takes exactly the inputs it needs so
// it can be unit-tested without a real systemd/production host.
// ---------------------------------------------------------------------

// checkUpdaterDaemonHealthy is trivially true: if this code is executing,
// the updater daemon process is up and able to run Go code. It exists as
// its own named check so the UI has a "the daemon itself is fine" line
// item distinct from every downstream dependency check.
func checkUpdaterDaemonHealthy() CheckResult {
	return CheckResult{Name: "updater_daemon_healthy", Status: CheckPass, Detail: "updater daemon process is running", Mandatory: true}
}

// systemctlLookPath is the exec.LookPath used by checkSystemdAvailable and
// checkServiceActive. It is a var (not a hardcoded call) purely so tests
// can substitute a fake without touching PATH.
var systemctlLookPath = exec.LookPath

// checkSystemdAvailable reports whether the systemctl binary exists on
// PATH. It never runs a command — LookPath only stats candidate paths.
func checkSystemdAvailable() CheckResult {
	path, err := systemctlLookPath("systemctl")
	if err != nil {
		return CheckResult{Name: "systemd_available", Status: CheckWarn, Detail: "systemctl not found on PATH: " + err.Error(), Mandatory: false}
	}
	return CheckResult{Name: "systemd_available", Status: CheckPass, Detail: "found at " + path, Mandatory: false}
}

// serviceActiveRunner abstracts "run `systemctl is-active orvix` and
// return its trimmed stdout" so tests can fake the result without a real
// systemd. Production wires this to runSystemctlIsActiveOrvix, which is
// the ONLY place this package ever calls exec.Command, and always with the
// fixed literal argv ["systemctl", "is-active", "orvix"] — never a string
// built from caller input.
type serviceActiveRunner func() (string, error)

// runSystemctlIsActiveOrvix is the production serviceActiveRunner. The
// argv is a fixed literal slice; nothing here is interpolated from any
// caller-supplied value.
func runSystemctlIsActiveOrvix() (string, error) {
	out, err := exec.Command("systemctl", "is-active", "orvix").Output()
	return strings.TrimSpace(string(out)), err
}

// checkServiceActive reports whether the orvix.service systemd unit is
// currently active. `systemctl is-active` exits non-zero for any state
// other than "active", so an error from the runner is expected and simply
// means "not active" rather than an operational failure of the check
// itself, unless systemctl could not be found/run at all.
func checkServiceActive(run serviceActiveRunner) CheckResult {
	if run == nil {
		run = runSystemctlIsActiveOrvix
	}
	out, err := run()
	if out == "active" {
		return CheckResult{Name: "orvix_service_active", Status: CheckPass, Detail: "systemctl reports active", Mandatory: true}
	}
	if err != nil && out == "" {
		return CheckResult{Name: "orvix_service_active", Status: CheckFail, Detail: "systemctl is-active orvix: " + err.Error(), Mandatory: true}
	}
	return CheckResult{Name: "orvix_service_active", Status: CheckFail, Detail: fmt.Sprintf("systemctl reports %q", out), Mandatory: true}
}

// checkInstalledVersionKnown reports whether the currently installed
// version string is non-empty and parses via ValidateVersionString.
func checkInstalledVersionKnown(installedVersion string) CheckResult {
	if err := ValidateVersionString(installedVersion); err != nil {
		return CheckResult{Name: "installed_version_known", Status: CheckFail, Detail: err.Error(), Mandatory: true}
	}
	return CheckResult{Name: "installed_version_known", Status: CheckPass, Detail: "installed version " + installedVersion, Mandatory: true}
}

// dbPinger is the minimal surface checkDatabaseReachable needs — *sql.DB
// satisfies it directly, and tests can wrap a *sql.DB opened against
// sqlite in-memory or a broken/closed DB to exercise the failure path.
type dbPinger interface {
	PingContext(ctx context.Context) error
}

// checkDatabaseReachable pings db with a short timeout.
func checkDatabaseReachable(ctx context.Context, db dbPinger) CheckResult {
	if db == nil {
		return CheckResult{Name: "database_reachable", Status: CheckFail, Detail: "no database handle configured", Mandatory: true}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return CheckResult{Name: "database_reachable", Status: CheckFail, Detail: err.Error(), Mandatory: true}
	}
	return CheckResult{Name: "database_reachable", Status: CheckPass, Detail: "ping succeeded", Mandatory: true}
}

// supportedDialects is the allow-list of dialects this updater knows how
// to safely migrate/backup/restore. Mirrors dbdialect's own two known
// dialects — if dbdialect ever grows a third, this list (and the rest of
// the update pipeline) needs an explicit decision, not a silent pass.
var supportedDialects = map[dbdialect.Dialect]string{
	dbdialect.SQLite:   "sqlite",
	dbdialect.Postgres: "postgres",
}

// checkSupportedDialect reports whether dialect is one this updater
// supports.
func checkSupportedDialect(dialect *dbdialect.Info) CheckResult {
	if dialect == nil {
		return CheckResult{Name: "supported_database_dialect", Status: CheckFail, Detail: "no dialect info available", Mandatory: true}
	}
	name, ok := supportedDialects[dialect.Dialect]
	if !ok {
		return CheckResult{Name: "supported_database_dialect", Status: CheckFail, Detail: "unrecognized dialect", Mandatory: true}
	}
	return CheckResult{Name: "supported_database_dialect", Status: CheckPass, Detail: name, Mandatory: true}
}

// checkFreeDiskSpace reports whether the filesystem containing path has at
// least minBytes free. On platforms where diskFreeBytes is not
// implemented (see diskspace_other.go) this degrades to an advisory warn
// rather than failing the build or blocking an install outright — matching
// the peercred_other.go stub precedent for "unsupported on this dev
// platform".
func checkFreeDiskSpace(name, path string, minBytes int64) CheckResult {
	if !diskSpaceCheckSupported {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "disk space check unsupported on this platform (best effort)", Mandatory: false}
	}
	free, err := diskFreeBytes(path)
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("statfs %s: %v", path, err), Mandatory: true}
	}
	if free < minBytes {
		return CheckResult{Name: name, Status: CheckFail,
			Detail: fmt.Sprintf("%s has %d bytes free, need at least %d", path, free, minBytes), Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: fmt.Sprintf("%d bytes free", free), Mandatory: true}
}

// checkDirWritable attempts a real temp-file write+delete inside dir.
func checkDirWritable(name, dir string) CheckResult {
	if dir == "" {
		return CheckResult{Name: name, Status: CheckFail, Detail: "no directory configured", Mandatory: true}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("mkdir %s: %v", dir, err), Mandatory: true}
	}
	f, err := os.CreateTemp(dir, ".preflight-write-test-*")
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("create temp file in %s: %v", dir, err), Mandatory: true}
	}
	path := f.Name()
	_, werr := f.WriteString("preflight")
	cerr := f.Close()
	rerr := os.Remove(path)
	if werr != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("write to %s: %v", path, werr), Mandatory: true}
	}
	if cerr != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("close %s: %v", path, cerr), Mandatory: true}
	}
	if rerr != nil {
		// Wrote fine, cleanup failed — still writable, just leaked a temp
		// file. Not fatal, but worth surfacing.
		return CheckResult{Name: name, Status: CheckWarn, Detail: fmt.Sprintf("wrote ok but failed to remove temp file %s: %v", path, rerr), Mandatory: false}
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: dir + " is writable", Mandatory: true}
}

// urlValidator matches the shape of Discoverer.validateURL, reused here
// (rather than re-implemented) so the download-preflight check applies the
// exact same SSRF/host-allowlist gate Phase E already enforces.
type urlValidator func(*url.URL) error

// checkReleaseDownloadable performs a HEAD (falling back to a small ranged
// GET if HEAD is not supported) against assetURL, reusing validate — the
// caller must pass the same Discoverer.validateURL used for the real
// download so no new host allowlist is invented here.
func checkReleaseDownloadable(ctx context.Context, client *http.Client, assetURL string, validate urlValidator) CheckResult {
	name := "release_downloadable"
	u, err := url.Parse(assetURL)
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "malformed asset URL: " + err.Error(), Mandatory: true}
	}
	if validate != nil {
		if err := validate(u); err != nil {
			return CheckResult{Name: name, Status: CheckFail, Detail: "asset URL rejected: " + err.Error(), Mandatory: true}
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, u.String(), nil)
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: err.Error(), Mandatory: true}
	}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "HEAD request failed: " + err.Error(), Mandatory: true}
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		// Some object storage hosts reject HEAD; fall back to a 1-byte
		// ranged GET, which still confirms reachability without pulling
		// the whole (up to 500MiB) artifact.
		getCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
		defer cancel2()
		getReq, err := http.NewRequestWithContext(getCtx, http.MethodGet, u.String(), nil)
		if err != nil {
			return CheckResult{Name: name, Status: CheckFail, Detail: err.Error(), Mandatory: true}
		}
		getReq.Header.Set("Range", "bytes=0-0")
		getResp, err := client.Do(getReq)
		if err != nil {
			return CheckResult{Name: name, Status: CheckFail, Detail: "ranged GET failed: " + err.Error(), Mandatory: true}
		}
		getResp.Body.Close()
		if getResp.StatusCode >= 200 && getResp.StatusCode < 400 {
			return CheckResult{Name: name, Status: CheckPass, Detail: fmt.Sprintf("ranged GET returned %d", getResp.StatusCode), Mandatory: true}
		}
		return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("ranged GET returned %d", getResp.StatusCode), Mandatory: true}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return CheckResult{Name: name, Status: CheckPass, Detail: fmt.Sprintf("HEAD returned %d", resp.StatusCode), Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckFail, Detail: fmt.Sprintf("HEAD returned %d", resp.StatusCode), Mandatory: true}
}

// checkChecksumVerified, checkSignatureVerified, and checkManifestVerified
// do not redo any verification — they only surface the already-computed
// Phase E VerifyBundle result (verified == nil means verification did not
// succeed / was not run).
func checkChecksumVerified(verified *VerifiedBundle) CheckResult {
	if verified == nil {
		return CheckResult{Name: "checksum_verified", Status: CheckFail, Detail: "no verified bundle available", Mandatory: true}
	}
	return CheckResult{Name: "checksum_verified", Status: CheckPass, Detail: "sha256 " + verified.SHA256, Mandatory: true}
}

func checkSignatureVerified(verified *VerifiedBundle) CheckResult {
	if verified == nil {
		return CheckResult{Name: "signature_verified", Status: CheckFail, Detail: "no verified bundle available", Mandatory: true}
	}
	return CheckResult{Name: "signature_verified", Status: CheckPass, Detail: "Ed25519 signature verified", Mandatory: true}
}

func checkManifestVerified(verified *VerifiedBundle) CheckResult {
	if verified == nil {
		return CheckResult{Name: "manifest_verified", Status: CheckFail, Detail: "no verified bundle available", Mandatory: true}
	}
	return CheckResult{Name: "manifest_verified", Status: CheckPass, Detail: fmt.Sprintf("version=%s commit=%s", verified.Version, verified.Commit), Mandatory: true}
}

// checkArchitectureCompatible surfaces Phase E's already-determined
// architecture compatibility rather than re-deriving it.
func checkArchitectureCompatible(info ReleaseInfoFull) CheckResult {
	if info.Architecture == "" {
		return CheckResult{Name: "architecture_compatible", Status: CheckFail, Detail: "no architecture information available", Mandatory: true}
	}
	if info.Architecture != wantArchLabel {
		return CheckResult{Name: "architecture_compatible", Status: CheckFail,
			Detail: fmt.Sprintf("release architecture %q does not match required %q", info.Architecture, wantArchLabel), Mandatory: true}
	}
	return CheckResult{Name: "architecture_compatible", Status: CheckPass, Detail: info.Architecture, Mandatory: true}
}

// checkUpgradePathSupported reuses verify.go's compareVersions rather than
// duplicating version-ordering logic: it fails if the installed version is
// newer than or equal to the target (i.e. not a forward upgrade).
func checkUpgradePathSupported(installedVersion, targetVersion string) CheckResult {
	name := "upgrade_path_supported"
	if err := ValidateVersionString(installedVersion); err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "installed version: " + err.Error(), Mandatory: true}
	}
	if err := ValidateVersionString(targetVersion); err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "target version: " + err.Error(), Mandatory: true}
	}
	cmp, err := compareVersions(targetVersion, installedVersion)
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: err.Error(), Mandatory: true}
	}
	if cmp <= 0 {
		return CheckResult{Name: name, Status: CheckFail,
			Detail: fmt.Sprintf("target %s is not newer than installed %s", targetVersion, installedVersion), Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: fmt.Sprintf("%s -> %s", installedVersion, targetVersion), Mandatory: true}
}

// configLoader abstracts "does the current config file still parse". The
// production wiring passes a closure over the real internal/config.Load
// (which reads ORVIX_CONFIG / /etc/orvix / ./config, see config.go), kept
// as a func so tests can substitute a fake without touching real files or
// requiring a *zap.Logger.
type configLoader func() error

// checkConfigCompatible is a best-effort check: it only confirms the
// current config file still parses with the existing loader. It cannot
// know whether the *target* release's config schema is compatible without
// the target binary in hand, so a pass here means "at least the starting
// point is sane", not "the upgrade is guaranteed config-compatible".
func checkConfigCompatible(load configLoader) CheckResult {
	name := "config_compatible"
	if load == nil {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "no config loader configured (best effort)", Mandatory: false}
	}
	if err := load(); err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "config failed to load: " + err.Error(), Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: "current configuration parses", Mandatory: true}
}

// migrationRunnerLoader abstracts "the migration runner package/binary is
// present and loadable". Kept as a func so tests can fake both outcomes.
type migrationRunnerLoader func() error

// checkMigrationsCompatible is a best-effort stub: it surfaces Phase E's
// NeedsMigration flag (informational — migrations only actually need to
// run if this is true) plus a check that the migration runner itself is
// present/loadable, without attempting to run or dry-run any migration.
func checkMigrationsCompatible(needsMigration bool, runnerLoads migrationRunnerLoader) CheckResult {
	name := "migrations_compatible"
	if runnerLoads == nil {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "no migration runner check configured (best effort)", Mandatory: false}
	}
	if err := runnerLoads(); err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "migration runner unavailable: " + err.Error(), Mandatory: true}
	}
	detail := "migration runner available; no migration expected"
	if needsMigration {
		detail = "migration runner available; this upgrade is expected to run migrations"
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: detail, Mandatory: true}
}

// checkNoActiveJob queries Phase D's Store.GetActiveJob. Preflight itself
// runs during PhasePreflight of the very job being checked, so that job's
// own (non-terminal) presence is expected and excluded via
// selfJobID — anything else non-terminal means a second job is running.
func checkNoActiveJob(store Store, selfJobID string) CheckResult {
	name := "no_active_job"
	if store == nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: "no store configured", Mandatory: true}
	}
	job, err := store.GetActiveJob()
	if errors.Is(err, ErrNoActiveJob) {
		return CheckResult{Name: name, Status: CheckPass, Detail: "no active job", Mandatory: true}
	}
	if err != nil {
		return CheckResult{Name: name, Status: CheckFail, Detail: err.Error(), Mandatory: true}
	}
	if job.ID == selfJobID {
		return CheckResult{Name: name, Status: CheckPass, Detail: "only this job (" + job.ID + ") is active", Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckFail, Detail: "another job is already active: " + job.ID, Mandatory: true}
}

// checkUpgradeScriptPresent checks a fixed expected install path for
// release/scripts/upgrade.sh (release/upgrade.sh in this repo, see
// release/scripts/build-release-bundle.sh's `cp release/upgrade.sh
// "$BUNDLE_ROOT/release/upgrade.sh"`). The repo's installer does not
// appear to copy upgrade.sh to a separate fixed system path outside the
// unpacked release tree, so this check looks for it relative to the
// current install root (installRoot/release/upgrade.sh) and degrades to
// an advisory "not installed yet" warn — rather than guessing a system
// path that may not exist — if it cannot be found there.
func checkUpgradeScriptPresent(installRoot string) CheckResult {
	name := "upgrade_script_present"
	if installRoot == "" {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "no install root configured; cannot locate upgrade.sh (best effort)", Mandatory: false}
	}
	candidate := filepath.Join(installRoot, "release", "upgrade.sh")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return CheckResult{Name: name, Status: CheckPass, Detail: candidate, Mandatory: false}
	}
	return CheckResult{Name: name, Status: CheckWarn, Detail: "upgrade.sh not found at " + candidate + " (not installed yet)", Mandatory: false}
}

// checkRollbackSnapshotPossible is best-effort and deliberately does not
// duplicate the disk-space/writability probes: it reuses the results of
// checkDirWritable(backup) and checkFreeDiskSpace(backup) computed
// elsewhere in the same run.
func checkRollbackSnapshotPossible(backupWritable, backupDiskSpace CheckResult) CheckResult {
	name := "rollback_snapshot_possible"
	if backupWritable.Status == CheckFail || backupDiskSpace.Status == CheckFail {
		return CheckResult{Name: name, Status: CheckFail, Detail: "backup directory is not writable or lacks free space", Mandatory: true}
	}
	return CheckResult{Name: name, Status: CheckPass, Detail: "backup directory is writable with sufficient free space", Mandatory: true}
}

// checkHTTPHealth performs a GET against url with a short timeout. A 200
// response is a pass; anything else (including a transport error) is a
// warn, never a hard fail — see the package doc comment for why Admin
// Console / Webmail / API health checks are advisory rather than
// mandatory.
func checkHTTPHealth(ctx context.Context, name string, client *http.Client, healthURL string) CheckResult {
	if healthURL == "" {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "no health endpoint configured", Mandatory: false}
	}
	if client == nil {
		client = http.DefaultClient
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Detail: err.Error(), Mandatory: false}
	}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "request failed: " + err.Error(), Mandatory: false}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return CheckResult{Name: name, Status: CheckPass, Detail: "200 OK", Mandatory: false}
	}
	return CheckResult{Name: name, Status: CheckWarn, Detail: fmt.Sprintf("returned %d", resp.StatusCode), Mandatory: false}
}

// ---------------------------------------------------------------------
// Full-suite runner
// ---------------------------------------------------------------------

// PreflightConfig bundles every injected dependency the full preflight
// suite needs. All fields are optional in the sense that a nil/empty value
// degrades the corresponding check to a fail or advisory warn rather than
// panicking — see each individual check function's doc comment.
type PreflightConfig struct {
	JobID string

	// Release/verification inputs — Phase E's already-computed results,
	// never re-verified here.
	Release  ReleaseInfoFull
	Verified *VerifiedBundle
	AssetURL string

	InstalledVersion string

	DB      dbPinger
	Dialect *dbdialect.Info
	Store   Store

	UpdateDir   string
	BackupDir   string
	InstallRoot string

	HTTPClient       *http.Client
	ValidateURL      urlValidator
	AdminHealthURL   string
	WebmailHealthURL string
	APIHealthURL     string

	LoadConfig          configLoader
	LoadMigrationRunner migrationRunnerLoader

	ServiceActiveRunner serviceActiveRunner

	// Now, if set, overrides time.Now for CreatedAt/ExpiresAt (tests only).
	Now func() time.Time
}

// RunPreflight executes every preflight check against cfg and returns the
// aggregate PreflightResult. It does not persist the result — call
// Store.SavePreflightResult with the return value to do that.
func RunPreflight(ctx context.Context, cfg PreflightConfig) PreflightResult {
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	nowT := now().UTC()

	backupWritable := checkDirWritable("backup_dir_writable", cfg.BackupDir)
	backupDiskSpace := checkFreeDiskSpace("free_disk_space_backup", cfg.BackupDir, MinFreeDiskBytes)

	checks := []CheckResult{
		checkUpdaterDaemonHealthy(),
		checkServiceActive(cfg.ServiceActiveRunner),
		checkInstalledVersionKnown(cfg.InstalledVersion),
		checkDatabaseReachable(ctx, cfg.DB),
		checkSupportedDialect(cfg.Dialect),
		checkFreeDiskSpace("free_disk_space_update", cfg.UpdateDir, MinFreeDiskBytes),
		backupDiskSpace,
		checkDirWritable("update_dir_writable", cfg.UpdateDir),
		backupWritable,
		checkReleaseDownloadable(ctx, cfg.HTTPClient, cfg.AssetURL, cfg.ValidateURL),
		checkChecksumVerified(cfg.Verified),
		checkSignatureVerified(cfg.Verified),
		checkManifestVerified(cfg.Verified),
		checkArchitectureCompatible(cfg.Release),
		checkUpgradePathSupported(cfg.InstalledVersion, cfg.Release.AvailableVersion),
		checkConfigCompatible(cfg.LoadConfig),
		checkMigrationsCompatible(cfg.Release.NeedsMigration, cfg.LoadMigrationRunner),
		checkNoActiveJob(cfg.Store, cfg.JobID),
		checkSystemdAvailable(),
		checkUpgradeScriptPresent(cfg.InstallRoot),
		checkRollbackSnapshotPossible(backupWritable, backupDiskSpace),
		checkHTTPHealth(ctx, "admin_console_health", cfg.HTTPClient, cfg.AdminHealthURL),
		checkHTTPHealth(ctx, "webmail_health", cfg.HTTPClient, cfg.WebmailHealthURL),
		checkHTTPHealth(ctx, "api_jmap_health", cfg.HTTPClient, cfg.APIHealthURL),
	}

	var failedMandatory []string
	overall := true
	for _, c := range checks {
		if c.failed() {
			overall = false
			failedMandatory = append(failedMandatory, c.Name)
		}
	}

	return PreflightResult{
		ID:              newID("preflight"),
		JobID:           cfg.JobID,
		ReleaseTag:      cfg.Release.Tag,
		ReleaseSHA256:   verifiedSHA256(cfg.Verified),
		CreatedAt:       nowT,
		ExpiresAt:       nowT.Add(PreflightTTL),
		OverallPass:     overall,
		Checks:          checks,
		FailedMandatory: failedMandatory,
	}
}

func verifiedSHA256(v *VerifiedBundle) string {
	if v == nil {
		return ""
	}
	return v.SHA256
}

// ---------------------------------------------------------------------
// Persistence (extends Phase D's Store / store.go)
// ---------------------------------------------------------------------

// PreflightStore is the persistence surface Phase F adds on top of Phase
// D's Store. Kept as a separate interface (rather than growing the Store
// interface itself) so store.go's existing Store consumers/mocks do not
// need updating; sqlStore implements both.
type PreflightStore interface {
	// SavePreflightResult persists result, replacing any prior result for
	// the same JobID.
	SavePreflightResult(result PreflightResult) (PreflightResult, error)

	// GetPreflightResult returns the most recently saved preflight result
	// for jobID. ok is false if none exists.
	GetPreflightResult(jobID string) (PreflightResult, bool, error)
}

// CreatePreflightTable creates the preflight_results table if it does not
// already exist. Safe to call on every process startup, same convention as
// CreateTables in store.go. Kept as a separate function (rather than
// folded into CreateTables) so callers that only need Phase D tables are
// unaffected; the self-update daemon's startup path calls both.
func CreatePreflightTable(db *sql.DB) error {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	autoInc := dialect.AutoIncrement()
	ts := dialect.TimestampType()
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS preflight_results (
		row_id %s,
		id TEXT NOT NULL UNIQUE,
		job_id TEXT NOT NULL,
		release_tag TEXT NOT NULL DEFAULT '',
		release_sha256 TEXT NOT NULL DEFAULT '',
		created_at %s NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at %s NOT NULL DEFAULT CURRENT_TIMESTAMP,
		overall_pass INTEGER NOT NULL DEFAULT 0,
		checks_json TEXT NOT NULL DEFAULT '[]'
	)`, autoInc, ts, ts)
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("selfupdate: create preflight_results table: %w", err)
	}
	idxStatements := []string{
		"CREATE INDEX IF NOT EXISTS idx_preflight_results_job_id ON preflight_results(job_id)",
		"CREATE INDEX IF NOT EXISTS idx_preflight_results_created_at ON preflight_results(created_at)",
	}
	for _, stmt := range idxStatements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("selfupdate: create preflight index: %w", err)
		}
	}
	return nil
}

// SavePreflightResult implements PreflightStore. One job may be preflighted
// more than once (e.g. after fixing a failed check and retrying), so this
// keeps every row (the table is small, append-only, and useful audit
// history) but GetPreflightResult always returns the newest one.
func (s *sqlStore) SavePreflightResult(result PreflightResult) (PreflightResult, error) {
	if result.JobID == "" {
		return PreflightResult{}, errors.New("selfupdate: preflight result requires a job id")
	}
	if result.ID == "" {
		result.ID = newID("preflight")
	}
	checksJSON, err := json.Marshal(result.Checks)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("selfupdate: marshal preflight checks: %w", err)
	}
	overall := 0
	if result.OverallPass {
		overall = 1
	}
	insertSQL := s.dialect.Rewrite(`INSERT INTO preflight_results
		(id, job_id, release_tag, release_sha256, created_at, expires_at, overall_pass, checks_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := s.db.Exec(insertSQL, result.ID, result.JobID, result.ReleaseTag, result.ReleaseSHA256,
		result.CreatedAt, result.ExpiresAt, overall, string(checksJSON)); err != nil {
		return PreflightResult{}, fmt.Errorf("selfupdate: insert preflight result: %w", err)
	}
	return result, nil
}

// GetPreflightResult implements PreflightStore.
func (s *sqlStore) GetPreflightResult(jobID string) (PreflightResult, bool, error) {
	q := s.dialect.Rewrite(`SELECT id, job_id, release_tag, release_sha256, created_at, expires_at, overall_pass, checks_json
		FROM preflight_results WHERE job_id = ? ORDER BY created_at DESC`)
	row := s.db.QueryRow(q, jobID)
	var r PreflightResult
	var overall int
	var checksJSON string
	if err := row.Scan(&r.ID, &r.JobID, &r.ReleaseTag, &r.ReleaseSHA256, &r.CreatedAt, &r.ExpiresAt, &overall, &checksJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PreflightResult{}, false, nil
		}
		return PreflightResult{}, false, err
	}
	r.OverallPass = overall != 0
	r.CreatedAt = r.CreatedAt.UTC()
	r.ExpiresAt = r.ExpiresAt.UTC()
	if err := json.Unmarshal([]byte(checksJSON), &r.Checks); err != nil {
		return PreflightResult{}, false, fmt.Errorf("selfupdate: unmarshal preflight checks: %w", err)
	}
	for _, c := range r.Checks {
		if c.failed() {
			r.FailedMandatory = append(r.FailedMandatory, c.Name)
		}
	}
	return r, true, nil
}
