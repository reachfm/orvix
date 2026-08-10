package mailbox

import (
	"context"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

var (
	ErrMailboxAlreadyDeleted = fmt.Errorf("mailbox is already deleted")
	ErrMailboxNotDeleted     = fmt.Errorf("mailbox is not deleted")
	ErrMailboxEmailConflict  = fmt.Errorf("email address is in use by another mailbox")
)

// GetByIDIncludingDeleted reads a mailbox regardless of soft-delete
// state — needed by Restore/Purge, which must operate on exactly the
// rows every other repository method's "deleted_at IS NULL" filter
// deliberately hides.
func (r *AdminMailboxRepo) GetByIDIncludingDeleted(ctx context.Context, id, tenantID uint) (*AdminMailbox, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT id, domain_id, tenant_id, email, local_part, name, status, quota_mb, used_bytes, msg_count, is_admin, allow_smtp, allow_imap, allow_pop3, allow_jmap, mfa_enabled, send_limit_per_hour, last_login, COALESCE(last_ip,''), created_at, updated_at FROM coremail_mailboxes WHERE id = "+r.dialect.Placeholder(1)+" AND tenant_id = "+r.dialect.Placeholder(2),
		id, tenantID)
	return scanAdminMailbox(row)
}

// RestoreByID clears deleted_at and reactivates a soft-deleted
// mailbox. The caller is responsible for the email-conflict check
// (ExistsByEmail) before calling this, inside the same transaction.
func (r *AdminMailboxRepo) RestoreByID(ctx context.Context, id, tenantID uint) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET status="+r.dialect.Placeholder(1)+", deleted_at=NULL, updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4),
		string(AdminMailboxActive), time.Now().UTC(), id, tenantID)
	return err
}

// PurgeByID permanently and irreversibly removes a mailbox row. It
// only ever affects rows that are ALREADY soft-deleted
// (deleted_at IS NOT NULL) — the WHERE clause is the safety rail that
// makes an accidental purge of a live mailbox structurally
// impossible, not just something the service layer happens to check
// first.
func (r *AdminMailboxRepo) PurgeByID(ctx context.Context, id, tenantID uint) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM coremail_mailboxes WHERE id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NOT NULL",
		id, tenantID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SoftDeleteMailbox transitions a mailbox to the deleted state. Reuses
// the existing status-transition machinery (SetStatus / UpdateStatus,
// which already special-cases AdminMailboxDeleted to stamp deleted_at)
// — the only change needed elsewhere is allowing the transition itself
// in isValidStatusTransition.
func (s *Service) SoftDeleteMailbox(ctx context.Context, id, tenantID uint, reason string) error {
	return s.SetStatus(ctx, id, tenantID, AdminMailboxDeleted, reason)
}

// RestoreMailbox reverses a soft-delete, transactionally re-checking
// the email-uniqueness constraint (another mailbox may have taken the
// address in the interim) so a restore can never silently create a
// duplicate-address collision that CreateMailbox's own check would
// have rejected.
func (s *Service) RestoreMailbox(ctx context.Context, id, tenantID uint) (*AdminMailbox, error) {
	m, err := s.repo.GetByIDIncludingDeleted(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMailboxNotFound
	}
	if m.Status != AdminMailboxDeleted {
		return nil, ErrMailboxNotDeleted
	}

	entry := &audit.ExtendedEntry{Action: "mailbox.restore", Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		exists, err := repo.ExistsByEmail(ctx, m.Email, id)
		if err != nil {
			return fmt.Errorf("check email conflict: %w", err)
		}
		if exists {
			return ErrMailboxEmailConflict
		}
		return repo.RestoreByID(ctx, id, tenantID)
	}); err != nil {
		return nil, err
	}

	restored, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	return restored, nil
}

// PurgeMailbox permanently removes an already soft-deleted mailbox.
// It is irreversible and is refused (ErrMailboxNotDeleted) for any
// mailbox not already in the deleted state — purge is a cleanup step
// after SoftDeleteMailbox, never a shortcut around it.
func (s *Service) PurgeMailbox(ctx context.Context, id, tenantID uint, reason string) error {
	m, err := s.repo.GetByIDIncludingDeleted(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrMailboxNotFound
	}
	if m.Status != AdminMailboxDeleted {
		return ErrMailboxNotDeleted
	}

	entry := &audit.ExtendedEntry{Action: "mailbox.purge", Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID, Result: "success", Reason: reason, Before: m.Email}
	return s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		n, err := repo.PurgeByID(ctx, id, tenantID)
		if err != nil {
			return fmt.Errorf("purge mailbox: %w", err)
		}
		if n == 0 {
			return ErrMailboxNotDeleted
		}
		return nil
	})
}

// CountEligibleForPurge counts mailboxes already soft-deleted (status
// = deleted) for tenantID whose deleted_at is older than cutoff — the
// "past the recovery window" set. Read-only; used by the retention
// bounded context's dry-run purge plan.
func (r *AdminMailboxRepo) CountEligibleForPurge(ctx context.Context, tenantID uint, cutoff time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND status="+r.dialect.Placeholder(2)+" AND deleted_at IS NOT NULL AND deleted_at < "+r.dialect.Placeholder(3),
		tenantID, string(AdminMailboxDeleted), cutoff).Scan(&n)
	return n, err
}

// PurgeBatchEligible permanently removes up to limit mailboxes already
// soft-deleted for tenantID with deleted_at older than cutoff, and
// returns how many rows were actually removed — bounded per call so a
// large backlog is never one unbounded DELETE. Like PurgeByID, the
// WHERE clause requiring status=deleted AND deleted_at IS NOT NULL
// makes purging a live mailbox structurally impossible here too.
func (r *AdminMailboxRepo) PurgeBatchEligible(ctx context.Context, tenantID uint, cutoff time.Time, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM coremail_mailboxes WHERE id IN (SELECT id FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND status="+r.dialect.Placeholder(2)+" AND deleted_at IS NOT NULL AND deleted_at < "+r.dialect.Placeholder(3)+" LIMIT "+r.dialect.Placeholder(4)+")",
		tenantID, string(AdminMailboxDeleted), cutoff, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
