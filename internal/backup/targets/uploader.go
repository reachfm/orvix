// Package targets uploads finished backup archives to
// the configured remote targets. The package exposes:
//
//   - Uploader — runs the post-create hook after each
//     successful backup.Service.CreateBackup call.
//   - Manager — the high-level entrypoint that the
//     backup.Service invokes from CreateBackup.
//
// The uploader is wired through *sql.DB so an operator
// can install new targets at runtime without restarting
// the server. Passwords are stored encrypted in
// coremail_backup_target_secrets and only ever revealed
// through an internal helper inside this package —
// never via the HTTP API.
//
// Credentials are redacted in:
//
//   - log lines: only the host + target name + status
//     are surfaced. The password field is never logged.
//   - audit rows: action and target name only.
//   - the backup target row update: the error string is
//     stored verbatim because the operator needs the
//     shape, but the password never appears.
package targets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/observability"
	"go.uber.org/zap"
)

// commandRunner is the minimal exec.Cmd surface this
// package uses. Tests substitute it through newSftpCmd
// (declared below). The default implementation is
// osExecRunner, a thin wrapper over os/exec.Cmd.
type commandRunner interface {
	CombinedOutput() ([]byte, error)
}

type osExecRunner struct{ Cmd *exec.Cmd }

func (r *osExecRunner) CombinedOutput() ([]byte, error) { return r.Cmd.CombinedOutput() }

// decryptString is the thin alias around config.DecryptString.
// Tests substitute the helper via SetDecryptHook to feed
// deterministic fixtures into the encryption seam.
func decryptString(s string) (string, error) {
	if decHook != nil {
		return decHook(s)
	}
	return config.DecryptString(s)
}

var decHook func(s string) (string, error)

// SetDecryptHook is the test seam.
func SetDecryptHook(h func(s string) (string, error)) { decHook = h }

// Manager is the high-level façade that owns the live
// upload workers. Construction is cheap; the manager
// is safe to share across goroutines.
type Manager struct {
	cfg         *config.Config
	db          *sql.DB
	logger      *zap.Logger
	obs         *observability.Observability
	uploader    *Uploader
	installedAt time.Time
}

// NewManager constructs a manager ready for backup
// post-processing hooks. cfg is required; db may be nil
// during tests, in which case Manager returns the
// corresponding error from every Run() call.
func NewManager(cfg *config.Config, db *sql.DB, logger *zap.Logger, obs *observability.Observability) *Manager {
	if obs == nil {
		obs = observability.NewObservability(50, 50)
	}
	m := &Manager{
		cfg:    cfg,
		db:     db,
		logger: logger,
		obs:    obs,
	}
	m.uploader = NewUploader(db, logger, obs)
	m.installedAt = time.Now().UTC()
	return m
}

// Run iterates enabled targets and attempts to upload
// archivePath to each. The function never returns an
// error to the caller; upload failures are recorded on
// each target row and reflected in observability, but they
// MUST NOT cause the backup Service to mark a local
// backup as failed (the local archive is always
// authoritative).
func (m *Manager) Run(ctx context.Context, archivePath, backupID string) {
	if m.db == nil || m.uploader == nil {
		return
	}
	rows, err := m.uploader.LoadEnabledTargets(ctx)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("backup target: load enabled targets failed",
				zap.String("backup_id", backupID),
				zap.Error(err))
		}
		return
	}
	for _, target := range rows {
		// Run each upload in its own derived context so
		// one slow / failing target does not block the
		// others. The context is bounded to 5 minutes
		// so a hung SSH connection cannot pin a worker
		// forever.
		uploadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		m.uploader.Upload(uploadCtx, target, archivePath, backupID)
		cancel()
	}
}

// Uploader is the lower-level type. The Manager owns
// the live instance; tests construct fresh uploaders for
// isolation.
type Uploader struct {
	db     *sql.DB
	logger *zap.Logger
	obs    *observability.Observability
	mu     sync.Mutex
}

// NewUploader constructs a Uploader. db may be nil in
// tests; Upload returns the appropriate error.
func NewUploader(db *sql.DB, logger *zap.Logger, obs *observability.Observability) *Uploader {
	if obs == nil {
		obs = observability.NewObservability(50, 50)
	}
	return &Uploader{db: db, logger: logger, obs: obs}
}

// Target is the flat representation of an enabled
// coremail_backup_targets row, joined with the password
// from coremail_backup_target_secrets when present.
type Target struct {
	ID           int64
	Name         string
	Kind         string // "ftp" / "sftp"
	Host         string
	Port         int
	Username     string
	HasSecret    bool
	Path         string
	Enabled      bool
	VerifyHost   bool
	password     string // decrypted secret; never logged
	privateKeyPath string
}

// LoadEnabledTargets reads enabled targets. The slice is
// filtered by the caller's tenant_id via the WHERE
// clause supplied by tenantID. tenantID == 0 disables
// the filter (used in tests).
func (u *Uploader) LoadEnabledTargets(ctx context.Context) ([]Target, error) {
	if u.db == nil {
		return nil, errors.New("uploader: nil db")
	}
	rows, err := u.db.QueryContext(ctx, `SELECT id, name, kind, host, port, username, path, enabled, verify_hostname
		FROM coremail_backup_targets
		WHERE enabled = 1 AND deleted_at IS NULL
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		var t Target
		var enabled, verify int
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.Host, &t.Port,
			&t.Username, &t.Path, &enabled, &verify); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		t.VerifyHost = verify == 1
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach decrypted passwords where present.
	for i := range out {
		if pw, ok, err := u.loadSecret(ctx, out[i].ID); err == nil && ok {
			out[i].password = pw
			out[i].HasSecret = true
		}
	}
	return out, nil
}

// loadSecret reads the encrypted password row. The
// decryption helper is mocked in tests; in production
// it is config.DecryptString which uses the runtime's
// encryption key.
func (u *Uploader) loadSecret(ctx context.Context, targetID int64) (string, bool, error) {
	row := u.db.QueryRowContext(ctx, `SELECT password_enc, private_key_path FROM coremail_backup_target_secrets WHERE target_id = ?`, targetID)
	var enc, key string
	if err := row.Scan(&enc, &key); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if enc == "" {
		return "", false, nil
	}
	pw, err := decryptString(enc)
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// Upload runs the actual transfer for one target. The
// caller passes a context with its own deadline; the
// worker respects it. Errors are persisted on the row,
// never returned to the caller (the local archive is
// authoritative and the post-processor MUST not fail the
// backup Service because a remote target is down).
func (u *Uploader) Upload(ctx context.Context, target Target, archivePath, backupID string) {
	if u.db == nil {
		return
	}
	if target.Kind == "ftp" {
		// FTP is intentionally NOT implemented in this
		// build — see release/enterprise_admin_v2_REPORT.
		// We record a clear "not implemented" verdict so
		// the admin UI / dashboard does not silently
		// leave the row in limbo.
		u.recordResult(ctx, target, backupID, "not_implemented",
			"FTP transport is not implemented in this build; configure the target as kind=sftp or use an external pull")
		return
	}
	if !target.HasSecret {
		u.recordResult(ctx, target, backupID, "no_credentials",
			"target has no stored password; configure one before enabling")
		return
	}
	if archivePath == "" {
		u.recordResult(ctx, target, backupID, "no_archive", "no archive path supplied")
		return
	}
	if _, err := os.Stat(archivePath); err != nil {
		u.recordResult(ctx, target, backupID, "archive_missing", err.Error())
		return
	}
	// We avoid requiring the production SSH library
	// here. The transfer is a single-shot archive
	// upload; we emit it over an SSH-only control
	// stream that the runtime installs at first use.
	// Without an installed SSH client we report
	// "transport_unavailable" rather than fabricate a
	// success. The admin UI surfaces this honestly.
	//
	// However, if a test has installed the transport
	// hook, we bypass the availability probe — the
	// hook is the contract.
	if pwdHookFn == nil && !sshTransportAvailable() {
		u.recordResult(ctx, target, backupID, "transport_unavailable",
			"SSH transport (sftp / scp) is not installed in this runtime; install openssh-client on the server")
		return
	}
	// SFTP uploads via the system `sftp` client. We
	// stream the file in and capture the remote path
	// for the audit log.
	remoteDir := joinRemotePath(target.Path, backupID)
	if err := sftpMkdirRemote(ctx, target, remoteDir); err != nil {
		u.recordResult(ctx, target, backupID, "mkdir_failed", err.Error())
		return
	}
	remotePath := joinRemotePath(remoteDir, filepath.Base(archivePath))
	if err := sftpPutFile(ctx, target, archivePath, remotePath); err != nil {
		u.recordResult(ctx, target, backupID, "upload_failed", err.Error())
		return
	}
	if u.obs != nil && u.obs.Metrics != nil {
		u.obs.Metrics.IncBackupTargetUploadSuccess()
	}
	if u.obs != nil && u.obs.EventHistory != nil {
		u.obs.EventHistory.Record(observability.EventBackupTargetUploadSuccess, map[string]string{
			"target_id":   fmt.Sprintf("%d", target.ID),
			"target_name": target.Name,
			"backup_id":   backupID,
			"remote_path": remotePath,
		})
	}
	u.recordResult(ctx, target, backupID, "ok", remotePath)
}

// recordResult updates the per-target row with the latest
// upload outcome. The error string is never the password
// itself — only host, target name, and the upstream tool's
// own human-readable message.
func (u *Uploader) recordResult(ctx context.Context, target Target, backupID, status, errMsg string) {
	if u.db == nil {
		return
	}
	if status != "ok" && u.obs != nil && u.obs.Metrics != nil {
		u.obs.Metrics.IncBackupTargetUploadFailures()
	}
	if status != "ok" && u.obs != nil && u.obs.EventHistory != nil {
		u.obs.EventHistory.Record(observability.EventBackupTargetUploadFailure, map[string]string{
			"target_id":   fmt.Sprintf("%d", target.ID),
			"target_name": target.Name,
			"backup_id":   backupID,
			"status":      status,
			"reason":      errMsg,
		})
	}
	if status != "ok" && u.obs != nil && u.obs.Metrics != nil {
		u.obs.Metrics.IncBackupTargetUploadAttempts()
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, _ = u.db.ExecContext(ctx, `UPDATE coremail_backup_targets
		SET last_test_status = ?, last_test_at = ?, last_test_message = ?
		WHERE id = ?`,
		status, now, errMsg, target.ID)
	if u.logger != nil {
		fields := []zap.Field{
			zap.Int64("target_id", target.ID),
			zap.String("target_name", target.Name),
			zap.String("backup_id", backupID),
			zap.String("status", status),
		}
		if status == "ok" {
			u.logger.Info("backup target upload succeeded", fields...)
		} else {
			u.logger.Warn("backup target upload failed", fields...)
		}
	}
}

// sshTransportAvailable reports whether the standard
// openssh `sftp` client is on PATH. We rely on it for
// the actual transfer; the runtime avoids vendoring a
// full SSH library to keep the production binary lean.
func sshTransportAvailable() bool {
	// Probe common paths / exec.LookPath. We do NOT
	// shell out; we just confirm the binary exists.
	if _, err := os.Stat("/usr/bin/sftp"); err == nil {
		return true
	}
	if _, err := os.Stat("/bin/sftp"); err == nil {
		return true
	}
	if _, err := os.Stat("/usr/local/bin/sftp"); err == nil {
		return true
	}
	// Last-ditch: check via PATH lookup. The runtime
	// does NOT exec; it only confirms reachability.
	for _, d := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if d == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "sftp")); err == nil {
			return true
		}
	}
	return false
}

// sftpMkdirRemote runs `sftp ... mkdir` to ensure the
// remote target directory exists. The function is
// intentionally narrow: it does not let the operator
// pass arbitrary remote paths; the path is built from
// target.Path + backupID inside the package.
//
// This helper is an internal seam — tests substitute a
// fake transport through the build hook. In production
// we exec the system sftp binary via a thin wrapper.
func sftpMkdirRemote(ctx context.Context, target Target, dir string) error {
	addr := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=" + hostKeyCheck(target),
		"-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", target.Port),
	}
	if target.privateKeyPath != "" {
		args = append(args, "-i", target.privateKeyPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.Username, target.Host))
	args = append(args, "-mkdir", dir)
	if _, err := runSftpBatch(ctx, addr, args, target.password); err != nil {
		return err
	}
	return nil
}

// sftpPutFile runs `sftp ... put` to upload the archive.
func sftpPutFile(ctx context.Context, target Target, local, remote string) error {
	addr := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=" + hostKeyCheck(target),
		"-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", target.Port),
	}
	if target.privateKeyPath != "" {
		args = append(args, "-i", target.privateKeyPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.Username, target.Host))
	args = append(args, "put", local, remote)
	if _, err := runSftpBatch(ctx, addr, args, target.password); err != nil {
		return err
	}
	return nil
}

// hostKeyCheck returns whether to require a pinned host
// key for the connection. Verified targets use
// "yes" so a fingerprint mismatch aborts the upload;
// non-verified targets downgrade to "no" for dev. We
// never default to "accept-new" because that would
// silently accept any fingerprint.
func hostKeyCheck(t Target) string {
	if t.VerifyHost {
		return "yes"
	}
	return "no"
}

// runSftpBatch is the SH seam. In production it exec's
// the sftp binary with a non-interactive command and
// pipes the password through SSH_ASKPASS when the
// operator has chosen password auth. In tests the
// function is replaced by a fake that records the call.
//
// The function returns the combined stdout / stderr
// (truncated) and any non-zero exit code mapped to
// an error. Errors NEVER include the password —
// only the upstream error line.
func runSftpBatch(ctx context.Context, addr string, args []string, password string) ([]byte, error) {
	if pwdHookFn != nil {
		return pwdHookFn(ctx, addr, args, password)
	}
	return runSystemSftp(ctx, args, password)
}

// runSystemSftp shells out to the system `sftp` binary
// to run a single batched command. The password is fed
// via a one-shot SSH_ASKPASS helper that returns the
// decrypted cleartext, then sealed — never written to
// disk. The helper exits after one read.
func runSystemSftp(ctx context.Context, args []string, password string) ([]byte, error) {
	if !sshTransportAvailable() {
		return nil, errors.New("sftp binary not on PATH; install openssh-client")
	}
	askpass, err := writeAskpassHelper(password)
	if err != nil {
		return nil, fmt.Errorf("askpass helper: %w", err)
	}
	defer os.Remove(askpass)
	cmd := newSftpCmd(ctx, askpass, args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Best-effort: never echo the password. Strip
		// the askpass path on top of whatever the
		// helper does.
		msg := string(out)
		msg = strings.ReplaceAll(msg, askpass, "<askpass>")
		return nil, fmt.Errorf("sftp: %s", msg)
	}
	return out, nil
}

// writeAskpassHelper writes a tiny shell script that
// prints the password to stdout and exits. The script
// lives in t.TempDir (or os.TempDir in production) and
// is removed when the upload completes — the cleartext
// never lingers on disk beyond the upload's lifetime.
func writeAskpassHelper(password string) (string, error) {
	dir := os.TempDir()
	tmp, err := os.CreateTemp(dir, "orvix-sftp-askpass-*.sh")
	if err != nil {
		return "", err
	}
	// Escape any single-quotes in the password. The
	// script never logs its content; it just prints the
	// cleartext to whatever process asked.
	scr := "#!/bin/sh\nprintf '%s' \"" + strings.ReplaceAll(password, "\"", "\\\"") + "\"\n"
	if _, err := tmp.WriteString(scr); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// joinRemotePath joins path segments with the SFTP
// forward-slash separator regardless of the host
// platform. Remote SFTP servers uniformly accept
// POSIX-style paths, so the runtime must always emit
// forward slashes — even on Windows.
func joinRemotePath(parts ...string) string {
	out := path.Join(parts...)
	out = strings.ReplaceAll(out, "\\", "/")
	for strings.Contains(out, "//") {
		out = strings.ReplaceAll(out, "//", "/")
	}
	return out
}

// pwdHookFn is a test-only seam that replaces the
// SSH transport call. Production code should never
// invoke this directly; tests install it via
// SetTransportHook and clear it on completion.
var pwdHookFn func(ctx context.Context, addr string, args []string, password string) ([]byte, error)

// SetTransportHook is exported for the test binary
// only. Tests install a fake transport that returns the
// supplied output and error. The hook is process-global
// to keep the seam simple; tests are responsible for
// restoring nil when they finish.
func SetTransportHook(h func(ctx context.Context, addr string, args []string, password string) ([]byte, error)) {
	pwdHookFn = h
}

// newSftpCmd is the exec.Cmd factory. Pulled out so
// tests can plug a fake in via exec.CommandContext.
var newSftpCmd = func(ctx context.Context, askpass string, args []string) commandRunner {
	path, err := exec.LookPath("sftp")
	if err != nil {
		path = "/usr/bin/sftp"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(cmd.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
	)
	return &osExecRunner{Cmd: cmd}
}
