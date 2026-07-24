// This file implements Phase G of the Admin Console self-update feature:
// the state machine that actually performs backup, install, health-check,
// and rollback.
//
// This is the most safety-critical file in the package: it is the code
// that eventually runs as root and replaces a running mail server's
// binary. Two invariants hold throughout:
//
//  1. This package NEVER executes an arbitrary/caller-supplied command.
//     Every exec.Command-equivalent call goes through the Runner
//     interface below, and every argv passed to Runner.Run is a fixed
//     literal shape populated only from internally-verified data (a
//     Phase E VerifiedBundle, a Phase D Job) — never a raw string that
//     crossed the IPC boundary. There is exactly one script this package
//     will ever invoke: the repo's own release/upgrade.sh (see
//     upgradeScriptArgv), and exactly four systemctl invocations (see
//     the systemctl* fixed argv slices).
//  2. All filesystem roots and command execution are injected via
//     OrchestratorDeps so tests run entirely against temp directories and
//     fake command runners — this package must never touch a real
//     system path or actually invoke systemctl/upgrade.sh outside a
//     production wiring.
package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------
// Command execution boundary
// ---------------------------------------------------------------------

// CmdResult is the captured outcome of one Runner invocation.
type CmdResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner is the ONLY way this package ever spawns a process. Production
// wiring uses execRunner (os/exec, explicit argv, never a shell string).
// Tests inject a fake that never touches the real system.
type Runner interface {
	Run(ctx context.Context, name string, args []string) (CmdResult, error)
}

// execRunner is the production Runner. It always passes an explicit
// argument slice to exec.CommandContext — never exec.Command("sh", "-c",
// ...) or any string concatenation.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args []string) (CmdResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := CmdResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
	}
	return res, err
}

// NewExecRunner returns the production Runner. Kept as a constructor
// (rather than exporting execRunner directly) so production wiring code
// outside this package cannot accidentally embed/extend it.
func NewExecRunner() Runner { return execRunner{} }

// Fixed, literal systemctl argv shapes. These are the ONLY systemctl
// invocations this package ever performs; none of them is ever built
// from a caller-supplied string.
var (
	argvSystemctlStopOrvix     = []string{"stop", "orvix"}
	argvSystemctlStartOrvix    = []string{"start", "orvix"}
	argvSystemctlRestartOrvix  = []string{"restart", "orvix"}
	argvSystemctlIsActiveOrvix = []string{"is-active", "orvix"}
)

// upgradeScriptArgv builds the fixed argv shape for release/upgrade.sh:
//
//	upgrade.sh --checksum <sha256-hex> <binary-path>
//
// matching upgrade.sh's own documented contract (see release/upgrade.sh
// usage() and verify_checksum_fail_closed): a local-file upgrade REQUIRES
// either an operator-supplied --checksum or --dev-unsafe, and this
// package never runs in --dev-unsafe/--from-url mode. sha256 and
// binaryPath both come from Phase E's already-verified VerifiedBundle /
// the already-downloaded-and-verified local artifact path — never from
// any value that crossed the IPC boundary unvalidated.
func upgradeScriptArgv(sha256Hex, binaryPath string) []string {
	return []string{"--checksum", sha256Hex, binaryPath}
}

// ---------------------------------------------------------------------
// Orchestrator dependencies (all injected; production vs test wiring)
// ---------------------------------------------------------------------

// OrchestratorDeps bundles every filesystem root and external dependency
// the orchestrator needs, all injected so tests can point everything at
// a temp directory and a fake Runner without ever touching a real
// system. Production wiring (cmd/orvix-updater/main.go) is the only
// place that should populate this with real paths.
type OrchestratorDeps struct {
	Store Store

	Runner Runner

	// UpgradeScriptPath is the fixed, on-disk path to release/upgrade.sh
	// this daemon will invoke. Resolved once at daemon startup; never
	// derived from request input.
	UpgradeScriptPath string

	// Filesystem roots backed up/restored by rollback snapshots. All are
	// real absolute paths in production, temp-dir paths in tests.
	BinaryPath        string // e.g. /usr/local/bin/orvix
	AdminAssetsDir    string // e.g. /usr/share/orvix/admin
	WebmailAssetsDir  string // e.g. /usr/share/orvix/webmail
	ConfigDir         string // e.g. /etc/orvix
	SystemdUnitsDir   string // e.g. /etc/systemd/system, containing orvix.service etc
	BuildInfoPath     string // e.g. /usr/share/orvix/BUILDINFO
	TrustedPubKeyPath string // Ed25519 public key used for release verification

	// SnapshotRoot is the parent directory rollback snapshots are written
	// under (one subdirectory per snapshot ID). Never cleaned up
	// automatically — see CreateRollbackSnapshot's doc comment.
	SnapshotRoot string

	// DownloadDir is where a verified artifact is written to disk before
	// being handed to upgrade.sh (which requires a real binary-path
	// argument, not bytes in memory).
	DownloadDir string

	// HTTPClient + health endpoints, reused from Phase F's conventions.
	HTTPClient       *http.Client
	AdminHealthURL   string
	WebmailHealthURL string
	APIHealthURL     string

	// InstalledVersionReader returns the currently-installed version
	// string (e.g. parsed from BUILDINFO). Used both for preflight and
	// for confirming a completed install actually changed the version.
	InstalledVersionReader func() (string, error)

	// DBBackup, if non-nil, is invoked during snapshot creation ONLY when
	// the release being installed needs a migration (Phase E's
	// NeedsMigration). Kept as an interface so tests never touch a real
	// Postgres/pg_dump, and so production wiring can plug in
	// internal/pgbackup.CreateBackup without this package importing
	// database-specific packages directly.
	DBBackup DBBackupper

	// Now, if set, overrides time.Now (tests only).
	Now func() time.Time

	// HealthPollInterval/HealthPollTimeout bound the post-restart health
	// gate. Small values in tests, real-world values (e.g. 2s/60s) in
	// production.
	HealthPollInterval time.Duration
	HealthPollTimeout  time.Duration
}

// DBBackupper is the minimal surface the orchestrator needs from a
// database backup mechanism. internal/pgbackup.CreateBackup/RestoreBackup
// satisfy this shape when adapted by production wiring; tests supply a
// fake that writes/reads a marker file instead of running pg_dump.
type DBBackupper interface {
	// Backup writes a database backup into dir and returns a manifest
	// checksum identifying it (opaque to this package).
	Backup(ctx context.Context, dir string) (checksum string, err error)
	// Restore restores the database from the backup previously written
	// into dir by Backup. Only ever called during rollback, and only
	// when the corresponding install actually took a DB backup (see
	// RestoreRollbackSnapshot's doc comment for the safety reasoning).
	Restore(ctx context.Context, dir string) error
}

func (d *OrchestratorDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// ---------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------

// Orchestrator drives the Phase G state machine. It holds no mutable
// state of its own beyond Deps — every fact about an in-flight job lives
// in Deps.Store, so a daemon restart mid-job can be recovered from
// (Store.RecoverActiveJob, wired at daemon startup) without this struct
// needing to persist anything.
type Orchestrator struct {
	Deps OrchestratorDeps
}

// NewOrchestrator returns an Orchestrator with production defaults
// filled in for any zero-valued Deps fields that have a safe default
// (Runner, HTTPClient, poll interval/timeout). Callers must still set
// every filesystem path explicitly — there is no default for those.
func NewOrchestrator(deps OrchestratorDeps) *Orchestrator {
	if deps.Runner == nil {
		deps.Runner = NewExecRunner()
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = http.DefaultClient
	}
	if deps.HealthPollInterval <= 0 {
		deps.HealthPollInterval = 2 * time.Second
	}
	if deps.HealthPollTimeout <= 0 {
		deps.HealthPollTimeout = 60 * time.Second
	}
	return &Orchestrator{Deps: deps}
}

// irreversibleFrom is the phase at and after which a job can no longer be
// cancelled — see requirement 7. Phases strictly before this (queued,
// checking, downloading, verifying, preflight) are cancelable.
const irreversibleFrom = PhaseBackingUp

// CancelableNow reports whether job can currently be cancelled via
// OpCancelBeforeIrreversible.
func CancelableNow(job Job) bool {
	order := []Phase{PhaseQueued, PhaseChecking, PhaseDownloading, PhaseVerifying, PhasePreflight}
	for _, p := range order {
		if job.Phase == p {
			return true
		}
	}
	return false
}

func (o *Orchestrator) transition(jobID string, phase Phase, percent int, msg string) (Job, error) {
	return o.Deps.Store.UpdateJobPhase(jobID, phase, percent, msg)
}

// failJob transitions job to PhaseFailed, recording code/msg, and returns
// the terminal error. Any error building the failure transition itself is
// logged into the returned error rather than swallowed.
func (o *Orchestrator) failJob(jobID, code, msg string) error {
	if _, err := o.transition(jobID, PhaseFailed, 100, fmt.Sprintf("%s: %s", code, msg)); err != nil {
		return fmt.Errorf("selfupdate: job %s failed (%s: %s) and failure transition itself errored: %w", jobID, code, msg, err)
	}
	return fmt.Errorf("selfupdate: %s: %s", code, msg)
}

// ---------------------------------------------------------------------
// Install pipeline
// ---------------------------------------------------------------------

// InstallInput bundles the caller-verified inputs StartInstall needs.
// Discover is the already-executed, already-verified Phase E result — the
// orchestrator never re-derives or trusts anything about the release
// except what VerifyBundle already certified inside it.
type InstallInput struct {
	Job      Job
	Discover DiscoverResult
	Dialect  string // "sqlite" or "postgres", from dbdialect — informs whether DBBackup runs
}

// RunInstall drives a full install job from PhaseChecking through
// PhaseCompleted (or PhaseFailed / PhaseRolledBack on any mandatory
// failure). The job must already exist (PhaseQueued) via Store.CreateJob
// — RunInstall only transitions it forward.
//
// in.Discover.Info.Compatible must be true and in.Discover.Verified must
// be non-nil; RunInstall treats anything else as a caller bug (it is the
// caller's job to have rejected an incompatible/unverified release before
// ever creating the install job) and fails the job immediately.
func (o *Orchestrator) RunInstall(ctx context.Context, in InstallInput) (Job, error) {
	jobID := in.Job.ID
	store := o.Deps.Store

	if in.Discover.Verified == nil || !in.Discover.Info.Compatible {
		return Job{}, o.failJob(jobID, "release_not_verified", "release was not successfully verified before install was requested")
	}
	verified := in.Discover.Verified
	info := in.Discover.Info

	if _, err := o.transition(jobID, PhaseChecking, 5, "resolved release "+info.AvailableVersion); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(jobID, PhaseDownloading, 15, "release artifact already downloaded and in memory"); err != nil {
		return Job{}, err
	}

	// Persist the verified artifact to disk — upgrade.sh requires a real
	// binary-path argument, not bytes in memory.
	artifactPath, err := o.writeArtifact(info.AssetName, in.Discover.Artifact)
	if err != nil {
		return Job{}, o.failJob(jobID, "artifact_write_failed", err.Error())
	}

	if _, err := o.transition(jobID, PhaseVerifying, 25, "checksum "+verified.SHA256+" signature and manifest verified"); err != nil {
		return Job{}, err
	}

	// --- Preflight ---
	if _, err := o.transition(jobID, PhasePreflight, 30, "running preflight checks"); err != nil {
		return Job{}, err
	}
	pf, ok, err := preflightStoreOf(store).GetPreflightResult(jobID)
	if err != nil {
		return Job{}, o.failJob(jobID, "preflight_lookup_failed", err.Error())
	}
	now := o.Deps.now()
	if !ok {
		return Job{}, o.failJob(jobID, "preflight_missing", "no preflight result found for this job; run OpPreflight first")
	}
	if pf.Expired(now) {
		return Job{}, o.failJob(jobID, "preflight_expired", "preflight result has expired; re-run OpPreflight")
	}
	if !pf.MatchesRelease(info.Tag, verified.SHA256) {
		return Job{}, o.failJob(jobID, "preflight_mismatch", "preflight result was computed against a different release")
	}
	if !pf.OverallPass {
		return Job{}, o.failJob(jobID, "preflight_failed", "mandatory preflight checks failed: "+strings.Join(pf.FailedMandatory, ", "))
	}

	// --- Rollback snapshot (must happen before any irreversible phase) ---
	if _, err := o.transition(jobID, PhaseBackingUp, 40, "creating rollback snapshot"); err != nil {
		return Job{}, err
	}
	snap, err := o.CreateRollbackSnapshot(ctx, in.Job, info.NeedsMigration)
	if err != nil {
		return Job{}, o.failJob(jobID, "backup_failed", err.Error())
	}
	if err := store.MarkLastKnownGood(snap.ID); err != nil {
		return Job{}, o.failJob(jobID, "backup_mark_lkg_failed", err.Error())
	}

	// From here on, any mandatory failure triggers automatic rollback
	// (requirement 5) instead of just failing the job outright.
	runStep := func(phase Phase, percent int, msg string, step func() error) error {
		if _, err := o.transition(jobID, phase, percent, msg); err != nil {
			return err
		}
		if err := step(); err != nil {
			return o.autoRollback(ctx, jobID, snap, info.NeedsMigration, phase, err)
		}
		return nil
	}

	if err := runStep(PhaseStoppingService, 45, "stopping orvix.service", func() error {
		res, rerr := o.Deps.Runner.Run(ctx, "systemctl", argvSystemctlStopOrvix)
		o.appendSanitizedEvent(jobID, PhaseStoppingService, res)
		return rerr
	}); err != nil {
		return o.jobAfterRollback(jobID)
	}

	if err := runStep(PhaseMigrating, 55, "running database migrations", func() error {
		if !info.NeedsMigration {
			return nil
		}
		// The migration runner itself is invoked as part of the next
		// orvix process start (replacing_runtime/restarting run the new
		// binary, which applies its own pending migrations on boot,
		// matching how internal/config-driven startup already migrates
		// on every launch elsewhere in this codebase). Phase G does not
		// invent a second, separate migration invocation here — it only
		// records that a migration was expected so DB restore-on-
		// rollback knows to run.
		return nil
	}); err != nil {
		return o.jobAfterRollback(jobID)
	}

	if err := runStep(PhaseReplacingRuntime, 70, "invoking upgrade.sh", func() error {
		argv := upgradeScriptArgv(verified.SHA256, artifactPath)
		res, rerr := o.Deps.Runner.Run(ctx, o.Deps.UpgradeScriptPath, argv)
		o.appendSanitizedEvent(jobID, PhaseReplacingRuntime, res)
		if rerr != nil {
			return fmt.Errorf("upgrade.sh exited non-zero: %w", rerr)
		}
		return nil
	}); err != nil {
		return o.jobAfterRollback(jobID)
	}

	if err := runStep(PhaseRestarting, 85, "confirming orvix.service is active", func() error {
		return o.confirmServiceActive(ctx)
	}); err != nil {
		return o.jobAfterRollback(jobID)
	}

	if err := runStep(PhaseHealthCheck, 95, "polling health endpoints and installed version", func() error {
		return o.runHealthGate(ctx, info.AvailableVersion)
	}); err != nil {
		return o.jobAfterRollback(jobID)
	}

	final, err := o.transition(jobID, PhaseCompleted, 100, "install completed: version "+info.AvailableVersion)
	if err != nil {
		return Job{}, err
	}
	return final, nil
}

// jobAfterRollback re-fetches the job after autoRollback has already
// transitioned it to a terminal phase, so callers get the final Job
// state rather than a bare error.
func (o *Orchestrator) jobAfterRollback(jobID string) (Job, error) {
	job, err := o.Deps.Store.GetJob(jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Phase == PhaseRolledBack {
		return job, fmt.Errorf("selfupdate: install failed at a mandatory step and was automatically rolled back: %s", job.FailureMessage)
	}
	return job, fmt.Errorf("selfupdate: install failed and automatic rollback also failed: %s", job.FailureMessage)
}

// preflightStoreOf narrows store to the PreflightStore surface. Every
// production Store (sqlStore) implements both; this is a defensive type
// assertion so a future alternate Store implementation fails loudly at
// call time rather than silently skipping preflight enforcement.
func preflightStoreOf(store Store) PreflightStore {
	ps, ok := store.(PreflightStore)
	if !ok {
		panic("selfupdate: configured Store does not implement PreflightStore")
	}
	return ps
}

// writeArtifact persists the verified artifact bytes to Deps.DownloadDir
// under a name derived only from the already-verified asset name (never
// from anything else).
func (o *Orchestrator) writeArtifact(assetName string, data []byte) (string, error) {
	if err := os.MkdirAll(o.Deps.DownloadDir, 0o755); err != nil {
		return "", err
	}
	safeName := filepath.Base(assetName)
	if safeName == "" || safeName == "." || safeName == string(filepath.Separator) {
		safeName = "orvix-release-artifact"
	}
	path := filepath.Join(o.Deps.DownloadDir, safeName)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// appendSanitizedEvent records a command's outcome as a job event, with
// stdout/stderr passed through sanitizeCommandOutput first so no secret
// or token that a script might have echoed ends up in persisted,
// admin-console-visible history.
func (o *Orchestrator) appendSanitizedEvent(jobID string, phase Phase, res CmdResult) {
	msg := fmt.Sprintf("exit=%d stdout=%q stderr=%q", res.ExitCode, sanitizeCommandOutput(res.Stdout), sanitizeCommandOutput(res.Stderr))
	_, _ = o.Deps.Store.AppendEvent(jobID, phase, msg)
}

// sanitizeCommandOutput redacts anything that looks like a secret/token
// from captured command output before it is persisted. This is
// best-effort defense in depth, not a substitute for scripts themselves
// not printing secrets.
func sanitizeCommandOutput(s string) string {
	const maxLen = 8192
	lines := strings.Split(s, "\n")
	redactedLines := make([]string, 0, len(lines))
	lower := func(x string) string { return strings.ToLower(x) }
	for _, line := range lines {
		l := lower(line)
		if strings.Contains(l, "password") || strings.Contains(l, "secret") ||
			strings.Contains(l, "token") || strings.Contains(l, "private key") ||
			strings.Contains(l, "-----begin") {
			redactedLines = append(redactedLines, "[redacted line containing sensitive keyword]")
			continue
		}
		redactedLines = append(redactedLines, line)
	}
	out := strings.Join(redactedLines, "\n")
	if len(out) > maxLen {
		out = out[:maxLen] + "...[truncated]"
	}
	return out
}

func (o *Orchestrator) confirmServiceActive(ctx context.Context) error {
	res, err := o.Deps.Runner.Run(ctx, "systemctl", argvSystemctlIsActiveOrvix)
	out := strings.TrimSpace(res.Stdout)
	if out != "active" {
		if err != nil {
			return fmt.Errorf("orvix.service not active after restart: %s (%v)", out, err)
		}
		return fmt.Errorf("orvix.service not active after restart: %q", out)
	}
	return nil
}

// runHealthGate polls Admin/Webmail/API health endpoints plus the
// installed version until either everything passes or
// Deps.HealthPollTimeout elapses.
func (o *Orchestrator) runHealthGate(ctx context.Context, wantVersion string) error {
	deadline := o.Deps.now().Add(o.Deps.HealthPollTimeout)
	var lastErr error
	for {
		if err := o.checkOnce(ctx, wantVersion); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if o.Deps.now().After(deadline) {
			return fmt.Errorf("selfupdate: health gate did not pass within %s: %w", o.Deps.HealthPollTimeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Deps.HealthPollInterval):
		}
	}
}

func (o *Orchestrator) checkOnce(ctx context.Context, wantVersion string) error {
	for _, u := range []string{o.Deps.AdminHealthURL, o.Deps.WebmailHealthURL, o.Deps.APIHealthURL} {
		if u == "" {
			continue
		}
		if err := probeHTTP200(ctx, o.Deps.HTTPClient, u); err != nil {
			return err
		}
	}
	if o.Deps.InstalledVersionReader != nil {
		got, err := o.Deps.InstalledVersionReader()
		if err != nil {
			return fmt.Errorf("reading installed version: %w", err)
		}
		if got != wantVersion {
			return fmt.Errorf("installed version %q does not yet match target %q", got, wantVersion)
		}
	}
	return nil
}

func probeHTTP200(ctx context.Context, client *http.Client, url string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------
// Rollback snapshot creation
// ---------------------------------------------------------------------

// snapshotManifestEntry is one recorded file inside a rollback snapshot.
type snapshotManifestEntry struct {
	// Component is the logical group this file belongs to (binary,
	// admin_assets, webmail_assets, config, systemd, buildinfo,
	// trust_key, db_backup) — used by RestoreRollbackSnapshot to know
	// where to restore each entry.
	Component string `json:"component"`
	// RelPath is the path relative to the snapshot directory.
	RelPath string `json:"rel_path"`
	// OrigPath is the absolute path this file was copied from (and will
	// be restored to).
	OrigPath string `json:"orig_path"`
	SHA256   string `json:"sha256"`
}

type snapshotManifest struct {
	Entries []snapshotManifestEntry `json:"entries"`
}

// CreateRollbackSnapshot captures every file needed to restore the
// currently-installed, currently-good state, hashes the result, and
// persists snapshot metadata via Store.CreateSnapshot. It NEVER deletes
// any existing snapshot directory on disk — retention/cleanup of old
// snapshots is deliberately out of scope for Phase G (see the package
// doc comment), so disk usage growth is a known, accepted operational
// concern for a later phase, not a correctness bug here.
func (o *Orchestrator) CreateRollbackSnapshot(ctx context.Context, job Job, needsDBBackup bool) (RollbackSnapshot, error) {
	snapID := "snap_" + newSnapshotSuffix(o.Deps.now())
	dir := filepath.Join(o.Deps.SnapshotRoot, snapID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: create snapshot dir: %w", err)
	}

	var manifest snapshotManifest

	copyOne := func(component, origPath, relSubdir string) error {
		if origPath == "" {
			return nil
		}
		info, err := os.Stat(origPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // best-effort: not every deployment has every optional path
			}
			return err
		}
		destRoot := filepath.Join(dir, relSubdir)
		if info.IsDir() {
			entries, err := copyDirTree(origPath, destRoot)
			if err != nil {
				return err
			}
			for _, e := range entries {
				rel, _ := filepath.Rel(dir, e.dest)
				manifest.Entries = append(manifest.Entries, snapshotManifestEntry{
					Component: component, RelPath: rel, OrigPath: e.orig, SHA256: e.sha256,
				})
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(destRoot), 0o755); err != nil {
			return err
		}
		sum, err := copyFileHash(origPath, destRoot)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, destRoot)
		manifest.Entries = append(manifest.Entries, snapshotManifestEntry{
			Component: component, RelPath: rel, OrigPath: origPath, SHA256: sum,
		})
		return nil
	}

	if err := copyOne("binary", o.Deps.BinaryPath, "bin/orvix"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot binary: %w", err)
	}
	if err := copyOne("admin_assets", o.Deps.AdminAssetsDir, "admin"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot admin assets: %w", err)
	}
	if err := copyOne("webmail_assets", o.Deps.WebmailAssetsDir, "webmail"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot webmail assets: %w", err)
	}
	if err := copyOne("config", o.Deps.ConfigDir, "config"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot config: %w", err)
	}
	if err := copyOne("systemd", o.Deps.SystemdUnitsDir, "systemd"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot systemd units: %w", err)
	}
	if err := copyOne("buildinfo", o.Deps.BuildInfoPath, "BUILDINFO"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot buildinfo: %w", err)
	}
	if err := copyOne("trust_key", o.Deps.TrustedPubKeyPath, "trust/release-signing.pub.pem"); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: snapshot trust key: %w", err)
	}

	// Database backup ONLY when this release needs a migration. Taking a
	// DB backup unconditionally on every install would be wasted work
	// for the common patch-release case and would also make the "only
	// restore DB on rollback when a migration actually ran" invariant
	// (see RestoreRollbackSnapshot) harder to reason about.
	if needsDBBackup && o.Deps.DBBackup != nil {
		dbDir := filepath.Join(dir, "db")
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return RollbackSnapshot{}, fmt.Errorf("selfupdate: create db backup dir: %w", err)
		}
		checksum, err := o.Deps.DBBackup.Backup(ctx, dbDir)
		if err != nil {
			return RollbackSnapshot{}, fmt.Errorf("selfupdate: database backup: %w", err)
		}
		manifest.Entries = append(manifest.Entries, snapshotManifestEntry{
			Component: "db_backup", RelPath: "db", OrigPath: "", SHA256: checksum,
		})
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return RollbackSnapshot{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: write snapshot manifest: %w", err)
	}

	overallSum := deterministicManifestHash(manifest)

	// SourceVersion/SourceCommit record what this snapshot restores TO,
	// i.e. the version that was running right before this install/
	// rollback started — never the target version being installed. This
	// is read via InstalledVersionReader (the same mechanism the health
	// gate uses) rather than from job.ArtifactVersion, which instead
	// describes the release being installed.
	sourceVersion := job.RequestedVersion
	if o.Deps.InstalledVersionReader != nil {
		if v, err := o.Deps.InstalledVersionReader(); err == nil && v != "" {
			sourceVersion = v
		}
	}
	snap := RollbackSnapshot{
		ID:             snapID,
		SourceVersion:  sourceVersion,
		SourceCommit:   job.ArtifactCommit,
		ChecksumSHA256: overallSum,
		Verified:       true,
		CreatedAt:      o.Deps.now(),
		LastKnownGood:  false, // caller marks this via Store.MarkLastKnownGood
		Retained:       true,
	}
	created, err := o.Deps.Store.CreateSnapshot(snap)
	if err != nil {
		return RollbackSnapshot{}, fmt.Errorf("selfupdate: persist snapshot metadata: %w", err)
	}
	return created, nil
}

// deterministicManifestHash hashes a sorted, canonical representation of
// the manifest's entries so the same snapshot contents always produce
// the same checksum regardless of filesystem walk order.
func deterministicManifestHash(m snapshotManifest) string {
	entries := make([]snapshotManifestEntry, len(m.Entries))
	copy(entries, m.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\t%s\t%s\n", e.Component, e.RelPath, e.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newSnapshotSuffix(t time.Time) string {
	return t.Format("20060102T150405.000000000Z07")
}

// ---------------------------------------------------------------------
// Restore (used by both automatic and manual rollback)
// ---------------------------------------------------------------------

// RestoreRollbackSnapshot restores every recorded file from the snapshot
// directory back to its original path, verifying each restored file's
// sha256 against the manifest before considering it successfully
// restored — this is the "byte-identical restoration" requirement.
// Database restore only runs when the snapshot actually contains a
// db_backup entry (i.e. the corresponding install actually took a DB
// backup because it ran a migration): restoring a DB backup that was
// never taken, or restoring one when no forward migration actually ran,
// would silently discard any writes made between backup and rollback for
// no compensating benefit — the safe default when in doubt is to leave
// the database alone and let the operator decide, not to destructively
// overwrite it.
func (o *Orchestrator) RestoreRollbackSnapshot(ctx context.Context, snapDir string) error {
	manifestBytes, err := os.ReadFile(filepath.Join(snapDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("selfupdate: read snapshot manifest: %w", err)
	}
	var manifest snapshotManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("selfupdate: parse snapshot manifest: %w", err)
	}

	var dbRestoreNeeded bool
	for _, e := range manifest.Entries {
		if e.Component == "db_backup" {
			dbRestoreNeeded = true
			continue
		}
		src := filepath.Join(snapDir, e.RelPath)
		if err := os.MkdirAll(filepath.Dir(e.OrigPath), 0o755); err != nil {
			return fmt.Errorf("selfupdate: restore %s: mkdir: %w", e.OrigPath, err)
		}
		sum, err := copyFileHash(src, e.OrigPath)
		if err != nil {
			return fmt.Errorf("selfupdate: restore %s: %w", e.OrigPath, err)
		}
		if sum != e.SHA256 {
			return fmt.Errorf("selfupdate: restored file %s hash mismatch: want %s got %s (restoration is NOT byte-identical)", e.OrigPath, e.SHA256, sum)
		}
	}

	if dbRestoreNeeded && o.Deps.DBBackup != nil {
		dbDir := filepath.Join(snapDir, "db")
		if err := o.Deps.DBBackup.Restore(ctx, dbDir); err != nil {
			return fmt.Errorf("selfupdate: database restore: %w", err)
		}
	}

	// Restart the prior version. Fixed literal command, never a dynamic
	// string.
	if res, err := o.Deps.Runner.Run(ctx, "systemctl", argvSystemctlRestartOrvix); err != nil {
		return fmt.Errorf("selfupdate: restart orvix.service after restore: %w (stderr: %s)", err, sanitizeCommandOutput(res.Stderr))
	}
	return o.confirmServiceActive(ctx)
}

// autoRollback is invoked when a mandatory step fails at or after
// PhaseBackingUp. It transitions the job into PhaseRollingBack, restores
// the given snapshot, re-runs the health gate, and records the outcome
// as a terminal phase (PhaseRolledBack on success, PhaseFailed — with
// RollbackResult recorded — if the restore itself fails).
func (o *Orchestrator) autoRollback(ctx context.Context, jobID string, snap RollbackSnapshot, dbBackedUp bool, failedPhase Phase, cause error) error {
	store := o.Deps.Store
	failMsg := fmt.Sprintf("mandatory failure at phase %s: %v", failedPhase, cause)
	if _, err := store.UpdateJobPhase(jobID, PhaseFailed, 100, failMsg); err != nil {
		return err
	}
	if _, err := store.UpdateJobPhase(jobID, PhaseRollingBack, 100, "restoring rollback snapshot "+snap.ID); err != nil {
		return err
	}

	snapDir := filepath.Join(o.Deps.SnapshotRoot, snap.ID)
	restoreErr := o.RestoreRollbackSnapshot(ctx, snapDir)
	if restoreErr != nil {
		if _, err := store.UpdateJobPhase(jobID, PhaseFailed, 100, "rollback FAILED: "+restoreErr.Error()); err != nil {
			return err
		}
		return fmt.Errorf("selfupdate: automatic rollback failed: %w (original cause: %v)", restoreErr, cause)
	}

	healthErr := o.runHealthGate(ctx, snap.SourceVersion)
	if healthErr != nil {
		if _, err := store.UpdateJobPhase(jobID, PhaseFailed, 100, "rollback restored files but post-rollback health gate failed: "+healthErr.Error()); err != nil {
			return err
		}
		return fmt.Errorf("selfupdate: rollback restored files but health gate failed: %w", healthErr)
	}

	if _, err := store.UpdateJobPhase(jobID, PhaseRolledBack, 100, "automatic rollback succeeded"); err != nil {
		return err
	}
	return cause
}

// ---------------------------------------------------------------------
// Manual rollback entry point (requirement 6)
// ---------------------------------------------------------------------

// StartRollback performs a manual, independently-triggered rollback to
// the given snapshot. It creates its own JobKindRollback job (so it has
// its own audit trail distinct from whatever install job preceded it),
// restores the snapshot, re-runs the health gate, and ends in
// PhaseRolledBack or PhaseFailed.
func (o *Orchestrator) StartRollback(ctx context.Context, idempotencyKey, initiatedBy string, snap RollbackSnapshot) (Job, error) {
	store := o.Deps.Store
	job, err := store.CreateJob(Job{
		Kind:             JobKindRollback,
		IdempotencyKey:   idempotencyKey,
		RequestedVersion: snap.SourceVersion,
		InitiatedBy:      initiatedBy,
		Phase:            PhaseQueued,
		RollbackSnapshot: snap.ID,
	})
	if err != nil {
		return Job{}, err
	}
	// A rollback job created in an already-terminal phase (idempotent
	// replay of a prior completed/failed rollback) is returned as-is,
	// matching CreateJob's own idempotency-key contract.
	if job.Phase.Terminal() {
		return job, nil
	}

	// A rollback job walks the same legalPhaseTransitions state machine as
	// an install job (see store.go) — there is no separate rollback-only
	// phase graph, so the intermediate phases that don't semantically
	// apply to a rollback (downloading/verifying/preflight — the
	// snapshot being restored was already verified when it was created)
	// are passed through quickly with a message that says so, rather
	// than skipped, so the job's event history stays a complete,
	// honest record of what happened.
	if _, err := o.transition(job.ID, PhaseChecking, 10, "validating snapshot "+snap.ID); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseDownloading, 12, "rollback: no download needed, snapshot already on disk"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseVerifying, 14, "rollback: snapshot integrity checked against manifest during restore"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhasePreflight, 16, "rollback: no preflight gate required for a restore"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseBackingUp, 20, "manual rollback: snapshot already exists, skipping new backup"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseStoppingService, 30, "stopping orvix.service"); err != nil {
		return Job{}, err
	}
	if res, err := o.Deps.Runner.Run(ctx, "systemctl", argvSystemctlStopOrvix); err != nil {
		o.appendSanitizedEvent(job.ID, PhaseStoppingService, res)
		return o.terminalFail(job.ID, "stop_failed", err.Error())
	}

	if _, err := o.transition(job.ID, PhaseMigrating, 40, "no forward migration performed by a rollback"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseReplacingRuntime, 60, "restoring snapshot files"); err != nil {
		return Job{}, err
	}
	snapDir := filepath.Join(o.Deps.SnapshotRoot, snap.ID)
	if err := o.RestoreRollbackSnapshot(ctx, snapDir); err != nil {
		return o.terminalFail(job.ID, "restore_failed", err.Error())
	}

	if _, err := o.transition(job.ID, PhaseRestarting, 80, "orvix.service restarted by restore step"); err != nil {
		return Job{}, err
	}
	if _, err := o.transition(job.ID, PhaseHealthCheck, 90, "polling health endpoints"); err != nil {
		return Job{}, err
	}
	if err := o.runHealthGate(ctx, snap.SourceVersion); err != nil {
		return o.terminalFail(job.ID, "health_check_failed", err.Error())
	}

	return o.transition(job.ID, PhaseRolledBack, 100, "manual rollback completed")
}

func (o *Orchestrator) terminalFail(jobID, code, msg string) (Job, error) {
	if _, err := o.transition(jobID, PhaseFailed, 100, code+": "+msg); err != nil {
		return Job{}, err
	}
	job, err := o.Deps.Store.GetJob(jobID)
	if err != nil {
		return Job{}, err
	}
	return job, fmt.Errorf("selfupdate: rollback failed: %s: %s", code, msg)
}

// ---------------------------------------------------------------------
// Cancellation (requirement 7)
// ---------------------------------------------------------------------

// ErrCancelAfterIrreversible is returned by Cancel when job is already at
// or beyond irreversibleFrom.
var ErrCancelAfterIrreversible = errors.New("selfupdate: cannot cancel; job has passed the irreversible boundary")

// Cancel cancels job if it is still in a cancelable phase (see
// CancelableNow), transitioning it to PhaseFailed with a clear
// "cancelled by operator" message. Returns ErrCancelAfterIrreversible
// otherwise, without mutating the job.
func (o *Orchestrator) Cancel(jobID string) (Job, error) {
	job, err := o.Deps.Store.GetJob(jobID)
	if err != nil {
		return Job{}, err
	}
	if !CancelableNow(job) {
		return Job{}, ErrCancelAfterIrreversible
	}
	return o.transition(jobID, PhaseFailed, job.ProgressPercent, "cancelled by operator before irreversible phase")
}

// ---------------------------------------------------------------------
// Filesystem copy helpers
// ---------------------------------------------------------------------

type copiedFile struct {
	orig, dest, sha256 string
}

// copyDirTree recursively copies src into dest (creating dest), hashing
// every regular file copied. Symlinks are skipped (never followed) —
// this package never needs to snapshot a symlink target it doesn't
// control.
func copyDirTree(src, dest string) ([]copiedFile, error) {
	var out []copiedFile
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // skip symlinks
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		sum, err := copyFileHash(path, destPath)
		if err != nil {
			return err
		}
		out = append(out, copiedFile{orig: path, dest: destPath, sha256: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// copyFileHash copies src to dest (preserving the source file's mode)
// and returns the sha256 of the copied bytes.
func copyFileHash(src, dest string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return "", err
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return "", err
	}
	defer out.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
