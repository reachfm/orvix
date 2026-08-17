package main

// orvix admin reset-password / orvix admin recover
//
// Root-only, root-console operator recovery commands. They implement the
// CLI described (design-only, not yet built) in
// docs/deployment/break-glass-recovery.md:
//
//   - reset-password rotates the password of an existing platform_super_admin
//     or tenant_admin WITHOUT changing its authorization identity (role,
//     tenant_id are untouched).
//   - recover is the platform break-glass path: it promotes an existing
//     tenant_admin (or an already-canonical/legacy-superadmin platform
//     account) to platform_super_admin with tenant_id=NULL, in addition to
//     rotating its password and clearing MFA.
//
// Both commands:
//   - refuse to run unless the process is root (checked BEFORE the database
//     is opened);
//   - never accept the new password via argv, environment, or config — it is
//     read twice from an interactive hidden TTY prompt and rejected outright
//     when stdin is not a terminal;
//   - resolve exactly one user by case-insensitive email match, failing
//     closed on zero or multiple matches;
//   - require active=true and deleted_at IS NULL;
//   - hash the new password with the same auth.HashPassword (Argon2id) the
//     rest of the application uses to verify logins — no new format;
//   - perform the row mutation, token_version bump, session revocation, and
//     audit insert inside ONE transaction that rolls back entirely if any
//     step fails;
//   - never print or log the password, its hash, a session token, a JWT, or
//     a database DSN. Only email, role, and generic status text are ever
//     written to stdout/stderr.

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"golang.org/x/term"
)

const (
	adminPasswordMinBytes = 8
	adminPasswordMaxBytes = 72 // bcrypt-compatible upper bound; Argon2id has no such limit, kept for parity with the rest of the app's password policy.
)

var (
	errAdminRecoveryNotRoot       = errors.New("this command must be run as root")
	errAdminRecoveryNotTTY        = errors.New("a password can only be entered interactively on a terminal")
	errAdminRecoveryPasswordShort = fmt.Errorf("password must be at least %d characters", adminPasswordMinBytes)
	errAdminRecoveryPasswordLong  = fmt.Errorf("password must be at most %d bytes", adminPasswordMaxBytes)
	errAdminRecoveryMismatch      = errors.New("passwords do not match")
	errAdminRecoveryUserNotFound  = errors.New("no eligible user found for that email")
	errAdminRecoveryRoleRejected  = errors.New("user's role is not eligible for this operation")
	errAdminRecoveryConfirmFailed = errors.New("confirmation email did not match; aborting")
	errAdminRecoveryRowsAffected  = errors.New("unexpected number of rows affected; aborting")
	errAdminInvalidTenantID       = errors.New("--tenant-id must be a positive integer")
	errAdminTenantNotFound        = errors.New("no active tenant found for that tenant id")
	errAdminEmailInvalid          = errors.New("invalid email format")
	errAdminEmailExists           = errors.New("a user with that email already exists")
)

// adminCLIDeps abstracts every side effect the recovery commands perform, so
// tests can exercise the full decision tree (root gate, TTY gate, password
// policy, role gate, transaction/rollback behavior) without a real root
// process, a real terminal, or a shared global. Production wiring is
// defaultAdminCLIDeps; every field is a hard requirement, not a fallback —
// production code must never weaken any of them.
type adminCLIDeps struct {
	isRoot func() bool

	// readPassword prompts (on stderr) and reads a password twice from an
	// interactive hidden TTY, returning an error if stdin is not a
	// terminal or the two entries do not match. It must never echo the
	// password back and must never accept it via argv/env.
	readPassword func(stderr io.Writer) (string, error)

	// readConfirmation prompts (on stderr) for a line of plain input (the
	// operator re-typing the target email) and returns it verbatim.
	readConfirmation func(stderr io.Writer, prompt string) (string, error)

	// openDB opens the SAME production config/database path the service
	// itself uses (internal/config.Load + internal/config.NewDatabase),
	// never a second/alternate connection string. It returns the raw
	// *sql.DB, the resolved dialect, and a close function.
	openDB func() (*sql.DB, *dbdialect.Info, func() error, error)

	now func() time.Time

	stdout, stderr io.Writer
}

func defaultAdminCLIDeps() adminCLIDeps {
	return adminCLIDeps{
		isRoot:           isRoot,
		readPassword:     ttyReadNewPassword,
		readConfirmation: ttyReadConfirmationLine,
		openDB:           openProductionDB,
		now:              func() time.Time { return time.Now().UTC() },
		stdout:           os.Stdout,
		stderr:           os.Stderr,
	}
}

// openProductionDB opens the exact configuration and database connection the
// running orvix.service uses — the same config.Load/config.NewDatabase pair
// as cmd/orvix's normal startup and restore-run path. It intentionally does
// NOT accept an alternate DSN/config path from the caller: the recovery CLI
// must always operate on the one real database the service reads from.
func openProductionDB() (*sql.DB, *dbdialect.Info, func() error, error) {
	logger, err := config.NewLogger(&config.LoggingConfig{Level: "error", Format: "console", Output: "stderr"})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("logger: %w", err)
	}
	cfg, err := config.Load(logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	gormDB, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sql.DB: %w", err)
	}
	dial := dbdialect.FromDriver(cfg.Database.Driver)
	return sqlDB, dial, sqlDB.Close, nil
}

// ttyReadNewPassword prompts twice (hidden input) and requires the two
// entries to match. It fails closed — with errAdminRecoveryNotTTY — when
// stdin is not an interactive terminal, so a non-interactive invocation
// (piped stdin, systemd, CI) can never supply a password.
func ttyReadNewPassword(stderr io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errAdminRecoveryNotTTY
	}
	fmt.Fprint(stderr, "New password (hidden): ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(stderr, "Confirm password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("read password confirmation: %w", err)
	}
	if string(first) != string(second) {
		return "", errAdminRecoveryMismatch
	}
	return string(first), nil
}

// ttyReadConfirmationLine prompts on stderr and reads one plain (visible)
// line from stdin, used for the "type the exact email to confirm" gate on
// `admin recover`. It also fails closed on a non-terminal stdin.
func ttyReadConfirmationLine(stderr io.Writer, prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errAdminRecoveryNotTTY
	}
	fmt.Fprint(stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read confirmation: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func validateNewPassword(pw string) error {
	if len(pw) < adminPasswordMinBytes {
		return errAdminRecoveryPasswordShort
	}
	if len(pw) > adminPasswordMaxBytes {
		return errAdminRecoveryPasswordLong
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// adminEligibleUser is the read-only projection used to decide eligibility
// before any prompt or mutation. Never logged or printed in full.
type adminEligibleUser struct {
	ID        int64
	Email     string
	Role      string
	TenantID  sql.NullInt64
	Active    bool
	DeletedAt sql.NullTime
}

// lookupExactlyOneUser resolves a user by case-insensitive email match on
// the SAME open connection/transaction the caller supplies, failing closed
// (errAdminRecoveryUserNotFound) on zero or more-than-one matches so a
// duplicate-email data anomaly can never be exploited to silently target the
// wrong row.
func lookupExactlyOneUser(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}, dial *dbdialect.Info, email string) (*adminEligibleUser, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT id, email, role, tenant_id, active, deleted_at FROM users WHERE LOWER(email) = "+dial.Placeholder(1),
		email,
	)
	if err != nil {
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	defer rows.Close()

	var matches []adminEligibleUser
	for rows.Next() {
		var u adminEligibleUser
		var active int
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.TenantID, &active, &u.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Active = active != 0
		matches = append(matches, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	if len(matches) != 1 {
		return nil, errAdminRecoveryUserNotFound
	}
	u := matches[0]
	if !u.Active || u.DeletedAt.Valid {
		return nil, errAdminRecoveryUserNotFound
	}
	return &u, nil
}

// lookupActiveTenant resolves tenantID on the SAME open connection/
// transaction the caller supplies, failing closed (errAdminTenantNotFound)
// unless the tenant exists, is active, and is not soft-deleted.
func lookupActiveTenant(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}, dial *dbdialect.Info, tenantID int64) error {
	var id int64
	var active int
	var deletedAt sql.NullTime
	err := q.QueryRowContext(ctx,
		"SELECT id, active, deleted_at FROM tenants WHERE id = "+dial.Placeholder(1),
		tenantID,
	).Scan(&id, &active, &deletedAt)
	if err != nil {
		return errAdminTenantNotFound
	}
	if active == 0 || deletedAt.Valid {
		return errAdminTenantNotFound
	}
	return nil
}

// resetPasswordAllowedRole reports whether role is an eligible LITERAL
// canonical target role for `admin reset-password`. Unlike `admin recover`,
// this command never changes authorization identity, so no legacy-role
// normalization is applied here — the role on disk must already be exactly
// one of the two canonical admin roles.
func resetPasswordAllowedRole(role string) bool {
	switch auth.Role(role) {
	case auth.RolePlatformSuperAdmin, auth.RoleTenantAdmin:
		return true
	default:
		return false
	}
}

// recoverAllowedSourceRole reports whether role is an eligible source role
// for `admin recover`. It intentionally does NOT call auth.NormalizeRole —
// that helper is a session-establishment/login-flow function reserved for
// internal/auth/auth.go and cmd/orvix/main.go's bootstrap path (see
// internal/api/handlers/no_direct_role_gates_test.go's normalizeRoleAllowlist
// and its PR #60 rationale: NormalizeRole must not be invoked from any other
// package to make an authorization decision). This mirrors the same,
// intentionally narrow migration-window mapping inline instead:
//   - tenant_admin and platform_super_admin (already canonical, the latter
//     making recovery idempotent) are allowed unconditionally;
//   - the legacy superadmin/super_admin/super-admin aliases are allowed
//     unconditionally, matching NormalizeRole's own unconditional mapping of
//     those three spellings to platform_super_admin;
//   - the ambiguous bare "admin" is allowed ONLY when tenant_id is set,
//     matching NormalizeRole's requirement of tenant context to disambiguate
//     it as tenant_admin; a NULL-tenant "admin" is rejected as ambiguous;
//   - tenant_operator, tenant_support, tenant_readonly, user, billing, and
//     any unknown/empty role are rejected.
func recoverAllowedSourceRole(role string, tenantID sql.NullInt64) bool {
	switch auth.Role(strings.TrimSpace(role)) {
	case auth.RolePlatformSuperAdmin, auth.RoleTenantAdmin:
		return true
	case "superadmin", "super_admin", "super-admin":
		return true
	case auth.RoleAdmin:
		return tenantID.Valid && tenantID.Int64 > 0
	default:
		return false
	}
}

// insertAuditRow writes exactly one coremail_audit row using the real
// production schema/columns. It is called only from inside the same
// transaction as the mutation it records, so a failed insert rolls back the
// entire operation.
func insertAuditRow(ctx context.Context, tx *sql.Tx, dial *dbdialect.Info, action, target string, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp) VALUES ("+
			dial.Placeholders(8)+")",
		"local-root", "", action, target, "success", "", "", now,
	)
	return err
}

// deleteUserSessions revokes every session belonging to userID, in the same
// transaction as the row mutation and audit insert.
func deleteUserSessions(ctx context.Context, tx *sql.Tx, dial *dbdialect.Info, userID int64) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = "+dial.Placeholder(1), userID)
	return err
}

// runAdminResetPassword implements `orvix admin reset-password --email <email>`.
func runAdminResetPassword(email string, deps adminCLIDeps) int {
	if !deps.isRoot() {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryNotRoot)
		return 1
	}
	normEmail := normalizeEmail(email)
	if normEmail == "" {
		fmt.Fprintln(deps.stderr, "error: --email is required")
		return 2
	}

	sqlDB, dial, closeDB, err := deps.openDB()
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: unable to access the database")
		return 1
	}
	defer closeDB()

	ctx := context.Background()

	user, err := lookupExactlyOneUser(ctx, sqlDB, dial, normEmail)
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryUserNotFound)
		return 1
	}
	if !resetPasswordAllowedRole(user.Role) {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryRoleRejected)
		return 1
	}

	password, err := deps.readPassword(deps.stderr)
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}
	if err := validateNewPassword(password); err != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}

	hash, err := auth.HashPassword(password)
	password = ""
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: failed to hash password")
		return 1
	}

	if err := resetPasswordTx(ctx, sqlDB, dial, user, hash, deps.now()); err != nil {
		fmt.Fprintln(deps.stderr, "error: password reset failed; no changes were made")
		return 1
	}

	fmt.Fprintf(deps.stdout, "OK: password reset for %s (role unchanged: %s)\n", user.Email, user.Role)
	return 0
}

func resetPasswordTx(ctx context.Context, sqlDB *sql.DB, dial *dbdialect.Info, user *adminEligibleUser, hash string, now time.Time) (err error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Re-verify inside the transaction to close the TOCTOU window between
	// the pre-prompt read and the write.
	fresh, ferr := lookupExactlyOneUser(ctx, tx, dial, user.Email)
	if ferr != nil {
		return ferr
	}
	if fresh.ID != user.ID || !resetPasswordAllowedRole(fresh.Role) {
		return errAdminRecoveryRoleRejected
	}

	res, uerr := tx.ExecContext(ctx,
		"UPDATE users SET password_hash = "+dial.Placeholder(1)+
			", token_version = COALESCE(token_version, 0) + 1"+
			", updated_at = "+dial.Placeholder(2)+
			" WHERE id = "+dial.Placeholder(3)+
			" AND role = "+dial.Placeholder(4)+
			" AND active = "+dial.TrueLiteral()+
			" AND deleted_at IS NULL",
		hash, now, fresh.ID, fresh.Role,
	)
	if uerr != nil {
		return uerr
	}
	affected, raErr := res.RowsAffected()
	if raErr != nil {
		return raErr
	}
	if affected != 1 {
		return errAdminRecoveryRowsAffected
	}

	if err := deleteUserSessions(ctx, tx, dial, fresh.ID); err != nil {
		return err
	}
	if err := insertAuditRow(ctx, tx, dial, "admin.password_reset", fresh.Email, now); err != nil {
		return err
	}
	return tx.Commit()
}

// runAdminRecover implements `orvix admin recover --email <email>`.
func runAdminRecover(email string, deps adminCLIDeps) int {
	if !deps.isRoot() {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryNotRoot)
		return 1
	}
	normEmail := normalizeEmail(email)
	if normEmail == "" {
		fmt.Fprintln(deps.stderr, "error: --email is required")
		return 2
	}

	sqlDB, dial, closeDB, err := deps.openDB()
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: unable to access the database")
		return 1
	}
	defer closeDB()

	ctx := context.Background()

	user, err := lookupExactlyOneUser(ctx, sqlDB, dial, normEmail)
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryUserNotFound)
		return 1
	}
	if !recoverAllowedSourceRole(user.Role, user.TenantID) {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryRoleRejected)
		return 1
	}

	confirmed, err := deps.readConfirmation(deps.stderr,
		fmt.Sprintf("This will make %s a Platform Super Admin. Type the email again to confirm: ", user.Email))
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}
	if normalizeEmail(confirmed) != user.Email {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryConfirmFailed)
		return 1
	}

	password, err := deps.readPassword(deps.stderr)
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}
	if err := validateNewPassword(password); err != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}

	hash, err := auth.HashPassword(password)
	password = ""
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: failed to hash password")
		return 1
	}

	if err := recoverTx(ctx, sqlDB, dial, user, hash, deps.now()); err != nil {
		fmt.Fprintln(deps.stderr, "error: recovery failed; no changes were made")
		return 1
	}

	fmt.Fprintf(deps.stdout, "OK: %s recovered as platform_super_admin\n", user.Email)
	return 0
}

func recoverTx(ctx context.Context, sqlDB *sql.DB, dial *dbdialect.Info, user *adminEligibleUser, hash string, now time.Time) (err error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	fresh, ferr := lookupExactlyOneUser(ctx, tx, dial, user.Email)
	if ferr != nil {
		return ferr
	}
	if fresh.ID != user.ID || !recoverAllowedSourceRole(fresh.Role, fresh.TenantID) {
		return errAdminRecoveryRoleRejected
	}

	res, uerr := tx.ExecContext(ctx,
		"UPDATE users SET role = "+dial.Placeholder(1)+
			", tenant_id = NULL"+
			", password_hash = "+dial.Placeholder(2)+
			", mfa_enabled = "+dial.FalseLiteral()+
			", mfa_secret = ''"+
			", updated_at = "+dial.Placeholder(3)+
			", token_version = COALESCE(token_version, 0) + 1"+
			" WHERE id = "+dial.Placeholder(4)+
			" AND active = "+dial.TrueLiteral()+
			" AND deleted_at IS NULL",
		string(auth.RolePlatformSuperAdmin), hash, now, fresh.ID,
	)
	if uerr != nil {
		return uerr
	}
	affected, raErr := res.RowsAffected()
	if raErr != nil {
		return raErr
	}
	if affected != 1 {
		return errAdminRecoveryRowsAffected
	}
	// Best-effort clear of the newer pending-MFA columns where present;
	// these were added after mfa_enabled/mfa_secret and are covered by
	// AutoMigrate on every deployed schema in this repository, so the
	// column always exists. A failure here still rolls back the whole
	// transaction via the deferred Rollback above.
	if _, err := tx.ExecContext(ctx,
		"UPDATE users SET pending_mfa_secret = '', pending_mfa_secret_raw = '', mfa_secret_raw = '' WHERE id = "+dial.Placeholder(1),
		fresh.ID,
	); err != nil {
		return err
	}

	if err := deleteUserSessions(ctx, tx, dial, fresh.ID); err != nil {
		return err
	}
	if err := insertAuditRow(ctx, tx, dial, "admin.recover", fresh.Email, now); err != nil {
		return err
	}
	return tx.Commit()
}

// runAdminCreateTenantAdmin implements
// `orvix admin create-tenant-admin --tenant-id <id> --email <email>`. It
// provisions a brand-new tenant_admin for an EXISTING tenant — the missing
// non-SQL production path noted in
// docs/deployment/portal-separation-phase1.md and by
// test/playwright/seed-fixture/main.go's doc comment. Unlike reset-password
// and recover, this never touches an existing user row: it fails closed if
// the email already exists rather than updating or promoting it.
func runAdminCreateTenantAdmin(tenantID int64, email string, deps adminCLIDeps) int {
	if !deps.isRoot() {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryNotRoot)
		return 1
	}
	if tenantID <= 0 {
		fmt.Fprintln(deps.stderr, "error:", errAdminInvalidTenantID)
		return 2
	}
	normEmail := normalizeEmail(email)
	if normEmail == "" {
		fmt.Fprintln(deps.stderr, "error: --email is required")
		return 2
	}
	if _, err := mail.ParseAddress(normEmail); err != nil {
		fmt.Fprintln(deps.stderr, "error:", errAdminEmailInvalid)
		return 2
	}

	sqlDB, dial, closeDB, err := deps.openDB()
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: unable to access the database")
		return 1
	}
	defer closeDB()

	ctx := context.Background()

	if err := lookupActiveTenant(ctx, sqlDB, dial, tenantID); err != nil {
		fmt.Fprintln(deps.stderr, "error:", errAdminTenantNotFound)
		return 1
	}

	var existing int64
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE LOWER(email) = "+dial.Placeholder(1),
		normEmail,
	).Scan(&existing); err != nil {
		fmt.Fprintln(deps.stderr, "error: unable to check existing users")
		return 1
	}
	if existing > 0 {
		fmt.Fprintln(deps.stderr, "error:", errAdminEmailExists)
		return 1
	}

	password, err := deps.readPassword(deps.stderr)
	if err != nil {
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}
	if err := validateNewPassword(password); err != nil {
		password = ""
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}

	hash, err := auth.HashPassword(password)
	password = ""
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: failed to hash password")
		return 1
	}

	userID, err := createTenantAdminTx(ctx, sqlDB, dial, tenantID, normEmail, hash, deps.now())
	if err != nil {
		fmt.Fprintln(deps.stderr, "error: tenant admin creation failed; no changes were made")
		return 1
	}

	fmt.Fprintf(deps.stdout, "OK: created user id=%d email=%s tenant_id=%d role=%s\n", userID, normEmail, tenantID, auth.RoleTenantAdmin)
	return 0
}

func createTenantAdminTx(ctx context.Context, sqlDB *sql.DB, dial *dbdialect.Info, tenantID int64, email, hash string, now time.Time) (userID int64, err error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Re-verify inside the transaction to close the TOCTOU window between
	// the pre-prompt reads and the write.
	if err := lookupActiveTenant(ctx, tx, dial, tenantID); err != nil {
		return 0, err
	}
	var existing int64
	if qerr := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE LOWER(email) = "+dial.Placeholder(1),
		email,
	).Scan(&existing); qerr != nil {
		return 0, qerr
	}
	if existing > 0 {
		return 0, errAdminEmailExists
	}

	var newID int64
	if dial.IsPostgres() {
		if qerr := tx.QueryRowContext(ctx,
			"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, mfa_enabled, mfa_secret, token_version) VALUES ("+
				dial.Placeholders(11)+") RETURNING id",
			now, now, email, hash, string(auth.RoleTenantAdmin), tenantID, true, true, false, "", 0,
		).Scan(&newID); qerr != nil {
			return 0, qerr
		}
	} else {
		res, uerr := tx.ExecContext(ctx,
			"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, mfa_enabled, mfa_secret, token_version) VALUES ("+
				dial.Placeholders(11)+")",
			now, now, email, hash, string(auth.RoleTenantAdmin), tenantID, true, true, false, "", 0,
		)
		if uerr != nil {
			return 0, uerr
		}
		affected, raErr := res.RowsAffected()
		if raErr != nil {
			return 0, raErr
		}
		if affected != 1 {
			return 0, errAdminRecoveryRowsAffected
		}
		newID, uerr = res.LastInsertId()
		if uerr != nil {
			return 0, uerr
		}
	}

	if err := insertAuditRow(ctx, tx, dial, "admin.tenant_admin_create", email, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

// adminCommand is the `orvix admin <subcommand>` entrypoint, dispatched from
// main() before any server bootstrap happens (matching the migrate/
// restore-run pattern).
func adminCommand(args []string) int {
	return runAdminCommand(args, defaultAdminCLIDeps())
}

func runAdminCommand(args []string, deps adminCLIDeps) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, adminUsage())
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "reset-password":
		fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
		fs.SetOutput(deps.stderr)
		email := fs.String("email", "", "target user's email (required)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if strings.TrimSpace(*email) == "" {
			fmt.Fprintln(deps.stderr, "error: --email is required")
			return 2
		}
		return runAdminResetPassword(*email, deps)
	case "recover":
		fs := flag.NewFlagSet("admin recover", flag.ContinueOnError)
		fs.SetOutput(deps.stderr)
		email := fs.String("email", "", "target user's email (required)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if strings.TrimSpace(*email) == "" {
			fmt.Fprintln(deps.stderr, "error: --email is required")
			return 2
		}
		return runAdminRecover(*email, deps)
	case "create-tenant-admin":
		fs := flag.NewFlagSet("admin create-tenant-admin", flag.ContinueOnError)
		fs.SetOutput(deps.stderr)
		email := fs.String("email", "", "new tenant admin's email (required)")
		tenantID := fs.Int64("tenant-id", 0, "existing, active tenant's numeric id (required, positive)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if strings.TrimSpace(*email) == "" {
			fmt.Fprintln(deps.stderr, "error: --email is required")
			return 2
		}
		if *tenantID <= 0 {
			fmt.Fprintln(deps.stderr, "error:", errAdminInvalidTenantID)
			return 2
		}
		return runAdminCreateTenantAdmin(*tenantID, *email, deps)
	case "provision-transactional-mailbox":
		return runAdminProvisionTransactionalMailbox(rest, deps)
	case "-h", "--help", "help":
		fmt.Fprintln(deps.stdout, adminUsage())
		return 0
	default:
		fmt.Fprintf(deps.stderr, "orvix admin: unknown subcommand %q\n\n%s\n", sub, adminUsage())
		return 2
	}
}

func adminUsage() string {
	return `orvix admin — root-only platform administrator recovery.

Usage:
  orvix admin reset-password --email <email>
      Rotate the password of an existing platform_super_admin or
      tenant_admin. Role and tenant_id are never changed.

  orvix admin recover --email <email>
      Break-glass recovery: promote an existing tenant_admin (or an
      already-canonical/legacy-superadmin account) to platform_super_admin
      with tenant_id=NULL, rotate its password, and clear MFA.

  orvix admin create-tenant-admin --tenant-id <id> --email <email>
      Provision a brand-new tenant_admin for an existing, active tenant.
      Fails closed if the email already exists — never updates or
      promotes an existing user. Creates no session.

  orvix admin provision-transactional-mailbox --domain <domain> [--local-part noreply] --confirm PROVISION-MAILBOX
      Create the platform's own outbound transactional mailbox (OTP,
      password reset, support mail). Resolves an EXISTING coremail_domains
      row by name and never creates one or touches DKIM. Idempotent —
      never resets an existing mailbox's password. Generates a random
      password, writes it once to a root-only env file, and never prints
      it.

All three commands:
  - must be run as root;
  - prompt for the new password twice on an interactive hidden TTY only
    (never via a flag, environment variable, or config file);
  - write exactly one coremail_audit row on success.

reset-password and recover additionally revoke every existing session
for the target user.

` + strconv.Itoa(adminPasswordMinBytes) + `-` + strconv.Itoa(adminPasswordMaxBytes) + ` byte password length is enforced.`
}
