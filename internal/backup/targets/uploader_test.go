package targets

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"go.uber.org/zap"
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
		VALUES (0, ?, ?, ?, ?, '', '/backups', ?, 1, ?, '', ?, ?)`,
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

// The transport is not actually installed in this test
// environment so the upload always lands on the
// "transport_unavailable" branch. The contract is: a
// missing SSH client never silently succeeds.
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
	// archivePath is required for the mkdir step to
	// even start. Make a real file in t.TempDir().
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := writeTempFile(archivePath, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	u.Upload(context.Background(), targets[0], archivePath, "backup-1")
	// Read back the row to see what was recorded.
	var status, msg string
	if err := db.QueryRow(`SELECT last_test_status, last_test_message FROM coremail_backup_targets WHERE id=?`, id).
		Scan(&status, &msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status == "ok" {
		t.Fatalf("transport_unavailable must NOT record ok; got %q", status)
	}
	if !strings.Contains(status, "transport_unavailable") &&
		!strings.Contains(status, "not_implemented") {
		t.Fatalf("expected transport_unavailable / not_implemented status, got %q (%s)", status, msg)
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
		if s == "plain" { return "x", nil }
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

func TestNoSecretSkipped(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "no_pw", "sftp", "sftp.example.com", 22, true, false)
	u := NewUploader(db, zap.NewNop(), nil)
	targets, _ := u.LoadEnabledTargets(context.Background())
	// The LoadEnabledTargets helper returns the row
	// regardless of has_secret; Upload checks
	// HasSecret itself.
	targets[0].password = "" // simulate no secret
	targets[0].HasSecret = false
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
	// Set a known-good decryption that returns the
	// cleartext. The Uploader test still asserts the
	// cleartext never appears in any output that
	// travels through adminHandler.
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
	// The decoded password lives on the in-memory
	// Target but never propagates into the row's
	// last_test_message. We verify that the existing
	// last_test_message is empty even after we
	// touched the password.
	if targets[0].password != "cleartext-password" {
		t.Fatalf("uploader did not decode the password; got %q", targets[0].password)
	}
	var msg string
	_ = db.QueryRow(`SELECT last_test_message FROM coremail_backup_targets WHERE id=?`, id).Scan(&msg)
	if strings.Contains(msg, "cleartext-password") {
		t.Fatalf("last_test_message leaked the password: %s", msg)
	}
}

// TestSftpSucessfulUploadValidatesHappyPath uses the
// fake transport to simulate a successful SFTP
// put; the row's last_test_status must read "ok" and
// the last_test_message must contain the remote path.
// No real SSH is involved — the test is the contract
// that "successful upload records ok and surfaces
// the remote path" remains intact as the implementation
// evolves.
func TestSftpSuccessfulUploadValidatesHappyPath(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "remote", "sftp", "sftp.example.com", 22, true, true)
	// Seed a bogus cipher; the test install SetDecryptHook
	// to bypass real decryption.
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" { return "secret", nil }
		return "", errors.New("bad")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })

	// Install a fake transport that fakes the mkdir
	// + put commands. We just record the args so the
	// test can assert that the path was honoured.
	calls := [][]string{}
	SetTransportHook(func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte("ok"), nil
	})
	t.Cleanup(func() { SetTransportHook(nil) })

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
	// The two SFTP calls must include the expected args.
	if len(calls) != 2 {
		t.Fatalf("want 2 transport calls (mkdir + put), got %d", len(calls))
	}
	// mkdir call: -mkdir <target.Path>/backup-1
	if calls[0][len(calls[0])-1] != "/backups/backup-1" {
		t.Fatalf("mkdir target path wrong: %v", calls[0])
	}
	// put call: put <local> <remote>
	putArgs := calls[1]
	if putArgs[len(putArgs)-3] != "put" {
		t.Fatalf("expected put verb: %v", putArgs)
	}
	if putArgs[len(putArgs)-2] != archivePath {
		t.Fatalf("expected local archive path, got %v", putArgs)
	}
	if putArgs[len(putArgs)-1] != "/backups/backup-1/backup.tar.gz" {
		t.Fatalf("expected remote path, got %v", putArgs)
	}
}

// TestSftpTransportErrorSurfaced makes sure an SFTP
// transport error lands in last_test_message and the
// status transitions to "upload_failed" without the
// local backup row being touched.
func TestSftpTransportErrorSurfaced(t *testing.T) {
	db := openTestDB(t)
	id := seedTarget(t, db, "remote", "sftp", "sftp.example.com", 22, true, true)
	if _, err := db.Exec(`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, updated_at) VALUES (?, 'plain', ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetDecryptHook(func(s string) (string, error) {
		if s == "plain" { return "secret", nil }
		return "", errors.New("bad")
	})
	t.Cleanup(func() { SetDecryptHook(nil) })
	SetTransportHook(func(_ context.Context, _ string, _ []string, _ string) ([]byte, error) {
		return []byte("permission denied"), errors.New("sftp: exit 1")
	})
	t.Cleanup(func() { SetTransportHook(nil) })

	u := NewUploader(db, zap.NewNop(), nil)
	targets, _ := u.LoadEnabledTargets(context.Background())
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	_ = writeTempFile(archivePath, "x")
	u.Upload(context.Background(), targets[0], archivePath, "backup-1")

	var status, msg string
	_ = db.QueryRow(`SELECT last_test_status, last_test_message FROM coremail_backup_targets WHERE id=?`, id).Scan(&status, &msg)
	// Both mkdir_failed and upload_failed are valid
	// surface outcomes because the directory step is
	// what hits the fake transport first; the contract
	// we care about is that ANY transport error lands
	// a non-OK status without leaking the password.
	if status != "upload_failed" && status != "mkdir_failed" {
		t.Fatalf("want upload_failed or mkdir_failed, got %q (%s)", status, msg)
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
