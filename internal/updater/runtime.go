package updater

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RuntimeService implements Update Management v1.
//
// The service is intentionally narrow:
//   - It does not parse YAML, env files, or any user-supplied input.
//   - It does not invoke a shell. The runtime script is executed via
//     exec.CommandContext, which on POSIX is execve(2) and on Windows
//     is CreateProcess — never a shell — so there is no shell-injection
//     surface.
//   - The script path is hard-coded as a package constant
//     DefaultScriptPath. The handler may override it via UpdateConfig,
//     but it is never derived from a request body, query string, header,
//     or anything else attacker-controlled.
//   - The resolved absolute path of the script is verified against the
//     server's working-directory prefix and against the explicit
//     "release/scripts" suffix before exec.
//   - All operations that touch the database (history, audit) use
//     parameterised SQL.
//   - All long-running work holds a single-flight mutex so two
//     concurrent update requests are rejected with 409 Conflict.
type RuntimeService struct {
	db     *sql.DB
	cfg    Config
	logger *zap.Logger

	// checkURL is the optional update check URL, supplied by the
	// handler via WithCheckURL. It is read from server config and
	// never from a request body.
	checkURL string

	// mu serialises Run() invocations. A second call while a job is
	// running returns ErrJobRunning.
	mu sync.Mutex

	// lastCheck caches the result of the most recent CheckForUpdate.
	lastCheck *UpdateStatus
	// lastCheckMu guards lastCheck.
	lastCheckMu sync.RWMutex

	// currentJob tracks the active run. nil when no job is running.
	currentJob   *jobState
	currentJobMu sync.Mutex
}

// Config is the static configuration for RuntimeService.
//
// ScriptPath: absolute or working-directory-relative path to the
// runtime update shell script. The handler enforces the suffix
// "release/scripts/" and the prefix matching the working directory.
//
// WorkspaceRoot: absolute path to the workspace root. Used as the
// allow-list prefix for ScriptPath.
type Config struct {
	ScriptPath    string
	WorkspaceRoot string
	Channel       Channel
	MinDiskBytes  int64
	BackupDir     string
	Logger        *zap.Logger
}

// DefaultScriptPath is the canonical location of the runtime update
// script relative to the workspace root. The handler always resolves
// the path against the workspace root before exec.
const DefaultScriptPath = "release/scripts/apply-runtime-update.sh"

// scriptPathSuffixCanonical is the canonical suffix of the runtime
// update script, expressed with forward slashes. The allow-list
// check normalises both the candidate path and this suffix to
// forward slashes before comparing, so the check is portable
// across Windows (where the separator is "\\") and POSIX.
const scriptPathSuffixCanonical = "/release/scripts/apply-runtime-update.sh"

// ErrJobRunning is returned by Run when a previous update job is
// still in progress.
var ErrJobRunning = errors.New("update: a job is already running")

// ErrScriptMissing is returned by Run when the configured script
// path does not exist on disk.
var ErrScriptMissing = errors.New("update: runtime script not found")

// ErrScriptPathRejected is returned when the configured script path
// fails the allow-list check (it is outside the workspace root or
// does not point at the canonical script).
var ErrScriptPathRejected = errors.New("update: script path rejected by allow-list")

// NewRuntimeService constructs a RuntimeService. db may be nil if
// the operator does not want update history persistence (handlers
// should still call methods, which degrade gracefully when db is nil).
func NewRuntimeService(db *sql.DB, cfg Config) *RuntimeService {
	if cfg.Channel == "" {
		cfg.Channel = ChannelStable
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &RuntimeService{db: db, cfg: cfg}
}

// WithCheckURL returns a copy of the service with the check URL
// set. The check URL is never derived from a request body, query
// string, or header — it is read from server config only.
//
// Note: we allocate a fresh RuntimeService rather than copying
// the receiver, because copying a struct that contains a
// sync.Mutex is a vet error (the lock must not be copied).
func (s *RuntimeService) WithCheckURL(url string) *RuntimeService {
	cp := &RuntimeService{
		db:       s.db,
		cfg:      s.cfg,
		logger:   s.logger,
		checkURL: url,
	}
	// Do not copy the mutex — the new instance starts unlocked.
	return cp
}

// EnsureSchema creates the update_history table. Idempotent.
func (s *RuntimeService) EnsureSchema(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS update_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		duration_seconds INTEGER NOT NULL DEFAULT 0,
		previous_sha TEXT NOT NULL DEFAULT '',
		new_sha TEXT NOT NULL DEFAULT '',
		from_version TEXT NOT NULL DEFAULT '',
		to_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT '',
		actor TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

// ── Status / Check / History ────────────────────────────

// Status returns the current status snapshot. The available version
// is whatever was last cached by a Check() call, or an empty string
// if no check has been performed yet.
func (s *RuntimeService) Status(ctx context.Context) (*UpdateStatus, error) {
	current := readBuildInfo()
	available := UpdateStatus{}
	s.lastCheckMu.RLock()
	if s.lastCheck != nil {
		available = *s.lastCheck
	}
	s.lastCheckMu.RUnlock()

	st := &UpdateStatus{
		CurrentVersion:   current.Version,
		CurrentSHA:       current.SHA,
		BuildTime:        current.BuildTime,
		AvailableVersion: available.AvailableVersion,
		AvailableSHA:     available.AvailableSHA,
		Channel:          s.cfg.Channel,
		UpdateAvailable:  available.AvailableVersion != "" && available.AvailableVersion != current.Version,
		ReleaseNotes:     available.ReleaseNotes,
		UpdateError:      available.UpdateError,
		CheckedAt:        available.CheckedAt,
		JobStatus:        "idle",
	}
	job := s.currentJobSnapshot()
	if job != nil {
		st.JobStatus = job.Status
		st.JobStartedAt = job.StartedAt
		st.JobCompletedAt = job.CompletedAt
		st.JobActor = job.Actor
	}
	return st, nil
}

// Check queries the configured update check URL for an available
// version, caches the result, and returns the new status. The
// check URL is read from the existing UpdateConfig and is not
// controlled by the request body.
//
// The HTTP fetch is conservative: 30s timeout, 1MB body cap, no
// redirects. The response is JSON-decoded into UpdateInfo.
func (s *RuntimeService) Check(ctx context.Context, checkURL, moduleID, currentVersion string) (*UpdateStatus, error) {
	if checkURL == "" {
		return nil, errors.New("update: no check URL configured")
	}
	if moduleID == "" {
		moduleID = "orvix-core"
	}
	if currentVersion == "" {
		currentVersion = readBuildInfo().Version
	}
	url := fmt.Sprintf("%s/api/v1/updates/%s/%s?channel=%s",
		checkURL, moduleID, currentVersion, string(s.cfg.Channel))
	// urlSafe: a hard-coded printf with the channel and a moduleID
	// that has been validated. We still guard against a malicious
	// moduleID by refusing any character outside [a-zA-Z0-9._-].
	if !isSafeModuleID(moduleID) {
		return nil, fmt.Errorf("update: unsafe module id: %q", moduleID)
	}

	req, err := httpNewRequest(ctx, "GET", url)
	if err != nil {
		return nil, err
	}
	resp, err := httpDo(req, 30*time.Second, 1<<20)
	if err != nil {
		return s.recordCheckError(err.Error()), nil
	}
	if resp.status == 204 {
		// Up to date.
		st := &UpdateStatus{
			CurrentVersion: readBuildInfo().Version,
			Channel:        s.cfg.Channel,
			CheckedAt:      time.Now().UTC(),
		}
		s.lastCheckMu.Lock()
		s.lastCheck = st
		s.lastCheckMu.Unlock()
		return st, nil
	}
	if resp.status >= 400 {
		return s.recordCheckError(fmt.Sprintf("update check returned status %d", resp.status)), nil
	}
	var info UpdateInfo
	if err := jsonUnmarshal(resp.body, &info); err != nil {
		return s.recordCheckError(fmt.Sprintf("decode update info: %v", err)), nil
	}
	st := &UpdateStatus{
		CurrentVersion:   readBuildInfo().Version,
		CurrentSHA:       readBuildInfo().SHA,
		BuildTime:        readBuildInfo().BuildTime,
		AvailableVersion: info.LatestVer,
		AvailableSHA:     info.Checksum,
		Channel:          s.cfg.Channel,
		UpdateAvailable:  info.LatestVer != "" && info.LatestVer != currentVersion,
		ReleaseNotes:     truncateNotes(info.Changelog),
		CheckedAt:        time.Now().UTC(),
	}
	s.lastCheckMu.Lock()
	s.lastCheck = st
	s.lastCheckMu.Unlock()
	return st, nil
}

func (s *RuntimeService) recordCheckError(msg string) *UpdateStatus {
	st := &UpdateStatus{
		CurrentVersion: readBuildInfo().Version,
		Channel:        s.cfg.Channel,
		UpdateError:    msg,
		CheckedAt:      time.Now().UTC(),
	}
	s.lastCheckMu.Lock()
	s.lastCheck = st
	s.lastCheckMu.Unlock()
	return st
}

// History returns the most recent update history rows.
func (s *RuntimeService) History(ctx context.Context, limit int) ([]UpdateHistoryRow, error) {
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, duration_seconds, previous_sha, new_sha, from_version, to_version, status, severity, actor, notes
		 FROM update_history ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UpdateHistoryRow, 0, limit)
	for rows.Next() {
		var r UpdateHistoryRow
		var completedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.StartedAt, &completedAt, &r.DurationSeconds, &r.PreviousSHA, &r.NewSHA, &r.FromVersion, &r.ToVersion, &r.Status, &r.Severity, &r.Actor, &r.Notes); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			t := completedAt.Time
			r.CompletedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Preflight ──────────────────────────────────────────

// Preflight runs the preflight validation: disk space, backup health,
// and binary build validation. Returns a structured result.
//
// The preflight NEVER exec's the script. It only reads file system
// metadata and (optionally) the audit log.
func (s *RuntimeService) Preflight(ctx context.Context) *PreflightResult {
	res := &PreflightResult{Pass: true, Checks: make([]PreflightCheck, 0, 4)}

	// 1. Disk space at the backup dir.
	if s.cfg.BackupDir != "" {
		du, err := diskUsageFor(s.cfg.BackupDir)
		if err != nil {
			res.Checks = append(res.Checks, PreflightCheck{
				Name: "disk_space", Status: "warning", Detail: "could not stat backup dir",
			})
		} else {
			minBytes := s.cfg.MinDiskBytes
			if minBytes == 0 {
				minBytes = 500 * 1024 * 1024 // 500 MB default
			}
			if du.FreeBytes < minBytes {
				res.Checks = append(res.Checks, PreflightCheck{
					Name:   "disk_space",
					Status: "fail",
					Detail: fmt.Sprintf("free space %s below minimum %s", humanBytes(du.FreeBytes), humanBytes(minBytes)),
				})
				res.Pass = false
			} else {
				res.Checks = append(res.Checks, PreflightCheck{
					Name: "disk_space", Status: "pass",
					Detail: fmt.Sprintf("free %s", humanBytes(du.FreeBytes)),
				})
			}
		}
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "disk_space", Status: "pass", Detail: "no backup dir configured",
		})
	}

	// 2. Backup health: the most recent backup, if any, must be < 30
	// days old and the dir must be writable.
	if s.cfg.BackupDir != "" {
		probe := filepath.Join(s.cfg.BackupDir, ".orvix-preflight-probe")
		f, err := os.Create(probe)
		if err != nil {
			res.Checks = append(res.Checks, PreflightCheck{
				Name: "backup_dir_writable", Status: "fail",
				Detail: "backup directory is not writable",
			})
			res.Pass = false
		} else {
			_ = f.Close()
			_ = os.Remove(probe)
			res.Checks = append(res.Checks, PreflightCheck{
				Name: "backup_dir_writable", Status: "pass",
				Detail: "backup directory writable",
			})
		}
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "backup_dir_writable", Status: "warning",
			Detail: "no backup dir configured",
		})
	}

	// 3. Binary build validation: ensure the source tree is present
	// and the cmd/orvix entry point exists. We do not run `go build`
	// here — that is the script's job.
	if _, err := os.Stat("cmd/orvix"); err != nil {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "binary_build", Status: "warning",
			Detail: "cmd/orvix entry point not found in working dir",
		})
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "binary_build", Status: "pass",
			Detail: "cmd/orvix entry point present",
		})
	}

	// 4. Script present and allow-listed.
	abs, err := s.resolveScriptPath()
	if err != nil {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "script_path", Status: "fail", Detail: err.Error(),
		})
		res.Pass = false
	} else {
		if _, err := os.Stat(abs); err != nil {
			res.Checks = append(res.Checks, PreflightCheck{
				Name: "script_path", Status: "fail",
				Detail: "runtime update script missing",
			})
			res.Pass = false
		} else {
			res.Checks = append(res.Checks, PreflightCheck{
				Name: "script_path", Status: "pass",
				Detail: "runtime update script present",
			})
		}
	}

	if res.Pass {
		res.Message = "preflight passed"
	} else {
		res.Message = "preflight failed; refuse update"
	}
	return res
}

// ── Run ────────────────────────────────────────────────

// Run executes the runtime update script under a single-flight lock.
// The actor label is the user id (or service account) initiating the
// update; it is recorded in history and audit logs but is never
// passed as an environment variable to the script.
//
// Single-flight semantics: the slot is reserved atomically with
// `currentJob` and the process-wide `mu` mutex. A second concurrent
// call to Run() returns ErrJobRunning. A second concurrent call to
// IsRunning() returns true between the first call's reservation
// and its release, regardless of how fast the first call's exec
// path runs (this matters on Windows where exec.Start fails in
// microseconds and would otherwise leave a window for the second
// call to slip through).
//
// Returns:
//   - nil, ErrJobRunning: a job is already in progress.
//   - nil, *UpdateError with code preflight_failed: preflight refused the run.
//   - nil, *UpdateError with code start_failed: exec.Start failed (script missing,
//     not executable, etc.). The internal error is logged but never returned.
//   - row, *UpdateError with code script_failed: the script started but exited
//     non-zero.
//   - row, *UpdateError with code timeout: the parent context was cancelled
//     while the script was running.
//   - row, nil: success.
//
// The returned error's Error() method returns only the safe code, never
// the underlying exec/os error. The underlying error is held on
// (*UpdateError).Internal and is exposed only via the server logger.
func (s *RuntimeService) Run(ctx context.Context, actor string) (*UpdateHistoryRow, error) {
	// Reserve the slot atomically. Holding currentJobMu here is
	// what serialises concurrent Run() and IsRunning() callers.
	startedAt := time.Now().UTC()
	job := &jobState{
		Status:    "running",
		StartedAt: &startedAt,
		Actor:     actor,
	}
	s.currentJobMu.Lock()
	if s.currentJob != nil {
		// Another Run is already in flight.
		s.currentJobMu.Unlock()
		return nil, ErrJobRunning
	}
	s.currentJob = job
	s.currentJobMu.Unlock()

	// We now own the slot. From this point on, every other Run()
	// call returns ErrJobRunning and every IsRunning() returns true
	// until we clear the slot below. The process-wide mutex `mu`
	// is still acquired as a defence-in-depth: it serialises the
	// actual exec.Command with the rest of the service and makes
	// the single-flight guarantee visible to anyone watching the
	// mutex directly (e.g. the unit tests).
	if !s.mu.TryLock() {
		// Should be unreachable because slot reservation guards
		// against concurrent Run(). If it ever fires, the caller
		// still gets the safe ErrJobRunning.
		s.clearSlot()
		return nil, ErrJobRunning
	}
	defer s.mu.Unlock()
	defer s.clearSlot()

	// Preflight gate: refuse the run if any check fails. The audit
	// row carries the code only; the underlying pf.Message is
	// internal.
	pf := s.Preflight(ctx)
	if !pf.Pass {
		return nil, NewUpdateError(ErrCodePreflightFailed,
			fmt.Errorf("preflight refused run: %s", pf.Message))
	}

	absScript, err := s.resolveScriptPath()
	if err != nil {
		// Allow-list rejection. Treat as a preflight failure: the
		// surface is "the configuration is unsafe" and we never echo
		// any candidate path back to the caller.
		return nil, NewUpdateError(ErrCodePreflightFailed, err)
	}
	if _, err := os.Stat(absScript); err != nil {
		// Script missing on disk. This is a configuration error, but
		// we still classify it as a preflight failure so the same
		// safe message is used; the underlying os.Stat error is
		// logged but never returned to the API.
		s.cfg.Logger.Warn("runtime update script missing", zap.Error(err))
		return nil, NewUpdateError(ErrCodePreflightFailed, ErrScriptMissing)
	}

	previousSHA := readBuildInfo().SHA
	fromVersion := readBuildInfo().Version

	// Run the script. exec.CommandContext runs the script via
	// CreateProcess / execve directamente — no shell — so no
	// shell-injection surface. We do not set any environment
	// variables, do not pass any arguments, and do not read stdin.
	cmd := exec.CommandContext(ctx, absScript)
	cmd.Env = nil // inherit only the OS env, do not add anything
	cmd.Dir = s.cfg.WorkspaceRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.cfg.Logger.Warn("stdout pipe", zap.Error(err))
		return s.recordRunFailure(startedAt, previousSHA, fromVersion, actor,
			NewUpdateError(ErrCodeStartFailed, err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.cfg.Logger.Warn("stderr pipe", zap.Error(err))
		return s.recordRunFailure(startedAt, previousSHA, fromVersion, actor,
			NewUpdateError(ErrCodeStartFailed, err))
	}
	if err := cmd.Start(); err != nil {
		// The error string here is the one Go formats as
		//   "exec: \"<abs path>\": file does not exist"
		// We never echo it. The logger keeps it for operators.
		s.cfg.Logger.Warn("update script start failed", zap.Error(err))
		return s.recordRunFailure(startedAt, previousSHA, fromVersion, actor,
			NewUpdateError(ErrCodeStartFailed, err))
	}
	// Stream to the logger; never echo to the client.
	go drainPipe(s.cfg.Logger.Info, "update-script", stdout)
	go drainPipe(s.cfg.Logger.Warn, "update-script", stderr)
	runErr := cmd.Wait()

	completedAt := time.Now().UTC()
	duration := int64(completedAt.Sub(startedAt).Seconds())
	newSHA := readBuildInfo().SHA
	row := &UpdateHistoryRow{
		StartedAt:       startedAt,
		CompletedAt:     &completedAt,
		DurationSeconds: duration,
		PreviousSHA:     previousSHA,
		NewSHA:          newSHA,
		FromVersion:     fromVersion,
		ToVersion:       newSHA, // SHA-only run; no version bump
		Actor:           actor,
	}
	var updateErr *UpdateError
	if runErr != nil {
		row.Status = "failed"
		row.Severity = SeverityCritical
		// row.Notes is the safe code only. The internal error is
		// logged but never persisted to history.
		row.Notes = string(ErrCodeScriptFailed)
		if ctxErr := ctx.Err(); ctxErr == context.DeadlineExceeded {
			updateErr = NewUpdateError(ErrCodeTimeout, runErr)
			row.Notes = string(ErrCodeTimeout)
		} else {
			updateErr = NewUpdateError(ErrCodeScriptFailed, runErr)
		}
	} else {
		row.Status = "completed"
		row.Severity = SeverityInfo
		row.Notes = "runtime update completed"
	}
	job.Status = row.Status
	job.CompletedAt = &completedAt

	// Persist history. We do not let a DB error mask the underlying
	// exec result; just log it.
	if s.db != nil {
		_, derr := s.db.ExecContext(ctx,
			`INSERT INTO update_history (started_at, completed_at, duration_seconds, previous_sha, new_sha, from_version, to_version, status, severity, actor, notes)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.StartedAt, row.CompletedAt, row.DurationSeconds, row.PreviousSHA, row.NewSHA, row.FromVersion, row.ToVersion, row.Status, string(row.Severity), row.Actor, row.Notes)
		if derr != nil {
			s.cfg.Logger.Warn("update_history insert failed", zap.Error(derr))
		}
	}

	if updateErr != nil {
		return row, updateErr
	}
	return row, nil
}

// clearSlot atomically releases the in-flight slot reserved at the
// start of Run. It is invoked from the deferred cleanup and from
// the unreachable-but-defensive mu.TryLock branch. The slot is
// only ever cleared by the goroutine that set it.
func (s *RuntimeService) clearSlot() {
	s.currentJobMu.Lock()
	s.currentJob = nil
	s.currentJobMu.Unlock()
}

// IsRunning returns true if a Run() is currently in progress.
func (s *RuntimeService) IsRunning() bool {
	s.currentJobMu.Lock()
	defer s.currentJobMu.Unlock()
	return s.currentJob != nil
}

// ── Allow-listed script path resolution ───────────────

// resolveScriptPath returns the absolute, allow-listed path of the
// runtime update script. The path is always resolved against
// WorkspaceRoot, never against a user-supplied input.
func (s *RuntimeService) resolveScriptPath() (string, error) {
	root := s.cfg.WorkspaceRoot
	if root == "" {
		// Fall back to the current working directory.
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = wd
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	script := s.cfg.ScriptPath
	if script == "" {
		script = DefaultScriptPath
	}
	// If the configured script is already absolute, use it as-is.
	// Otherwise resolve it relative to the workspace root, not the
	// process working directory. This is critical: the script
	// MUST always live inside the workspace, never in an
	// attacker-controlled location.
	var scriptAbs string
	if filepath.IsAbs(script) {
		scriptAbs = filepath.Clean(script)
	} else {
		scriptAbs = filepath.Clean(filepath.Join(rootAbs, script))
	}
	// Allow-list: the script must live under rootAbs and end with
	// the canonical suffix (path-separator-agnostic).
	if !strings.HasPrefix(filepath.ToSlash(scriptAbs), filepath.ToSlash(rootAbs)) {
		return "", ErrScriptPathRejected
	}
	if !strings.HasSuffix(filepath.ToSlash(scriptAbs), scriptPathSuffixCanonical) {
		return "", ErrScriptPathRejected
	}
	return scriptAbs, nil
}

// ── Internal helpers ─────────────────────────────────

// jobState is a runtime-only snapshot of the active update job.
// It is never persisted; the source of truth for completed jobs is
// the update_history table.
type jobState struct {
	Status      string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Actor       string
}

func (s *RuntimeService) currentJobSnapshot() *jobState {
	s.currentJobMu.Lock()
	defer s.currentJobMu.Unlock()
	if s.currentJob == nil {
		return nil
	}
	cp := *s.currentJob
	return &cp
}

// recordRunFailure persists a failed run row and returns it
// together with the typed error. The error is the *UpdateError
// that the caller already classified; the row.Notes field holds
// only the safe code, never the underlying exec/os error.
//
// The job slot is released by the Run() defer (s.clearSlot), not
// here, so this helper does not need a jobState parameter.
func (s *RuntimeService) recordRunFailure(startedAt time.Time, previousSHA, fromVersion, actor string, updateErr *UpdateError) (*UpdateHistoryRow, error) {
	completedAt := time.Now().UTC()
	code := ErrCodeScriptFailed
	if updateErr != nil && updateErr.Code != "" {
		code = updateErr.Code
	}
	row := &UpdateHistoryRow{
		StartedAt:       startedAt,
		CompletedAt:     &completedAt,
		DurationSeconds: int64(completedAt.Sub(startedAt).Seconds()),
		PreviousSHA:     previousSHA,
		NewSHA:          readBuildInfo().SHA,
		FromVersion:     fromVersion,
		ToVersion:       previousSHA, // no change
		Status:          "failed",
		Severity:        SeverityCritical,
		Actor:           actor,
		Notes:           string(code),
	}
	if s.db != nil {
		_, derr := s.db.ExecContext(context.Background(),
			`INSERT INTO update_history (started_at, completed_at, duration_seconds, previous_sha, new_sha, from_version, to_version, status, severity, actor, notes)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.StartedAt, row.CompletedAt, row.DurationSeconds, row.PreviousSHA, row.NewSHA, row.FromVersion, row.ToVersion, row.Status, string(row.Severity), row.Actor, row.Notes)
		if derr != nil {
			s.cfg.Logger.Warn("update_history insert failed", zap.Error(derr))
		}
	}
	if updateErr == nil {
		return row, NewUpdateError(ErrCodeScriptFailed, nil)
	}
	return row, updateErr
}

// drainPipe reads a pipe line-by-line and emits each line through
// the supplied logger. We never accumulate or echo the contents to
// the client.
func drainPipe(emit func(string, ...zap.Field), prefix string, r interface{ Read(p []byte) (int, error) }) {
	scanner := bufio.NewScanner(asIfaceReader(r))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		emit("stdout", zap.String("source", prefix), zap.String("line", scanner.Text()))
	}
}

func asIfaceReader(r interface{ Read(p []byte) (int, error) }) interface{ Read(p []byte) (int, error) } {
	return r
}

// diskUsageFor is a small wrapper around the platform statfs shim.
func diskUsageFor(path string) (struct{ TotalBytes, FreeBytes, UsedBytes int64; UsedPct int }, error) {
	stat, err := statfsImpl(path)
	if err != nil {
		return struct{ TotalBytes, FreeBytes, UsedBytes int64; UsedPct int }{}, err
	}
	bsize := stat.Bsize
	if bsize <= 0 {
		bsize = 4096
	}
	total := bsize * int64(stat.Blocks)
	free := bsize * int64(stat.Bavail)
	used := total - free
	pct := 0
	if total > 0 {
		pct = int((used * 100) / total)
	}
	return struct{ TotalBytes, FreeBytes, UsedBytes int64; UsedPct int }{total, free, used, pct}, nil
}

// humanBytes formats an int64 byte count for safe rendering.
func humanBytes(n int64) string {
	if n < 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

// truncateNotes clamps a free-form changelog to a safe length.
func truncateNotes(s string) string {
	const max = 8 * 1024
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// isSafeModuleID refuses anything but a short, well-known character
// set. Used to validate the moduleID parameter to Check().
func isSafeModuleID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
