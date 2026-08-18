package main

// orvix admin repair-signup-owner
//
// Root-only, operator-run repair command for the one narrow class of
// pre-fix legacy data this codebase knowingly produced: an Organization
// created through public Start Free signup whose owner identity row was
// persisted with role='user' (the pre-canonical signup behavior fixed in
// customer_auth.go / customer_signup_otp.go, which now persist a new
// organization owner as tenant_admin).
//
// It repairs ONLY a role='user' row that is PROVEN to be a signup-created
// Organization owner by audited, non-secret facts:
//
//  1. exactly one user matches the email (case-insensitive);
//  2. the row belongs to the tenant named via --tenant-id;
//  3. the row's role is exactly 'user' (never promotes platform_super_admin,
//     superadmin, admin, tenant_*, billing, or unknown roles);
//  4. the row is active and not soft-deleted;
//  5. a coremail_audit 'customer.signup' record exists for this exact user
//     (actor user:<id>, target user:<id>|email:<email>|tenant:<tenantID>);
//  6. NO coremail_mailboxes row exists for that email (a mailbox identity
//     means the row is a webmail end-user — RoleUser semantics — and must
//     never be promoted; webmail end users are deliberately excluded).
//
// All predicates are re-verified INSIDE the mutation transaction. The
// mutation is role='user' → 'tenant_admin' + token_version bump (existing
// JWTs are rejected on next use) + exactly one coremail_audit row
// (admin.repair_signup_owner). Never prints passwords, hashes, tokens, or
// DSNs — only email/role/tenant status text.
//
// Dry-run is the DEFAULT: without --confirm REPAIR-SIGNUP-OWNER the
// command reports the exact row it would repair and exits 0 without
// mutating anything.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

const repairSignupOwnerConfirmToken = "REPAIR-SIGNUP-OWNER"

var (
	errRepairNotSignupOwner     = errors.New("row is not a proven signup-created organization owner")
	errRepairRoleNotUser        = errors.New("only role='user' rows can be repaired to tenant_admin")
	errRepairTenantMismatch     = errors.New("user does not belong to the given tenant")
	errRepairMailboxPresent     = errors.New("email has a mailbox identity; refusing to promote a webmail end-user")
	errRepairNoSignupAudit      = errors.New("no customer.signup audit record found for this user; refusing")
	errRepairRowsAffected       = errors.New("unexpected number of rows affected; aborting")
	errRepairConfirmRequired    = errors.New("dry-run complete — pass --confirm REPAIR-SIGNUP-OWNER to apply")
	errRepairConfirmMismatch    = errors.New("confirmation token did not match; aborting")
	errRepairSignupAuditMissing = errors.New("no customer.signup audit record found for this user")
)

// runRepairSignupOwner implements `orvix admin repair-signup-owner`.
func runRepairSignupOwner(email string, tenantID int64, confirm string, dryRun bool, deps adminCLIDeps) int {
	if !deps.isRoot() {
		fmt.Fprintln(deps.stderr, "error:", errAdminRecoveryNotRoot)
		return 1
	}
	normEmail := normalizeEmail(email)
	if normEmail == "" {
		fmt.Fprintln(deps.stderr, "error: --email is required")
		return 2
	}
	if tenantID <= 0 {
		fmt.Fprintln(deps.stderr, "error:", errAdminInvalidTenantID)
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

	// Strict predicate (checked again inside the transaction).
	if !user.TenantID.Valid || user.TenantID.Int64 != tenantID {
		fmt.Fprintln(deps.stderr, "error:", errRepairTenantMismatch)
		return 1
	}
	if user.Role != string(authRoleUserLiteral) {
		fmt.Fprintln(deps.stderr, "error:", errRepairRoleNotUser)
		return 1
	}
	if err := verifySignupOwnerPredicates(ctx, sqlDB, dial, user); err != nil {
		fmt.Fprintln(deps.stderr, "error:", err)
		return 1
	}

	if dryRun {
		fmt.Fprintf(deps.stdout, "DRY-RUN: would repair signup-created owner\n")
		fmt.Fprintf(deps.stdout, "  email:     %s\n", user.Email)
		fmt.Fprintf(deps.stdout, "  user id:   %d\n", user.ID)
		fmt.Fprintf(deps.stdout, "  tenant id: %d\n", tenantID)
		fmt.Fprintf(deps.stdout, "  role:      user -> tenant_admin (token_version bump)\n")
		fmt.Fprintln(deps.stderr, "error:", errRepairConfirmRequired)
		return 1
	}

	if confirm != repairSignupOwnerConfirmToken {
		fmt.Fprintln(deps.stderr, "error:", errRepairConfirmMismatch)
		return 1
	}

	if err := repairSignupOwnerTx(ctx, sqlDB, dial, user, tenantID, deps.now()); err != nil {
		fmt.Fprintln(deps.stderr, "error: repair failed; no changes were made")
		return 1
	}

	fmt.Fprintf(deps.stdout, "OK: signup-created owner %s (user:%d, tenant:%d) repaired to tenant_admin\n",
		user.Email, user.ID, tenantID)
	return 0
}

// authRoleUserLiteral is the literal persisted spelling of auth.RoleUser.
// Deliberately a local literal so this command never depends on a runtime
// role map: it must only ever promote the exact string 'user'.
const authRoleUserLiteral = "user"

// verifySignupOwnerPredicates proves the row is a signup-created
// Organization owner via audited, non-secret facts:
//   - a coremail_audit row action='customer.signup' targets this exact user
//     (actor user:<id>, target user:<id>|email:<email>|tenant:<tenantID>);
//   - no coremail_mailboxes row exists for the email (absence of a mailbox
//     identity — a webmail end-user must never be promoted).
func verifySignupOwnerPredicates(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}, dial *dbdialect.Info, user *adminEligibleUser) error {
	// Absence of mailbox identity.
	var mailboxCount int
	if err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE LOWER(email) = "+dial.Placeholder(1)+" AND deleted_at IS NULL",
		strings.ToLower(user.Email),
	).Scan(&mailboxCount); err != nil {
		return fmt.Errorf("check mailbox identity: %w", err)
	}
	if mailboxCount > 0 {
		return errRepairMailboxPresent
	}

	// Presence of the signup audit record for this exact user.
	actor := fmt.Sprintf("user:%d", user.ID)
	targetPrefix := fmt.Sprintf("user:%d|email:%s|tenant:", user.ID, user.Email)
	var auditCount int
	if err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_audit WHERE action = "+dial.Placeholder(1)+
			" AND actor = "+dial.Placeholder(2)+
			" AND (target LIKE "+dial.Placeholder(3)+" OR target LIKE "+dial.Placeholder(4)+")",
		"customer.signup", actor, targetPrefix+"%", "%"+targetPrefix+"%",
	).Scan(&auditCount); err != nil {
		return fmt.Errorf("check signup audit record: %w", err)
	}
	if auditCount == 0 {
		return errRepairNoSignupAudit
	}
	return nil
}

// repairSignupOwnerTx performs the audited promotion inside one
// transaction, re-verifying every predicate against the fresh row.
func repairSignupOwnerTx(ctx context.Context, sqlDB *sql.DB, dial *dbdialect.Info, user *adminEligibleUser, tenantID int64, now time.Time) (err error) {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Re-verify inside the transaction to close the TOCTOU window.
	fresh, ferr := lookupExactlyOneUser(ctx, tx, dial, user.Email)
	if ferr != nil {
		return ferr
	}
	if fresh.ID != user.ID || !fresh.TenantID.Valid || fresh.TenantID.Int64 != tenantID {
		return errRepairTenantMismatch
	}
	if fresh.Role != authRoleUserLiteral {
		return errRepairRoleNotUser
	}
	if err := verifySignupOwnerPredicates(ctx, tx, dial, fresh); err != nil {
		return err
	}

	res, uerr := tx.ExecContext(ctx,
		"UPDATE users SET role = "+dial.Placeholder(1)+
			", token_version = COALESCE(token_version, 0) + 1"+
			", updated_at = "+dial.Placeholder(2)+
			" WHERE id = "+dial.Placeholder(3)+
			" AND tenant_id = "+dial.Placeholder(4)+
			" AND role = "+dial.Placeholder(5)+
			" AND active = "+dial.TrueLiteral()+
			" AND deleted_at IS NULL",
		"tenant_admin", now, fresh.ID, tenantID, authRoleUserLiteral,
	)
	if uerr != nil {
		return uerr
	}
	affected, raErr := res.RowsAffected()
	if raErr != nil {
		return raErr
	}
	if affected != 1 {
		return errRepairRowsAffected
	}

	if err := deleteUserSessions(ctx, tx, dial, fresh.ID); err != nil {
		return err
	}
	target := fmt.Sprintf("user:%d|email:%s|tenant:%d|role:user->tenant_admin", fresh.ID, fresh.Email, tenantID)
	if err := insertAuditRow(ctx, tx, dial, "admin.repair_signup_owner", target, now); err != nil {
		return err
	}
	return tx.Commit()
}
