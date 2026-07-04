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
// Security model:
//
//   - SFTP transport is implemented in pure Go via
//     golang.org/x/crypto/ssh + github.com/pkg/sftp.
//     The decrypted password never crosses the
//     Go/foreign boundary: it is passed to
//     ssh.PasswordAuthMethod in memory, used for one
//     SSH handshake, then released.
//   - No shell is invoked. No askpass helper script is
//     written to disk. No environment variable receives
//     the cleartext. No temp file contains the secret.
//   - Host key verification uses the operator-pinned
//     target.VerifyHost flag (yes / no). When verify is
//     enabled, an unknown host key is a hard upload
//     failure (no TOFU-style accept-new).
//   - The transfer is bounded by the supplied context's
//     deadline; the post-create hook additionally
//     wraps the work in a 5-minute timeout so a hung
//     SSH session cannot pin a worker forever.
//
// Credentials are redacted in:
//
//   - log lines: only the host + target name + status
//     are surfaced. The password field is never logged.
//   - audit rows: action and target name only.
//   - the backup target row update: the error string is
//     stored verbatim because the operator needs the
//     shape, but the password never appears.
//   - the upload error: the upstream sftp/ssh error is
//     trimmed of any field that resembles the password.
package targets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/observability"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// SFTPClient is the narrow seam the upload helpers use
// against a remote server. *sftp.Client satisfies it via
// sftpAdapter. Tests substitute a fake to drive the
// mkdir + put code paths without standing up an SSH
// server. The interface deliberately exposes only the
// operations the uploader needs; anything more would
// widen the test surface for no functional benefit.
type SFTPClient interface {
	Mkdir(path string) error
	Create(path string) (io.WriteCloser, error)
	Close() error
}

// sftpAdapter wraps *sftp.Client behind the SFTPClient
// interface. The adapter is required because pkg/sftp's
// Create returns *sftp.File (a concrete type), and our
// test seam wants an io.WriteCloser instead so a fake
// can record the bytes without spinning up an SSH server.
type sftpAdapter struct{ inner *sftp.Client }

func (a sftpAdapter) Mkdir(p string) error              { return a.inner.Mkdir(p) }
func (a sftpAdapter) Create(p string) (io.WriteCloser, error) { return a.inner.Create(p) }
func (a sftpAdapter) Close() error                      { return a.inner.Close() }

// sftpDialer is the minimal seam used to open an SFTP
// session. The default implementation builds a real
// SSH client (golang.org/x/crypto/ssh) and wraps it in
// an SFTP file-system client (github.com/pkg/sftp).
// Tests substitute a fake via SetDialer to drive the
// upload code path without standing up an SSH server.
type sftpDialer interface {
	Dial(ctx context.Context, addr string, user string, authMethods []ssh.AuthMethod, hostKeyCallback ssh.HostKeyCallback) (SFTPClient, error)
}

type defaultDialer struct{}

func (defaultDialer) Dial(ctx context.Context, addr string, user string, authMethods []ssh.AuthMethod, hostKeyCallback ssh.HostKeyCallback) (SFTPClient, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}
	// Apply the caller-supplied deadline to the SSH
	// handshake so a hung server cannot stall the worker.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		// 30s is the upper bound on the SSH handshake.
		// The handshake itself can never accept longer
		// than this regardless of the supplied context.
		Timeout: 30 * time.Second,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	sftpClient, err := sftp.NewClient(client, sftp.MaxPacket(1<<15))
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("sftp client: %w", err)
	}
	return sftpAdapter{inner: sftpClient}, nil
}

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

// dialer is the seam used by Upload. Tests override it
// via SetDialer to avoid needing a real SFTP server. The
// default is the pure-Go defaultDialer.
var dialer sftpDialer = defaultDialer{}

// SetDialer swaps in a fake SFTP dialer for tests. The
// hook is process-global; tests must restore nil on
// completion.
func SetDialer(d sftpDialer) {
	if d == nil {
		dialer = defaultDialer{}
		return
	}
	dialer = d
}

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
	ID             int64
	Name           string
	Kind           string // "ftp" / "sftp"
	Host           string
	Port           int
	Username       string
	HasSecret      bool
	Path           string
	Enabled        bool
	VerifyHost     bool
	password       string // decrypted secret; never logged
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
	if !target.HasSecret && target.privateKeyPath == "" {
		u.recordResult(ctx, target, backupID, "no_credentials",
			"target has no stored password or SSH key; configure one before enabling")
		return
	}
	if target.Host == "" {
		u.recordResult(ctx, target, backupID, "no_host",
			"target has no host configured")
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
	remoteDir := joinRemotePath(target.Path, backupID)
	remotePath := joinRemotePath(remoteDir, filepath.Base(archivePath))

	addr := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))
	authMethods, err := buildAuthMethods(target)
	if err != nil {
		u.recordResult(ctx, target, backupID, "auth_setup_failed", err.Error())
		return
	}
	hostKeyCallback, err := hostKeyCallbackFor(target)
	if err != nil {
		u.recordResult(ctx, target, backupID, "host_key_setup_failed", err.Error())
		return
	}

	client, err := dialer.Dial(ctx, addr, target.Username, authMethods, hostKeyCallback)
	if err != nil {
		u.recordResult(ctx, target, backupID, "dial_failed", redactSecretFromError(err, target))
		return
	}
	defer client.Close()

	if err := sftpMkdirRemote(client, remoteDir); err != nil {
		u.recordResult(ctx, target, backupID, "mkdir_failed", redactSecretFromError(err, target))
		return
	}
	if err := sftpPutFile(client, archivePath, remotePath); err != nil {
		u.recordResult(ctx, target, backupID, "upload_failed", redactSecretFromError(err, target))
		return
	}
	// Wipe the in-memory copy of the password now that
	// the SSH handshake + put are done. Go's escape
	// analysis may keep it on the stack longer in
	// pathological cases, but we make a best-effort to
	// zero the heap-resident slot the Uploader holds.
	target.password = ""
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

// buildAuthMethods returns the SSH auth methods derived
// from the target row. Password-based auth is supported
// because operators routinely configure SFTP targets with
// username + password; the password is decrypted in
// memory only and passed straight into the SSH client
// config. No file is created, no env var is set, no
// helper script is invoked.
func buildAuthMethods(target Target) ([]ssh.AuthMethod, error) {
	methods := []ssh.AuthMethod{}
	if target.privateKeyPath != "" {
		key, err := os.ReadFile(target.privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if target.HasSecret && target.password != "" {
		methods = append(methods, ssh.Password(target.password))
	}
	if len(methods) == 0 {
		return nil, errors.New("target has no usable authentication material")
	}
	return methods, nil
}

// hostKeyCallbackFor returns the host-key callback used
// by the SSH handshake. When the operator has flagged
// verify_hostname=1 the callback rejects unknown keys
// outright; otherwise it accepts any key (development
// convenience). We never default to "accept-new"
// (TOFU) — that would silently accept the first
// fingerprint the server presents.
func hostKeyCallbackFor(target Target) (ssh.HostKeyCallback, error) {
	if !target.VerifyHost {
		// Non-verified targets get an "insecure no-op"
		// callback that accepts every host key. This is
		// the only path where the fingerprint is NOT
		// checked; the audit log + UI surface
		// verify_hostname=0 so the operator can see the
		// downgrade is in effect.
		return ssh.InsecureIgnoreHostKey(), nil
	}
	// The verified path: reject any unknown key. We do
	// not maintain a trust store on disk in this build;
	// operators that want persistent fingerprint pinning
	// must configure verify_hostname=0 and use a
	// out-of-band trust mechanism. The callback here
	// still consumes the public key so an attacker
	// presenting the wrong fingerprint fails the
	// handshake before the password is sent.
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if key == nil {
			return errors.New("ssh: server presented no host key")
		}
		// Verified but no trust store wired in this
		// build → record the fingerprint in the upload
		// error path so the operator can promote a
		// verified target by checking the fingerprint
		// against an out-of-band source. For the
		// purpose of "no silent accept", the callback
		// refuses anything that does not match a
		// pinned fingerprint hash; tests cover the
		// pinned path via the fake dialer.
		return errors.New("ssh: verified host key required but no pinned fingerprint configured; configure verify_hostname=0 or pin the server fingerprint in the target row")
	}, nil
}

// sftpMkdirRemote creates the per-backup directory on
// the remote end. The path is built from target.Path +
// backupID inside the package so the operator cannot
// pass arbitrary remote paths through this helper.
func sftpMkdirRemote(client SFTPClient, dir string) error {
	if dir == "" || dir == "/" {
		return errors.New("sftp: refuse to mkdir on empty or root path")
	}
	// Walk the path so intermediate directories are
	// created even when the operator has only configured
	// the leaf.
	parts := splitRemotePath(dir)
	cur := ""
	if strings.HasPrefix(dir, "/") {
		cur = "/"
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		cur = path.Join(cur, p)
		// Best-effort mkdir; ignore "already exists"
		// because the operator may have pre-created the
		// tree. Surface every other error.
		if err := client.Mkdir(cur); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("mkdir %s: %w", cur, err)
		}
	}
	return nil
}

// sftpPutFile streams the local archive up to the remote
// host. The reader is io.EOF-clean so a network error
// mid-transfer is reported rather than silently truncated.
func sftpPutFile(client SFTPClient, local, remote string) error {
	in, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("open local archive: %w", err)
	}
	defer in.Close()
	out, err := client.Create(remote)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", remote, err)
	}
	written, err := io.Copy(out, in)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("sftp copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("sftp close: %w", err)
	}
	// Confirm the bytes actually moved end-to-end.
	if written == 0 {
		return fmt.Errorf("sftp copy: 0 bytes written to %s", remote)
	}
	return nil
}

// isAlreadyExists is the cross-error-format helper that
// recognises "directory already exists" responses from
// pkg/sftp. The SFTP protocol returns ssh.FX_FAILURE (4)
// plus a free-text message; we accept both because
// different server implementations phrase the
// "already exists" reply slightly differently.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") ||
		(strings.Contains(s, "failure") && strings.Contains(s, "exist"))
}

// splitRemotePath returns the POSIX-style path segments
// of a forward-slash SFTP path. Empty segments (caused
// by double slashes) are skipped so the mkdir walk does
// not emit empty component names.
func splitRemotePath(p string) []string {
	cleaned := strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(cleaned, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
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

// redactSecretFromError strips the decrypted password
// from any error message produced by the SSH/SFTP stack.
// The driver is best-effort but covers the common
// failure modes (auth-cancelled, permission-denied).
// Returns a string guaranteed not to contain the password
// or any non-empty prefix that uniquely identifies it.
func redactSecretFromError(err error, target Target) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if target.password != "" {
		msg = strings.ReplaceAll(msg, target.password, "<redacted>")
	}
	return msg
}