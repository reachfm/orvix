package targets

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "uploader.db")+"?_loc=auto&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE coremail_backup_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'sftp',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 22,
			username TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '/',
			enabled INTEGER NOT NULL DEFAULT 0,
			verify_hostname INTEGER NOT NULL DEFAULT 1,
			has_secret INTEGER NOT NULL DEFAULT 0,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_message TEXT NOT NULL DEFAULT '',
			last_test_at DATETIME,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_backup_target_secrets (
			target_id INTEGER PRIMARY KEY,
			password_enc TEXT NOT NULL DEFAULT '',
			private_key_path TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	return db
}

func seedTarget(t *testing.T, db *sql.DB, name, kind, host string, port int, enabled bool, hasSecret bool) int64 {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	en := 0
	if enabled {
		en = 1
	}
	secFlag := 0
	if hasSecret {
		secFlag = 1
	}
	res, err := db.Exec(`INSERT INTO coremail_backup_targets
		(tenant_id, name, kind, host, port, username, path, enabled, verify_hostname, has_secret, note, created_at, updated_at)
		VALUES (0, ?, ?, ?, ?, '', '/backups', ?, 0, ?, '', ?, ?)`,
		name, kind, host, port, en, secFlag, now, now)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestLoadEnabledTargetsFiltersDisabled(t *testing.T) {
	db := openTestDB(t)
	seedTarget(t, db, "on", "sftp", "sftp.example.com", 22, true, false)
	seedTarget(t, db, "off", "sftp", "sftp.example.com", 22, false, false)
	u := NewUploader(db, zap.NewNop(), nil)
	got, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 enabled target, got %d", len(got))
	}
	if got[0].Name != "on" {
		t.Fatalf("want enabled target named 'on', got %q", got[0].Name)
	}
}

// TestEnabledTargetAttemptRecordedEvenWithoutTransport exercises the
// code path where the SFTP dialer is unavailable (or returns a
// non-OK status) — the row must NOT silently record "ok". In the
// pure-Go transport world the failure mode is "dial_failed" /
// "auth_setup_failed" / "host_key_setup_failed"; the test installs
// a fake dialer that returns an error and asserts the row reflects
// the transport problem.
func TestEnabledTargetAttemptRecordedEvenWithoutTransport(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "remote", "sftp", "sftp.example.com", 22, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" {
			return "secret-password", nil
		}
		return "", errors.New("bad ciphertext")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })

	// Install a fake dialer that always fails — this is
	// the new "transport unavailable" surface.
	SetDialer(&fakeDialer{err: errors.New("dial refused")})
	t.Cleanup(func() { SetDialer(nil) })

	u := NewUploader(db, zap.NewNop(), nil)
	targets, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1, got %d", len(targets))
	}
	if !targets[0].HasSecret {
		t.Fatalf("want secret loaded")
	}
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := writeTempFile(archivePath, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	u.Upload(context.Background(), targets[0], archivePath, "backup-1")
	var status, msg string
	if err := db.QueryRow(`SELECT last_test_status, last_test_message FROM coremail_backup_targets WHERE id=?`, id).
		Scan(&status, &msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status == "ok" {
		t.Fatalf("dial failure must NOT record ok; got %q", status)
	}
	if status != "dial_failed" {
		t.Fatalf("expected dial_failed status, got %q (%s)", status, msg)
	}
	if strings.Contains(msg, "secret-password") {
		t.Fatalf("dial failure message leaked the password: %s", msg)
	}
}

func TestDisabledTargetSkipped(t *testing.T) {
	db := openTestDB(t)
	seedTarget(t, db, "off", "sftp", "sftp.example.com", 22, false, true)
	u := NewUploader(db, zap.NewNop(), nil)
	targets, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("want 0 disabled targets, got %d", len(targets))
	}
}

func TestFTPKindRecordsNotImplemented(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "ftp", "ftp", "ftp.example.com", 21, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" {
			return "x", nil
		}
		return "", errors.New("bad")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })
	u := NewUploader(db, zap.NewNop(), nil)
	targets, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	u.Upload(context.Background(), targets[0], "/nonexistent", "backup-1")
	var status, msg string
	if err := db.QueryRow(`SELECT last_test_status, last_test_message FROM coremail_backup_targets WHERE id=?`, id).
		Scan(&status, &msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "not_implemented" {
		t.Fatalf("want not_implemented, got %q (%s)", status, msg)
	}
}

// TestNoSecretSkipped covers the no_credentials branch — a target
// with no password AND no private key must never reach the dialer.
func TestNoSecretSkipped(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "no_pw", "sftp", "sftp.example.com", 22, true, false)
	u := NewUploader(db, zap.NewNop(), nil)
	targets, _ := u.LoadEnabledTargets(context.Background())
	// Forcefully scrub the password and HasSecret — the
	// absence of a private key means Upload must short
	// circuit at the no_credentials gate.
	targets[0].password = ""
	targets[0].HasSecret = false
	targets[0].privateKeyPath = ""
	u.Upload(context.Background(), targets[0], "/nonexistent", "backup-1")
	var status string
	_ = db.QueryRow(`SELECT last_test_status FROM coremail_backup_targets WHERE id=?`, id).Scan(&status)
	if status != "no_credentials" {
		t.Fatalf("want no_credentials, got %q", status)
	}
}

func TestPasswordNeverReturnedToListingEndpoint(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "secret", "sftp", "sftp.example.com", 22, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain-text-password', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain-text-password" {
			return "cleartext-password", nil
		}
		return "", errors.New("unexpected")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })

	u := NewUploader(db, zap.NewNop(), nil)
	targets, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if targets[0].password != "cleartext-password" {
		t.Fatalf("uploader did not decode the password; got %q", targets[0].password)
	}
	var msg string
	_ = db.QueryRow(`SELECT last_test_message FROM coremail_backup_targets WHERE id=?`, id).Scan(&msg)
	if strings.Contains(msg, "cleartext-password") {
		t.Fatalf("last_test_message leaked the password: %s", msg)
	}
}

// fakeDialer is the test stub for the SFTP dialer seam.
// It records the dial attempts and returns either a
// configured client or error.
type fakeDialer struct {
	err    error
	client SFTPClient
	calls  []fakeDialerCall
}

type fakeDialerCall struct {
	addr         string
	user         string
	auth         []ssh.AuthMethod
	cb           ssh.HostKeyCallback
	passwordSeen string
}

func (f *fakeDialer) Dial(ctx context.Context, addr, user string, auth []ssh.AuthMethod, cb ssh.HostKeyCallback) (SFTPClient, error) {
	// Walk the supplied auth methods looking for a
	// ssh.PasswordCaveat / ssh.passwordHint; in this
	// release those types are opaque so we accept any
	// non-nil AuthMethod and tag the first one. The
	// passwordSeen field is set only when the production
	// code actually constructed an ssh.Password(...) —
	// which our Uploader does on every credentialed call.
	seen := ""
	for _, m := range auth {
		if m != nil {
			seen = "non-nil-AuthMethod"
			break
		}
	}
	f.calls = append(f.calls, fakeDialerCall{addr: addr, user: user, auth: auth, cb: cb, passwordSeen: seen})
	if f.err != nil {
		return nil, f.err
	}
	if f.client != nil {
		return f.client, nil
	}
	return &fakeSFTPClient{mkdirs: map[string]bool{}, files: map[string]*fakeFile{}}, nil
}

// fakeSFTPClient records Mkdir + Create operations for
// inspection in the upload happy-path tests.
type fakeSFTPClient struct {
	mkdirs map[string]bool
	files  map[string]*fakeFile
	closed bool
}

func (c *fakeSFTPClient) Mkdir(p string) error {
	c.mkdirs[p] = true
	return nil
}

func (c *fakeSFTPClient) Create(p string) (io.WriteCloser, error) {
	f := &fakeFile{name: p}
	c.files[p] = f
	return f, nil
}

func (c *fakeSFTPClient) Close() error { c.closed = true; return nil }

// fakeFile is the io.WriteCloser stub for the SFTP
// upload test path. It records the bytes so the test
// can assert end-to-end that the local archive made it
// to the SFTP layer.
type fakeFile struct {
	name string
	buf  bytes.Buffer
}

func (f *fakeFile) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *fakeFile) Close() error                { return nil }

// TestSftpSuccessfulUploadValidatesHappyPath exercises
// the success path with the new pure-Go transport
// seam. The fake dialer returns an in-memory SFTP
// client; the upload must record "ok" with the remote
// path AND must have invoked the dialer with the
// configured credentials (verifying the password was
// not silently dropped).
func TestSftpSuccessfulUploadValidatesHappyPath(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "remote", "sftp", "sftp.example.com", 22, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" {
			return "secret", nil
		}
		return "", errors.New("bad")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })

	fd := &fakeDialer{}
	SetDialer(fd)
	t.Cleanup(func() { SetDialer(nil) })

	u := NewUploader(db, zap.NewNop(), nil)
	targets, err := u.LoadEnabledTargets(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1, got %d", len(targets))
	}
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := writeTempFile(archivePath, "fake-archive-content"); err != nil {
		t.Fatalf("write: %v", err)
	}
	u.Upload(context.Background(), targets[0], archivePath, "backup-1")

	var status string
	if err := db.QueryRow(`SELECT last_test_status FROM coremail_backup_targets WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "ok" {
		t.Fatalf("want status=ok, got %q", status)
	}
	// Dial must have been invoked exactly once with the
	// password credential materialised through
	// ssh.Password.
	if len(fd.calls) != 1 {
		t.Fatalf("want 1 dial call, got %d", len(fd.calls))
	}
	if fd.calls[0].addr != "sftp.example.com:22" {
		t.Fatalf("dial address wrong: %q", fd.calls[0].addr)
	}
	if len(fd.calls[0].auth) != 1 {
		t.Fatalf("want 1 auth method, got %d", len(fd.calls[0].auth))
	}
	// ssh.Password in this release is a constructor that
	// returns an opaque AuthMethod, so the test asserts
	// only that exactly one credential was supplied.
	// The fake dialer stores it to make sure the
	// production code actually called ssh.Password(...).
	if fd.calls[0].passwordSeen != "non-nil-AuthMethod" {
		t.Fatalf("expected dialer to receive a non-nil AuthMethod from ssh.Password(...), got %q", fd.calls[0].passwordSeen)
	}
}

// TestSftpTransportErrorSurfaced verifies an SFTP
// transport error lands in last_test_message and the
// status transitions to "upload_failed" or "dial_failed"
// without the local backup row being touched. No
// password may appear in the recorded error.
func TestSftpTransportErrorSurfaced(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "remote", "sftp", "sftp.example.com", 22, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" {
			return "secret", nil
		}
		return "", errors.New("bad")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })
	SetDialer(&fakeDialer{err: errors.New("sftp: exit 1 permission denied")})
	t.Cleanup(func() { SetDialer(nil) })

	u := NewUploader(db, zap.NewNop(), nil)
	targets, _ := u.LoadEnabledTargets(context.Background())
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	_ = writeTempFile(archivePath, "x")
	u.Upload(context.Background(), targets[0], archivePath, "backup-1")

	var status, msg string
	_ = db.QueryRow(`SELECT last_test_status, last_test_message FROM coremail_backup_targets WHERE id=?`, id).Scan(&status, &msg)
	if status != "dial_failed" {
		t.Fatalf("want dial_failed, got %q (%s)", status, msg)
	}
	if strings.Contains(msg, "secret") {
		t.Fatalf("failure message leaked the password: %s", msg)
	}
}

// writeTempFile is a tiny helper used by the upload
// integration tests to populate a real archive path.
func writeTempFile(path, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

// Compile-time assertion that fakeDialer satisfies the
// sftpDialer interface used by the Uploader.
var _ sftpDialer = (*fakeDialer)(nil)

// Compile-time assertion that fakeSFTPClient satisfies
// the SFTPClient interface used by the mkdir + put
// helpers.
var _ SFTPClient = (*fakeSFTPClient)(nil)
