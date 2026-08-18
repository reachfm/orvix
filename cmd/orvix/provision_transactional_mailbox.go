package main

// orvix admin provision-transactional-mailbox
//
// Root-only, one-shot provisioning command for the platform's own outbound
// transactional mailbox (OTP, password reset, support-request mail — see
// internal/api/mail_sender.go). It exists because those flows require a
// REAL coremail_mailboxes row to authenticate SMTP submission (SMTP AUTH is
// mailbox-credential based; there is no lighter-weight service-account
// mechanism — see internal/coremail/smtp/identity.go), and no such mailbox
// existed on this instance.
//
// Safety contract:
//   - refuses to run unless the process is root;
//   - NEVER creates a domain row and NEVER touches DKIM. It only resolves
//     an EXISTING coremail_domains row by name and fails closed if none is
//     found — creating a domain via the normal admin flow defaults to
//     generating a fresh DKIM keypair, which would silently invalidate the
//     live DKIM signature the target domain's real mail already depends on.
//   - idempotent: if a mailbox for the target address already exists, it
//     reports the existing row and makes no changes — it never resets an
//     existing password.
//   - generates the mailbox password with crypto/rand and hashes it with
//     the same auth.HashPassword (Argon2id) the rest of the application
//     uses — no new format, no plaintext persisted.
//   - the plaintext password is NEVER printed, logged, or returned to the
//     caller. It is written once, directly, to a root-only (0600) env file
//     for systemd to source — see writeTransactionalCredentialsEnvFile.
//   - the mailbox row insert and the audit row insert happen inside one
//     transaction.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/dbdialect"
)

var (
	errProvisionNotRoot        = errors.New("this command must be run as root")
	errProvisionDomainNotFound = errors.New("no existing, non-deleted coremail_domains row found for that domain name; this command never creates a domain")
	errProvisionBadConfirm     = errors.New("confirmation refused")
)

// transactionalCredentialsEnvPath is the root-only env file that
// orvix.service's systemd unit sources (via an EnvironmentFile= drop-in)
// to pick up COREMAIL_TRANSACTIONAL_SMTP_USERNAME/PASSWORD. Kept out of
// /etc/orvix/orvix.yaml (never written into version-controlled or
// human-edited config) and out of git entirely. A var (not const) solely
// so tests can point it at a temp file instead of the real root-only path.
var transactionalCredentialsEnvPath = "/etc/orvix/coremail-transactional.env"

func runAdminProvisionTransactionalMailbox(args []string, deps adminCLIDeps) int {
	fs := flag.NewFlagSet("admin provision-transactional-mailbox", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	domainName := fs.String("domain", "", "existing hosted mail domain (required, e.g. orvix.email)")
	localPart := fs.String("local-part", "noreply", "mailbox local part")
	confirm := fs.String("confirm", "", "type PROVISION-MAILBOX to confirm")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *domainName == "" {
		fmt.Fprintln(deps.stderr, "error: --domain is required")
		return 2
	}
	if *confirm != "PROVISION-MAILBOX" {
		fmt.Fprintln(deps.stderr, "error:", errProvisionBadConfirm)
		return 2
	}
	if !deps.isRoot() {
		fmt.Fprintln(deps.stderr, "error:", errProvisionNotRoot)
		return 1
	}

	sqlDB, dial, closeDB, err := deps.openDB()
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: unable to access the database")
		return 1
	}
	defer closeDB()

	ctx := context.Background()
	email := *localPart + "@" + *domainName

	// Read-only domain resolution. Never creates, never touches DKIM.
	var domainID, tenantID int64
	var dkimEnabled bool
	var dkimSelector string
	row := sqlDB.QueryRowContext(ctx,
		"SELECT id, tenant_id, dkim_enabled, dkim_selector FROM coremail_domains WHERE name="+dial.Placeholder(1)+" AND deleted_at IS NULL",
		*domainName,
	)
	if err := row.Scan(&domainID, &tenantID, &dkimEnabled, &dkimSelector); err != nil {
		fmt.Fprintln(deps.stderr, "error:", errProvisionDomainNotFound)
		return 1
	}
	fmt.Fprintf(deps.stdout, "resolved existing domain: id=%d tenant_id=%d dkim_enabled=%t dkim_selector=%q (unchanged)\n", domainID, tenantID, dkimEnabled, dkimSelector)

	// Idempotency: never overwrite an existing mailbox's password.
	var existingID int64
	err = sqlDB.QueryRowContext(ctx,
		"SELECT id FROM coremail_mailboxes WHERE email="+dial.Placeholder(1)+" AND deleted_at IS NULL",
		email,
	).Scan(&existingID)
	if err == nil {
		fmt.Fprintf(deps.stdout, "OK: mailbox already exists id=%d email=%s — no changes made (password not rotated)\n", existingID, email)
		return 0
	}
	if err != sql.ErrNoRows {
		fmt.Fprintln(deps.stderr, "error: unable to check existing mailboxes")
		return 1
	}

	password, err := generateServicePassword()
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: failed to generate password")
		return 1
	}
	hash, hashErr := auth.HashPassword(password)
	if hashErr != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error: failed to hash password")
		return 1
	}

	mailboxID, txErr := provisionMailboxTx(ctx, sqlDB, dial, domainID, tenantID, *localPart, email, hash, deps.now())
	if txErr != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error: mailbox provisioning failed; no changes were made")
		return 1
	}

	if err := writeTransactionalCredentialsEnvFile(email, password); err != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error: mailbox row created (id="+fmt.Sprint(mailboxID)+") but writing the credentials env file failed:", err)
		fmt.Fprintln(deps.stderr, "the mailbox exists but SMTP credentials were not persisted anywhere — investigate before retrying (retrying will hit the idempotency guard above).")
		return 1
	}
	password = ""

	fmt.Fprintf(deps.stdout, "OK: created mailbox id=%d email=%s domain_id=%d tenant_id=%d\n", mailboxID, email, domainID, tenantID)
	fmt.Fprintf(deps.stdout, "OK: credentials written to %s (root-only, 0600)\n", transactionalCredentialsEnvPath)
	fmt.Fprintln(deps.stdout, "next: point orvix.service at this file (EnvironmentFile=-"+transactionalCredentialsEnvPath+") and restart the service.")
	return 0
}

// generateServicePassword returns a cryptographically random, URL-safe
// password with well over 256 bits of entropy — long enough that SMTP
// AUTH PLAIN base64 framing and env-file quoting are both unambiguous.
func generateServicePassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func provisionMailboxTx(ctx context.Context, sqlDB *sql.DB, dial *dbdialect.Info, domainID, tenantID int64, localPart, email, passwordHash string, now time.Time) (int64, error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Re-check idempotency inside the transaction to close the TOCTOU
	// window between the pre-insert read and the write.
	var existing int64
	if qerr := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE email="+dial.Placeholder(1)+" AND deleted_at IS NULL",
		email,
	).Scan(&existing); qerr != nil {
		err = qerr
		return 0, err
	}
	if existing > 0 {
		err = fmt.Errorf("mailbox already exists")
		return 0, err
	}

	// 23 real (bound) values; used_bytes, msg_count, and version are
	// fixed literals (0, 0, 1) for a freshly created mailbox, matching
	// internal/coremail/mailbox.go's own Create(). Placeholder positions
	// are explicit (not dial.Placeholders(n), which always starts at 1)
	// so Postgres's $1..$23 numbering stays aligned with args' order —
	// SQLite's "?" ignores the number but position still must match.
	insert := fmt.Sprintf(`
		INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name,
			 password_hash, auth_scheme, mfa_enabled, mfa_secret, app_passwords,
			 status, quota_mb, used_bytes, msg_count,
			 is_admin, is_forwarder, forward_to, labels,
			 send_limit_per_hour, recv_limit_per_hour,
			 last_login, last_ip, mail_access_mode, version, created_at, updated_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, 0, 0, %s, %s, %s, %s, %s, %s, %s, %s, %s, 1, %s, %s)`,
		dial.Placeholder(1), dial.Placeholder(2), dial.Placeholder(3), dial.Placeholder(4), dial.Placeholder(5),
		dial.Placeholder(6), dial.Placeholder(7), dial.Placeholder(8), dial.Placeholder(9), dial.Placeholder(10),
		dial.Placeholder(11), dial.Placeholder(12),
		dial.Placeholder(13), dial.Placeholder(14), dial.Placeholder(15), dial.Placeholder(16),
		dial.Placeholder(17), dial.Placeholder(18),
		dial.Placeholder(19), dial.Placeholder(20), dial.Placeholder(21),
		dial.Placeholder(22), dial.Placeholder(23),
	)
	args := []interface{}{
		domainID, tenantID, localPart, email, "Transactional Mail", // 1-5
		passwordHash, "argon2id", false, "", "", // 6-10
		"active", int64(256), // 11-12
		false, false, "", "", // 13-16
		200, 0, // 17-18
		nil, "", "internal_external", // 19-21
		now, now, // 22-23
	}

	var mailboxID int64
	if dial.IsPostgres() {
		if qerr := tx.QueryRowContext(ctx, insert+" RETURNING id", args...).Scan(&mailboxID); qerr != nil {
			err = qerr
			return 0, err
		}
	} else {
		res, qerr := tx.ExecContext(ctx, insert, args...)
		if qerr != nil {
			err = qerr
			return 0, err
		}
		mailboxID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}

	if aerr := insertAuditRow(ctx, tx, dial, "admin.transactional_mailbox_provision", email, now); aerr != nil {
		err = aerr
		return 0, err
	}
	if cerr := tx.Commit(); cerr != nil {
		err = cerr
		return 0, err
	}
	return mailboxID, nil
}

// writeTransactionalCredentialsEnvFile writes the generated credentials to
// a root-only (0600) env file, atomically (write to a temp file in the same
// directory, then rename). The plaintext password touches memory here and
// nowhere else in this process after the caller clears its own copy.
func writeTransactionalCredentialsEnvFile(email, password string) error {
	dir := filepath.Dir(transactionalCredentialsEnvPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".coremail-transactional-*.env.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed
	content := fmt.Sprintf("COREMAIL_TRANSACTIONAL_SMTP_USERNAME=%s\nCOREMAIL_TRANSACTIONAL_SMTP_PASSWORD=%s\n", email, password)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, transactionalCredentialsEnvPath)
}
