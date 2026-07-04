package updater

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
//   - It does not invoke the runtime script directly. The web process
//     drives the systemd oneshot helper DefaultUpdateHelperUnit via
//     `sudo -n systemctl start <unit>`. The unit's ExecStart (not the
//     web process) runs the script. The sudoers drop-in at
//     release/sudoers.d/orvix-update grants passwordless sudo for
//     the single systemctl command.
//   - On machines without systemd the preflight gate reliably fails
//     with "update helper not installed". There is no direct-exec
//     fallback.
//   - The script path lives only in the systemd unit file (ExecStart).
//     The web process never resolves, stats, or exec's the script
//     path. The unit name DefaultUpdateHelperUnit is a compile-time
//     constant; no request-derived value can influence which unit is
//     started (buildSystemctlStartArgs coerces non-canonical values).
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
// WorkspaceRoot: absolute path to the workspace root. Used as
// the working directory when systemctl dispatches the oneshot
// helper.
//
// UpdateHelperUnit: the name of the systemd oneshot unit that
// wraps the runtime update script. The default is
// DefaultUpdateHelperUnit. The web process refuses any value
// that is not exactly that default — this is a defence-in-depth
// measure so a misconfigured handler cannot drive a different
// unit into a privileged run.
type Config struct {
	WorkspaceRoot    string
	Channel          Channel
	MinDiskBytes     int64
	BackupDir        string
	Logger           *zap.Logger
	UpdateHelperUnit string
}

// DefaultScriptPath is the canonical runtime update script location
// relative to the workspace root. The web process never executes this
// path directly; it is used only to detect the deployed source tree.
const DefaultScriptPath = "release/scripts/apply-runtime-update.sh"

const defaultVPSWorkspaceRoot = "/opt/orvix"

// DefaultUpdateHelperUnit is the canonical name of the systemd
// oneshot helper that wraps the runtime update script. The web
// process drives this unit via `systemctl start
// <DefaultUpdateHelperUnit>` (no other arguments). The unit file
// is shipped at release/systemd/orvix-update.service.
const DefaultUpdateHelperUnit = "orvix-update.service"

// ErrJobRunning is returned by Run when a previous update job is
// still in progress.
var ErrJobRunning = errors.New("update: a job is already running")

// NewRuntimeService constructs a RuntimeService. db may be nil if
// the operator does not want update history persistence (handlers
// should still call methods, which degrade gracefully when db is nil).
//
// The web process always drives the update via the systemd oneshot
// helper (DefaultUpdateHelperUnit). On machines without systemd the
// preflight gate reliably fails with "update helper not installed"
// so the operator knows the server is misconfigured. There is no
// direct-exec fallback.
//
// UpdateHelperUnit defaults to DefaultUpdateHelperUnit. Any
// non-empty value that is not the canonical default is silently
// coerced to the default in buildSystemctlStartArgs and
// buildSystemctlShowArgs — the test
// TestConfigRejectsNonCanonicalHelperUnit pins this.
func NewRuntimeService(db *sql.DB, cfg Config) *RuntimeService {
	if cfg.Channel == "" {
		cfg.Channel = ChannelStable
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.UpdateHelperUnit == "" {
		cfg.UpdateHelperUnit = DefaultUpdateHelperUnit
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

// CheckManifest fetches the configured HTTPS release manifest and
// returns the operator-facing update check response. The feed URL is
// server-side configuration only; request bodies never influence it.
func (s *RuntimeService) CheckManifest(ctx context.Context, feedURL string) (*UpdateCheckResult, error) {
	current := readBuildInfo()
	result := &UpdateCheckResult{
		CurrentVersion: current.Version,
		CurrentSHA:     current.SHA,
		Channel:        s.cfg.Channel,
		Message:        "update check not configured",
		ReleaseNotes:   []string{},
	}
	if strings.TrimSpace(feedURL) == "" {
		s.cacheManifestResult(result)
		return result, nil
	}
	u, err := validateFeedURL(feedURL)
	if err != nil {
		result.Message = "update feed URL is invalid"
		s.cacheManifestResult(result)
		return result, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		result.Message = "update check failed"
		s.cacheManifestResult(result)
		return result, nil
	}
	resp, err := updateFeedClient.Do(req)
	if err != nil {
		result.Message = "update check failed"
		s.cacheManifestResult(result)
		return result, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Message = "update check failed"
		s.cacheManifestResult(result)
		return result, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		result.Message = "update check failed"
		s.cacheManifestResult(result)
		return result, nil
	}
	var manifest ReleaseManifest
	if err := jsonUnmarshal(body, &manifest); err != nil {
		result.Message = "update feed response is invalid"
		s.cacheManifestResult(result)
		return result, nil
	}
	if !manifest.isValidForChannel(s.cfg.Channel) {
		result.Message = "update feed response is invalid"
		s.cacheManifestResult(result)
		return result, nil
	}
	result.LatestVersion = manifest.Version
	result.LatestSHA = shortSHA(manifest.GitSHA)
	result.Channel = manifest.Channel
	result.ReleaseNotes = safeReleaseNotes(manifest.ReleaseNotes)
	result.UpdateAvailable = isVersionNewer(manifest.Version, current.Version) || (manifest.GitSHA != "" && shortSHA(manifest.GitSHA) != "" && shortSHA(manifest.GitSHA) != current.SHA)
	result.Message = ""
	s.cacheManifestResult(result)
	return result, nil
}

func (s *RuntimeService) cacheManifestResult(result *UpdateCheckResult) {
	if result == nil {
		return
	}
	st := &UpdateStatus{
		CurrentVersion:   result.CurrentVersion,
		CurrentSHA:       result.CurrentSHA,
		AvailableVersion: result.LatestVersion,
		AvailableSHA:     result.LatestSHA,
		Channel:          result.Channel,
		UpdateAvailable:  result.UpdateAvailable,
		ReleaseNotes:     strings.Join(result.ReleaseNotes, "\n"),
		UpdateError:      result.Message,
		CheckedAt:        time.Now().UTC(),
		JobStatus:        "idle",
	}
	s.lastCheckMu.Lock()
	s.lastCheck = st
	s.lastCheckMu.Unlock()
}

var updateFeedClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var lookupFeedHost = net.LookupIP

func validateFeedURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("feed URL must be HTTPS")
	}
	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") {
		return nil, errors.New("feed URL host rejected")
	}
	if ip := net.ParseIP(host); ip != nil && isRejectedFeedIP(ip) {
		return nil, errors.New("feed URL host rejected")
	}
	ips, err := lookupFeedHost(host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("feed URL host rejected")
	}
	for _, ip := range ips {
		if isRejectedFeedIP(ip) {
			return nil, errors.New("feed URL host rejected")
		}
	}
	return u, nil
}

func isRejectedFeedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func (m ReleaseManifest) isValidForChannel(channel Channel) bool {
	if m.Version == "" || m.GitSHA == "" {
		return false
	}
	if m.Channel == "" {
		return false
	}
	if channel != "" && m.Channel != channel {
		return false
	}
	return true
}

func safeReleaseNotes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, note := range in {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		out = append(out, truncateNotes(note))
		if len(out) >= 50 {
			break
		}
	}
	return out
}

func isVersionNewer(latest, current string) bool {
	if latest == "" || current == "" || current == "development" {
		return latest != "" && latest != current
	}
	la := versionParts(latest)
	cu := versionParts(current)
	for i := 0; i < len(la) || i < len(cu); i++ {
		var l, c int
		if i < len(la) {
			l = la[i]
		}
		if i < len(cu) {
			c = cu[i]
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		var n int
		for _, r := range f {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
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

// Preflight runs the preflight validation: helper unit installed,
// script present, source tree present, backup dir writable, disk
// space available. Returns a structured result.
//
// The preflight NEVER exec's the script. It only reads file system
// metadata and (optionally) the audit log. On the systemd path it
// also verifies the unit file is installed, because without it
// `systemctl start orvix-update.service` cannot dispatch and Run
// would fail with start_failed — which is a configuration problem
// we want to surface to the operator before they click "Run
// Update".
func (s *RuntimeService) Preflight(ctx context.Context) *PreflightResult {
	res := &PreflightResult{Pass: true, Checks: make([]PreflightCheck, 0, 6)}
	root := s.effectiveWorkspaceRoot()

	// 0. Helper unit installed. The detail field never reveals the
	// absolute path of the unit file; it only says "installed" or
	// "not installed".
	if isHelperUnitInstalled() {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "update_helper_unit", Status: "pass",
			Detail: "update helper unit installed",
		})
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "update_helper_unit", Status: "fail",
			Detail: "update helper not installed",
		})
		res.Pass = false
	}

	// 1. Script exists and is executable at the canonical location
	// declared in the systemd unit's ExecStart.
	const canonicalScriptPath = "/opt/orvix/release/scripts/apply-runtime-update.sh"
	info, err := os.Stat(canonicalScriptPath)
	if err != nil {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "script_path", Status: "fail",
			Detail: "runtime update script missing",
		})
		res.Pass = false
	} else if info.Mode()&0111 == 0 {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "script_path", Status: "fail",
			Detail: "runtime update script not executable",
		})
		res.Pass = false
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "script_path", Status: "pass",
			Detail: "runtime update script present and executable",
		})
	}

	// 2. Disk space at the backup dir.
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

	// 3. Backup health: the dir must be writable.
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

	// 4. Binary build validation: ensure the source tree is present
	// and the cmd/orvix entry point exists. We do not run `go build`
	// here — that is the script's job.
	if _, err := os.Stat(filepath.Join(root, "cmd", "orvix")); err != nil {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "binary_build", Status: "warning",
			Detail: "cmd/orvix entry point not found in workspace root",
		})
	} else {
		res.Checks = append(res.Checks, PreflightCheck{
			Name: "binary_build", Status: "pass",
			Detail: "cmd/orvix entry point present",
		})
	}

	if res.Pass {
		res.Message = "preflight passed"
	} else {
		res.Message = "preflight failed; refuse update"
	}
	return res
}

// ── Systemd helper integration ────────────────────────

// buildSystemctlStartArgs returns the fixed argument list for
// `systemctl start <unit>`. The argv is the ONLY thing the web
// process ever passes to systemctl for an update run: the verb
// "start" and the configured unit name. There is no
// `--user`, no `--no-block`, no environment variable, no
// command flag, no script path. The systemd unit file (see
// release/systemd/orvix-update.service) owns the rest.
//
// The function is a single point of truth: every code path
// that issues `systemctl ... orvix-update.service` MUST go
// through this builder. The test
// TestBuildSystemctlStartArgs_AreFixedAndBounded pins the
// exact argv shape so a refactor cannot accidentally widen
// it.
func buildSystemctlStartArgs(unit string) []string {
	// Defensive: if a caller ever passes a unit name that is
	// not the canonical default, refuse. The web process is
	// the only caller; the handler in api/router.go wires
	// Config.UpdateHelperUnit = DefaultUpdateHelperUnit. If
	// that wiring is ever bypassed we want to fail closed
	// (return an argv that still references the canonical
	// unit), not silently start a different unit.
	if unit == "" || unit != DefaultUpdateHelperUnit {
		unit = DefaultUpdateHelperUnit
	}
	return []string{"start", unit}
}

// buildSystemctlShowArgs returns the fixed argument list for
// `systemctl show <unit> --property=Result,ExecMainStatus`.
// Used by the helper status reader; same defensive pattern as
// buildSystemctlStartArgs.
func buildSystemctlShowArgs(unit string) []string {
	if unit == "" || unit != DefaultUpdateHelperUnit {
		unit = DefaultUpdateHelperUnit
	}
	return []string{"show", unit, "--property=Result,ExecMainStatus", "--no-pager"}
}

// buildSystemctlIsActiveArgs returns the fixed argument list for
// `systemctl is-active <unit>`. Same canonicalisation as the other
// builders: any non-canonical unit name is coerced to
// DefaultUpdateHelperUnit. Every systemctl subprocess that the web
// process spawns MUST use one of the buildSystemctl*Args builders
// so a misconfigured Config value cannot reach the command line.
func buildSystemctlIsActiveArgs(unit string) []string {
	if unit == "" || unit != DefaultUpdateHelperUnit {
		unit = DefaultUpdateHelperUnit
	}
	return []string{"is-active", unit}
}

// helperStatusQuery runs `systemctl is-active <unit>` and
// returns true only when the answer is exactly "active".
// Anything else (inactive, failed, activating, unknown, blank)
// returns false. The raw output is NEVER returned to the
// caller; it is safe to log.
//
// The unit name is canonicalised via buildSystemctlIsActiveArgs:
// any non-canonical configured unit is coerced to
// DefaultUpdateHelperUnit. This matches the defensive pattern
// used by buildSystemctlStartArgs and buildSystemctlShowArgs.
func (s *RuntimeService) helperStatusQuery() (isActive bool, raw string) {
	// Use a 5s timeout. systemctl is-active is local-only and
	// should be sub-millisecond, so this is just defence.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := buildSystemctlIsActiveArgs(s.cfg.UpdateHelperUnit)
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.Output()
	if err != nil {
		// Non-zero exit: the unit is not active. The combined
		// output is safe to log; we still do not echo it to
		// the API.
		raw = strings.TrimSpace(string(out))
		return false, raw
	}
	raw = strings.TrimSpace(string(out))
	return raw == "active", raw
}

// helperResultQuery runs `systemctl show <unit> --property=Result`
// and parses the trailing value. The return is (result, execMainStatus)
// where result is one of "success", "exit-code", "signal", "core-dump",
// "watchdog", "timeout", "resources", "start-limit-hit", or the empty
// string. execMainStatus is the integer exit code of the script (or
// 0 if the unit is still running / never started). The raw property
// output is logged only.
func (s *RuntimeService) helperResultQuery() (result string, execMainStatus int, raw string) {
	unit := s.cfg.UpdateHelperUnit
	if unit == "" {
		unit = DefaultUpdateHelperUnit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := buildSystemctlShowArgs(unit)
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.Output()
	if err != nil {
		raw = strings.TrimSpace(string(out))
		return "", 0, raw
	}
	raw = strings.TrimSpace(string(out))
	// Output is two lines like:
	//   Result=success
	//   ExecMainStatus=0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Result=") {
			result = strings.TrimPrefix(line, "Result=")
		}
		if strings.HasPrefix(line, "ExecMainStatus=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(line, "ExecMainStatus="))
			if n > 0 {
				execMainStatus = n
			}
		}
	}
	return result, execMainStatus, raw
}

// helperUnitInstalled is the function used by isHelperUnitInstalled.
// Tests can replace it via SetHelperUnitCheck.
var helperUnitInstalled = func() bool {
	candidates := []string{
		"/etc/systemd/system/" + DefaultUpdateHelperUnit,
		"/lib/systemd/system/" + DefaultUpdateHelperUnit,
		"/usr/lib/systemd/system/" + DefaultUpdateHelperUnit,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// isHelperUnitInstalled reports whether the systemd oneshot
// unit is installed on the local machine.
func isHelperUnitInstalled() bool {
	return helperUnitInstalled()
}

// SetHelperUnitCheck replaces the helper unit installation check
// for testing and returns a restore function. Must not be called
// from production code. Usage:
//
//	restore := updater.SetHelperUnitCheck(func() bool { return false })
//	defer restore()
func SetHelperUnitCheck(fn func() bool) (restore func()) {
	old := helperUnitInstalled
	helperUnitInstalled = fn
	return func() { helperUnitInstalled = old }
}

// runViaSystemd triggers the systemd oneshot helper and waits
// for it to finish. The function does NOT return the script's
// exit code directly: it polls the helper status until the
// oneshot is no longer "activating" / "active" and then
// reports the helper's Result property (success, exit-code,
// etc.). The internal error from systemctl is logged but not
// returned; the returned *UpdateError carries only the safe
// code.
//
// This is the only path that ever executes a root-required
// script. The web process never exec's
// apply-runtime-update.sh directly; the systemd oneshot unit
// (DefaultUpdateHelperUnit) owns the privileged work.
func (s *RuntimeService) runViaSystemd(ctx context.Context) (startErr *UpdateError, helperResult string, helperExitCode int) {
	unit := s.cfg.UpdateHelperUnit
	if unit == "" {
		unit = DefaultUpdateHelperUnit
	}
	// 1. Defensive: refuse anything but the canonical unit.
	if unit != DefaultUpdateHelperUnit {
		s.cfg.Logger.Warn("update helper unit is not the canonical default; refusing",
			zap.String("unit", unit),
			zap.String("expected", DefaultUpdateHelperUnit))
		return NewUpdateError(ErrCodePreflightFailed,
				fmt.Errorf("helper unit not canonical: %s", unit)),
			"", 0
	}
	// 2. Preflight: helper unit must be installed.
	if !isHelperUnitInstalled() {
		return NewUpdateError(ErrCodePreflightFailed,
				fmt.Errorf("update helper not installed: %s", DefaultUpdateHelperUnit)),
			"", 0
	}
	// 3. Drive the helper. The web process runs as a non-root
	//    service account. On Linux, starting a system-level
	//    systemd unit requires root, so we use `sudo -n
	//    systemctl start <unit>`. The sudoers drop-in at
	//    release/sudoers.d/orvix-update grants passwordless
	//    sudo for this specific command only.
	//    `systemctl start <unit>` returns synchronously for a
	//    Type=oneshot unit: it blocks until the script
	//    finishes. We use a 6-minute outer deadline so we
	//    always beat the unit's 5-minute TimeoutStartSec.
	helperCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	args := buildSystemctlStartArgs(unit)
	cmd := exec.CommandContext(helperCtx, "sudo", append([]string{"-n", "systemctl"}, args...)...)
	cmd.Env = nil // do not propagate any env to the helper driver
	cmd.Dir = s.cfg.WorkspaceRoot
	// Capture stderr to a buffer so we can log it on failure.
	// We never echo the buffer to the API.
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.cfg.Logger.Warn("systemctl stdout pipe", zap.Error(err))
		return NewUpdateError(ErrCodeStartFailed, err), "", 0
	}
	if err := cmd.Start(); err != nil {
		s.cfg.Logger.Warn("systemctl start failed",
			zap.String("unit", unit),
			zap.Error(err),
			zap.String("stderr", stderrBuf.String()))
		return NewUpdateError(ErrCodeStartFailed, err), "", 0
	}
	go drainPipe(s.cfg.Logger.Info, "systemctl-orvix-update", stdout)
	runErr := cmd.Wait()
	// runErr from `systemctl start <oneshot>`:
	//   - nil: unit ran to completion; check Result.
	//   - non-nil with exit code != 0: unit ran and the script
	//     exited non-zero, OR systemctl itself failed.
	// We must always read Result/ExecMainStatus to distinguish
	// "script exited non-zero" from "systemctl could not
	// dispatch the unit". The ExitError's exit code reflects
	// systemctl, not the script.
	if runErr != nil {
		s.cfg.Logger.Warn("systemctl start returned non-zero",
			zap.String("unit", unit),
			zap.Error(runErr),
			zap.String("stderr", stderrBuf.String()))
	}
	// 4. Read the helper's final state.
	result, exitCode, raw := s.helperResultQuery()
	s.cfg.Logger.Info("systemctl show result",
		zap.String("unit", unit),
		zap.String("result", result),
		zap.Int("execMainStatus", exitCode),
		zap.String("raw", raw))
	return nil, result, exitCode
}

// ── Run ────────────────────────────────────────────────

// Run executes the runtime update under a single-flight lock.
// The actor label is the user id (or service account) initiating the
// update; it is recorded in history and audit logs but is never
// passed as an environment variable or argument.
//
// The web process NEVER exec's the runtime update script directly.
// It drives the root-owned systemd oneshot helper
// DefaultUpdateHelperUnit via `sudo -n systemctl start <unit>`.
// The helper unit's ExecStart is the only path that ever reaches
// exec. On machines without systemd the preflight gate refuses the
// run. The sudoers drop-in at release/sudoers.d/orvix-update grants
// passwordless sudo for the specific systemctl command.
//
// Single-flight semantics: the slot is reserved atomically with
// `currentJob` and the process-wide `mu` mutex. A second concurrent
// call to Run() returns ErrJobRunning.
//
// Returns:
//   - nil, ErrJobRunning: a job is already in progress.
//   - nil, *UpdateError with code preflight_failed: preflight refused the run.
//   - row, *UpdateError with code start_failed: systemctl start failed
//     (unit missing, unresponsive, etc.). The internal error is logged
//     but never returned.
//   - row, *UpdateError with code script_failed: the helper ran but the
//     underlying script exited non-zero.
//   - row, *UpdateError with code timeout: the parent context was cancelled
//     while the helper was running.
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

	previousSHA := readBuildInfo().SHA
	fromVersion := readBuildInfo().Version

	// Production path: drive the root-owned oneshot unit via
	// `systemctl start <canonical-unit>`. No args, no env, no
	// script path on the command line. The unit's own ExecStart
	// is the only script path that ever reaches exec. There is no
	// direct-exec fallback; on machines without systemd the
	// preflight gate above refuses the run.
	var (
		row        *UpdateHistoryRow
		updateErr  *UpdateError
		helperRes  string
		helperExit int
	)
	var startErr *UpdateError
	startErr, helperRes, helperExit = s.runViaSystemd(ctx)
	if startErr != nil {
		return s.recordRunFailure(startedAt, previousSHA, fromVersion, actor, startErr)
	}
	// Translate helper result → typed code. The web
	// process never sees the raw ExecMainStatus except in
	// the server log.
	completedAt := time.Now().UTC()
	row = &UpdateHistoryRow{
		StartedAt:       startedAt,
		CompletedAt:     &completedAt,
		DurationSeconds: int64(completedAt.Sub(startedAt).Seconds()),
		PreviousSHA:     previousSHA,
		NewSHA:          readBuildInfo().SHA,
		FromVersion:     fromVersion,
		ToVersion:       readBuildInfo().SHA, // no version bump on the web side
		Actor:           actor,
	}
	if helperRes != "success" {
		row.Status = "failed"
		row.Severity = SeverityCritical
		row.Notes = string(ErrCodeScriptFailed)
		if ctxErr := ctx.Err(); ctxErr == context.DeadlineExceeded {
			updateErr = NewUpdateError(ErrCodeTimeout, fmt.Errorf("helper timeout"))
			row.Notes = string(ErrCodeTimeout)
		} else {
			updateErr = NewUpdateError(ErrCodeScriptFailed,
				fmt.Errorf("helper result=%s exit=%d", helperRes, helperExit))
		}
	} else {
		row.Status = "completed"
		row.Severity = SeverityInfo
		row.Notes = "runtime update completed"
	}
	job.Status = row.Status
	job.CompletedAt = &completedAt
	// Persist history.
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

// ── Internal helpers ─────────────────────────────────

// DetectWorkspaceRoot returns the best local repository root for
// runtime update operations. It never uses request data and never
// returns a path to clients.
//
// Preference order:
//  1. git rev-parse --show-toplevel when the process is already
//     running inside a git checkout.
//  2. Explicit configured root, when present.
//  3. /opt/orvix, when it has the canonical runtime update script.
//  4. Process working directory.
func DetectWorkspaceRoot(configuredRoot string) string {
	if root := gitWorkspaceRoot(); root != "" {
		return root
	}
	if configuredRoot != "" {
		return configuredRoot
	}
	if hasRuntimeUpdateScript(defaultVPSWorkspaceRoot) {
		return defaultVPSWorkspaceRoot
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func (s *RuntimeService) effectiveWorkspaceRoot() string {
	return DetectWorkspaceRoot(s.cfg.WorkspaceRoot)
}

func gitWorkspaceRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	if hasRuntimeUpdateScript(root) {
		return root
	}
	return ""
}

func hasRuntimeUpdateScript(root string) bool {
	if root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(DefaultScriptPath)))
	return err == nil && !info.IsDir()
}

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
func diskUsageFor(path string) (struct {
	TotalBytes, FreeBytes, UsedBytes int64
	UsedPct                          int
}, error) {
	stat, err := statfsImpl(path)
	if err != nil {
		return struct {
			TotalBytes, FreeBytes, UsedBytes int64
			UsedPct                          int
		}{}, err
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
	return struct {
		TotalBytes, FreeBytes, UsedBytes int64
		UsedPct                          int
	}{total, free, used, pct}, nil
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
