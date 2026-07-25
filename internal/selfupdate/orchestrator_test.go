package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every invocation and lets tests script per-argv
// responses/failures without ever touching a real system.
type fakeRunner struct {
	calls []fakeCall
	// fail, keyed by "name argv-joined-by-space", returns this error
	// (and this result) instead of the default success result.
	fail map[string]error
	// custom overrides the default success result for a given key.
	custom map[string]CmdResult
	// failIsActiveOnly, when set, fails ONLY the "systemctl is-active
	// orvix" call (confirmServiceActive's restart-confirmation step)
	// while leaving "systemctl stop/restart orvix" and every other
	// command succeeding — used to isolate the restart-confirmation
	// failure path from the separate post-restart health-gate failure
	// path (see TestRunInstall_RestartConfirmationFailure_TriggersRollback).
	// It fails only the first failIsActiveOnlyTimes is-active calls (the
	// forward install's own confirmation step) so that rollback's own
	// restore-time confirmServiceActive call, which comes later, is left
	// free to succeed — isolating "forward restart confirmation failed"
	// from "rollback's restart confirmation also failed" (the latter is
	// already covered by TestRunInstall_ServiceStopFailure_TriggersRollback).
	failIsActiveOnly      error
	failIsActiveOnlyTimes int
	isActiveCalls         int
}

type fakeCall struct {
	name string
	args []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{fail: map[string]error{}, custom: map[string]CmdResult{}}
}

func (f *fakeRunner) key(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

// failOn registers an error for any call whose argv0 matches prefix
// (matched against the base command name, e.g. "upgrade.sh" or
// "systemctl").
func (f *fakeRunner) failOnCommand(name string, err error) {
	f.fail[name] = err
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string) (CmdResult, error) {
	f.calls = append(f.calls, fakeCall{name: name, args: args})
	base := filepath.Base(name)
	if err, ok := f.fail[base]; ok {
		return CmdResult{ExitCode: 1, Stderr: err.Error()}, err
	}
	if err, ok := f.fail[name]; ok {
		return CmdResult{ExitCode: 1, Stderr: err.Error()}, err
	}
	if base == "systemctl" && len(args) > 0 && args[0] == "is-active" {
		f.isActiveCalls++
		if f.failIsActiveOnly != nil && f.isActiveCalls <= f.failIsActiveOnlyTimes {
			return CmdResult{Stdout: "inactive", ExitCode: 3}, f.failIsActiveOnly
		}
		return CmdResult{Stdout: "active", ExitCode: 0}, nil
	}
	return CmdResult{Stdout: "ok", ExitCode: 0}, nil
}

// fakeDBBackup is an in-memory DBBackupper used by tests so no real
// pg_dump/pg_restore ever runs.
type fakeDBBackup struct {
	backupCalled, restoreCalled bool
	failBackup, failRestore     bool
}

func (f *fakeDBBackup) Backup(ctx context.Context, dir string) (string, error) {
	f.backupCalled = true
	if f.failBackup {
		return "", errors.New("simulated db backup failure")
	}
	if err := os.WriteFile(filepath.Join(dir, "backup.marker"), []byte("db-backup-contents"), 0o644); err != nil {
		return "", err
	}
	return "fake-db-checksum", nil
}

func (f *fakeDBBackup) Restore(ctx context.Context, dir string) error {
	f.restoreCalled = true
	if f.failRestore {
		return errors.New("simulated db restore failure")
	}
	return nil
}

// testHarness bundles a fresh Store + filesystem layout + Orchestrator for
// one test, entirely under t.TempDir().
type testHarness struct {
	t       *testing.T
	store   Store
	runner  *fakeRunner
	orch    *Orchestrator
	root    string
	binary  string
	admin   string
	webmail string
	config  string
	systemd string
	trust   string
	build   string

	installedVersion string
	healthServer     *httptest.Server
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	db, _ := newTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreatePreflightTable(db); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	h := &testHarness{
		t:       t,
		store:   store,
		root:    root,
		binary:  filepath.Join(root, "bin", "orvix"),
		admin:   filepath.Join(root, "admin"),
		webmail: filepath.Join(root, "webmail"),
		config:  filepath.Join(root, "config"),
		systemd: filepath.Join(root, "systemd"),
		trust:   filepath.Join(root, "trust", "signing.pub.pem"),
		build:   filepath.Join(root, "BUILDINFO"),
	}
	h.installedVersion = "1.0.0"

	mustWrite(t, h.binary, "orig-binary-bytes")
	mustWrite(t, filepath.Join(h.admin, "index.html"), "admin-html")
	mustWrite(t, filepath.Join(h.webmail, "index.html"), "webmail-html")
	mustWrite(t, filepath.Join(h.config, "orvix.yaml"), "config: true")
	mustWrite(t, filepath.Join(h.systemd, "orvix.service"), "[Unit]\n")
	mustWrite(t, h.trust, "-----BEGIN PUBLIC KEY-----\nfake\n-----END PUBLIC KEY-----\n")
	mustWrite(t, h.build, "version=1.0.0\n")

	h.healthServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(h.healthServer.Close)

	h.runner = newFakeRunner()

	h.orch = NewOrchestrator(OrchestratorDeps{
		Store:             store,
		Runner:            h.runner,
		UpgradeScriptPath: filepath.Join(root, "release", "upgrade.sh"),
		BinaryPath:        h.binary,
		AdminAssetsDir:    h.admin,
		WebmailAssetsDir:  h.webmail,
		ConfigDir:         h.config,
		SystemdUnitsDir:   h.systemd,
		BuildInfoPath:     h.build,
		TrustedPubKeyPath: h.trust,
		SnapshotRoot:      filepath.Join(root, "snapshots"),
		DownloadDir:       filepath.Join(root, "downloads"),
		HTTPClient:        h.healthServer.Client(),
		AdminHealthURL:    h.healthServer.URL + "/admin",
		WebmailHealthURL:  h.healthServer.URL + "/webmail",
		APIHealthURL:      h.healthServer.URL + "/api",
		InstalledVersionReader: func() (string, error) {
			return h.installedVersion, nil
		},
		HealthPollInterval: time.Millisecond,
		HealthPollTimeout:  200 * time.Millisecond,
	})

	return h
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// verifiedDiscoverResult builds a plausible, already-"verified" discovery
// result for tests, bypassing real network/signature verification (Phase
// E is covered by its own tests) so orchestrator tests focus purely on
// Phase G behavior.
func verifiedDiscoverResult(version string, needsMigration bool) DiscoverResult {
	return DiscoverResult{
		Info: ReleaseInfoFull{
			ReleaseInfo: ReleaseInfo{
				Tag:     "v" + version,
				Version: version,
			},
			AvailableVersion: version,
			NeedsMigration:   needsMigration,
			Compatible:       true,
		},
		Verified: &VerifiedBundle{
			SHA256:  "deadbeefcafef00d",
			Version: version,
			Commit:  "abc123",
		},
		Artifact: []byte("fake-verified-artifact-bytes"),
	}
}

// createQueuedJobWithPreflight creates a job, saves a passing preflight
// result for it (matching the given discover result), and returns the job.
func (h *testHarness) createQueuedJobWithPreflight(t *testing.T, disc DiscoverResult) Job {
	t.Helper()
	job, err := h.store.CreateJob(Job{
		Kind:             JobKindInstall,
		IdempotencyKey:   "test-key-" + disc.Info.AvailableVersion,
		RequestedVersion: disc.Info.AvailableVersion,
		InitiatedBy:      "test@example.com",
		Phase:            PhaseQueued,
		ArtifactVersion:  disc.Info.AvailableVersion,
		ArtifactCommit:   disc.Verified.Commit,
		ArtifactSHA256:   disc.Verified.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	pfStore := preflightStoreOf(h.store)
	_, err = pfStore.SavePreflightResult(PreflightResult{
		JobID:         job.ID,
		ReleaseTag:    disc.Info.Tag,
		ReleaseSHA256: disc.Verified.SHA256,
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(PreflightTTL),
		OverallPass:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// ---------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------

func TestRunInstall_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.1.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)

	// Simulate the new binary being live once "restart" happens: flip
	// installedVersion right when upgrade.sh (the runner call whose argv
	// contains our checksum) is invoked.
	h.installedVersion = "1.1.0"

	final, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err != nil {
		t.Fatalf("RunInstall failed: %v", err)
	}
	if final.Phase != PhaseCompleted {
		t.Fatalf("want completed, got %s", final.Phase)
	}

	// Confirm upgrade.sh was invoked with the fixed argv shape.
	var sawUpgrade bool
	for _, c := range h.runner.calls {
		if filepath.Base(c.name) == "upgrade.sh" {
			sawUpgrade = true
			if len(c.args) != 3 || c.args[0] != "--checksum" || c.args[1] != disc.Verified.SHA256 {
				t.Fatalf("unexpected upgrade.sh argv: %v", c.args)
			}
		}
	}
	if !sawUpgrade {
		t.Fatal("upgrade.sh was never invoked")
	}

	snaps, err := h.store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || !snaps[0].LastKnownGood {
		t.Fatalf("want exactly one last-known-good snapshot, got %+v", snaps)
	}
}

func TestStartRollback_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.2.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)
	h.installedVersion = "1.2.0"

	if _, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc}); err != nil {
		t.Fatalf("setup install failed: %v", err)
	}
	snaps, err := h.store.ListSnapshots()
	if err != nil || len(snaps) == 0 {
		t.Fatalf("expected a snapshot from install, got %v %v", snaps, err)
	}
	snap := snaps[0]

	// Mutate the binary to simulate the "bad" post-install state, then
	// roll back and confirm byte-identical restoration.
	if err := os.WriteFile(h.binary, []byte("corrupted-by-bad-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.installedVersion = snap.SourceVersion

	rbJob, err := h.orch.StartRollback(context.Background(), "rollback-key-1", "test@example.com", snap)
	if err != nil {
		t.Fatalf("StartRollback failed: %v", err)
	}
	if rbJob.Phase != PhaseRolledBack {
		t.Fatalf("want rolled_back, got %s", rbJob.Phase)
	}

	got, err := os.ReadFile(h.binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "orig-binary-bytes" {
		t.Fatalf("binary was not restored byte-identically: got %q", got)
	}
}

// ---------------------------------------------------------------------
// Forced-failure scenarios (requirement 9)
// ---------------------------------------------------------------------

func TestRunInstall_RejectsUnverifiedRelease(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.1.0", false)
	job, err := h.store.CreateJob(Job{
		Kind: JobKindInstall, IdempotencyKey: "unverified-key",
		RequestedVersion: disc.Info.AvailableVersion, Phase: PhaseQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	disc.Verified = nil // simulate a caller bug: unverified release handed in

	_, err = h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil {
		t.Fatal("expected error for unverified release")
	}
	got, gerr := h.store.GetJob(job.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Phase != PhaseFailed {
		t.Fatalf("want failed, got %s", got.Phase)
	}
}

func TestRunInstall_MissingPreflightRejected(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.1.0", false)
	// Create the job WITHOUT saving a preflight result.
	job, err := h.store.CreateJob(Job{
		Kind: JobKindInstall, IdempotencyKey: "no-preflight-key",
		RequestedVersion: disc.Info.AvailableVersion, Phase: PhaseQueued,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil || !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("want preflight-related error, got %v", err)
	}
	got, _ := h.store.GetJob(job.ID)
	if got.Phase != PhaseFailed {
		t.Fatalf("want failed, got %s", got.Phase)
	}
}

func TestRunInstall_UpgradeScriptFailure_TriggersRollback(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.3.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)

	h.runner.failOnCommand("upgrade.sh", errors.New("simulated upgrade.sh exit 1"))

	_, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil {
		t.Fatal("expected error")
	}

	final, gerr := h.store.GetJob(job.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if final.Phase != PhaseRolledBack {
		t.Fatalf("want rolled_back after upgrade.sh failure, got %s (msg=%s)", final.Phase, final.FailureMessage)
	}

	// Byte-identical restoration check.
	got, err := os.ReadFile(h.binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "orig-binary-bytes" {
		t.Fatalf("binary not restored byte-identically after rollback: %q", got)
	}

	// systemctl restart must have been invoked as part of restore.
	var sawRestart bool
	for _, c := range h.runner.calls {
		if filepath.Base(c.name) == "systemctl" && len(c.args) > 0 && c.args[0] == "restart" {
			sawRestart = true
		}
	}
	if !sawRestart {
		t.Fatal("expected systemctl restart during rollback")
	}
}

func TestRunInstall_ServiceStopFailure_TriggersRollback(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.4.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)

	h.runner.failOnCommand("systemctl", errors.New("simulated systemctl failure"))

	_, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := h.store.GetJob(job.ID)
	// systemctl failing means BOTH the stop step and the rollback's own
	// restart/is-active calls fail, so rollback itself cannot succeed —
	// this must land on failed (with a recorded rollback failure), not a
	// false "rolled_back".
	if final.Phase != PhaseFailed {
		t.Fatalf("want failed (rollback itself also fails because systemctl is broken), got %s", final.Phase)
	}
}

func TestRunInstall_HealthCheckFailure_TriggersRollback(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.5.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)

	// Never let installedVersion catch up, so the post-install health
	// gate's version check fails every poll until timeout.
	h.installedVersion = "1.0.0"

	_, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := h.store.GetJob(job.ID)
	if final.Phase != PhaseRolledBack {
		t.Fatalf("want rolled_back after health-check failure, got %s (msg=%s)", final.Phase, final.FailureMessage)
	}
	got, _ := os.ReadFile(h.binary)
	if string(got) != "orig-binary-bytes" {
		t.Fatalf("binary not restored byte-identically: %q", got)
	}
}

// TestRunInstall_RestartConfirmationFailure_TriggersRollback isolates the
// restart-confirmation step (PhaseRestarting: confirmServiceActive's
// `systemctl is-active orvix` check, orchestrator.go) from the
// post-restart health gate (PhaseHealthCheck: runHealthGate's HTTP
// probes + version check). These are two distinct code paths and two
// distinct phases; TestRunInstall_ServiceStopFailure_TriggersRollback
// only exercises a systemctl-wide outage (stop AND is-active AND
// restart all fail together, landing on PhaseFailed because rollback's
// own restart also fails), and TestRunInstall_HealthCheckFailure_
// TriggersRollback only exercises the HTTP/version health gate failing
// while systemctl succeeds throughout. Neither existing test isolates
// "systemctl restart succeeds but the service never reports active"
// while the health gate itself is never even reached.
func TestRunInstall_RestartConfirmationFailure_TriggersRollback(t *testing.T) {
	h := newTestHarness(t)
	disc := verifiedDiscoverResult("1.6.0", false)
	job := h.createQueuedJobWithPreflight(t, disc)

	h.runner.failIsActiveOnly = errors.New("simulated: orvix.service failed to become active")
	h.runner.failIsActiveOnlyTimes = 1 // only the forward-install confirmation call; rollback's own restore-time check must still succeed

	_, err := h.orch.RunInstall(context.Background(), InstallInput{Job: job, Discover: disc})
	if err == nil {
		t.Fatal("expected error")
	}
	final, gerr := h.store.GetJob(job.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	// Restart-confirmation failure during the forward install (not during
	// rollback's own restore, where is-active is not gated the same way)
	// must still trigger the same automatic-rollback path as any other
	// mandatory-step failure, landing on rolled_back with the original
	// binary restored byte-identically.
	if final.Phase != PhaseRolledBack {
		t.Fatalf("want rolled_back after restart-confirmation failure, got %s (msg=%s)", final.Phase, final.FailureMessage)
	}
	got, err := os.ReadFile(h.binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "orig-binary-bytes" {
		t.Fatalf("binary not restored byte-identically after rollback: %q", got)
	}
	// The terminal FailureMessage is overwritten by autoRollback's success
	// message ("automatic rollback succeeded"), so the restart-confirmation
	// failure itself must be found in the durable, immutable event log
	// instead — confirming this test actually exercised confirmServiceActive
	// and not some other step.
	events, everr := h.store.ListEvents(job.ID)
	if everr != nil {
		t.Fatal(everr)
	}
	var sawRestartConfirmFailure bool
	for _, e := range events {
		if e.Phase == PhaseFailed && strings.Contains(e.Message, "not active after restart") {
			sawRestartConfirmFailure = true
		}
	}
	if !sawRestartConfirmFailure {
		t.Fatalf("expected an event recording the restart-confirmation failure specifically; events: %+v", events)
	}
	if h.runner.isActiveCalls < 2 {
		t.Fatalf("expected is-active to be called at least twice (forward-install failure + rollback restore success), got %d", h.runner.isActiveCalls)
	}
}

func TestCreateRollbackSnapshot_WithDBBackup(t *testing.T) {
	h := newTestHarness(t)
	fakeDB := &fakeDBBackup{}
	h.orch.Deps.DBBackup = fakeDB

	job := Job{ID: "job1", ArtifactVersion: "1.0.0", ArtifactCommit: "abc"}
	snap, err := h.orch.CreateRollbackSnapshot(context.Background(), job, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fakeDB.backupCalled {
		t.Fatal("expected DBBackup.Backup to be called when needsDBBackup=true")
	}
	if snap.ChecksumSHA256 == "" {
		t.Fatal("expected non-empty snapshot checksum")
	}

	// A migration NOT needed must not call DBBackup.
	fakeDB2 := &fakeDBBackup{}
	h.orch.Deps.DBBackup = fakeDB2
	if _, err := h.orch.CreateRollbackSnapshot(context.Background(), job, false); err != nil {
		t.Fatal(err)
	}
	if fakeDB2.backupCalled {
		t.Fatal("did not expect DBBackup.Backup to be called when needsDBBackup=false")
	}
}

func TestCreateRollbackSnapshot_BackupFailure(t *testing.T) {
	h := newTestHarness(t)
	fakeDB := &fakeDBBackup{failBackup: true}
	h.orch.Deps.DBBackup = fakeDB

	job := Job{ID: "job1", ArtifactVersion: "1.0.0"}
	_, err := h.orch.CreateRollbackSnapshot(context.Background(), job, true)
	if err == nil {
		t.Fatal("expected error when db backup fails")
	}
}

func TestCancel_AllowedBeforeIrreversible(t *testing.T) {
	h := newTestHarness(t)
	job, err := h.store.CreateJob(Job{Kind: JobKindInstall, IdempotencyKey: "cancel-key-1", Phase: PhaseQueued})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.orch.Cancel(job.ID)
	if err != nil {
		t.Fatalf("expected cancel to succeed while queued: %v", err)
	}
	if got.Phase != PhaseFailed {
		t.Fatalf("want failed (cancelled), got %s", got.Phase)
	}
}

func TestCancel_RejectedAfterIrreversible(t *testing.T) {
	h := newTestHarness(t)
	job, err := h.store.CreateJob(Job{Kind: JobKindInstall, IdempotencyKey: "cancel-key-2", Phase: PhaseQueued})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Phase{PhaseChecking, PhaseDownloading, PhaseVerifying, PhasePreflight, PhaseBackingUp} {
		if _, err := h.store.UpdateJobPhase(job.ID, p, 0, "advance"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.orch.Cancel(job.ID); !errors.Is(err, ErrCancelAfterIrreversible) {
		t.Fatalf("want ErrCancelAfterIrreversible, got %v", err)
	}
}

func TestCancelableNow(t *testing.T) {
	cancelable := []Phase{PhaseQueued, PhaseChecking, PhaseDownloading, PhaseVerifying, PhasePreflight}
	for _, p := range cancelable {
		if !CancelableNow(Job{Phase: p}) {
			t.Fatalf("phase %s should be cancelable", p)
		}
	}
	notCancelable := []Phase{PhaseBackingUp, PhaseStoppingService, PhaseMigrating, PhaseReplacingRuntime, PhaseRestarting, PhaseHealthCheck, PhaseCompleted, PhaseFailed, PhaseRollingBack, PhaseRolledBack}
	for _, p := range notCancelable {
		if CancelableNow(Job{Phase: p}) {
			t.Fatalf("phase %s should NOT be cancelable", p)
		}
	}
}
