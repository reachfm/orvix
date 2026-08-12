package retention

import (
	"context"
	"time"

	"github.com/orvix/orvix/internal/admin/mailbox"
)

// MailboxPurgeAdapter implements PurgeTarget over the real
// internal/admin/mailbox soft-delete lifecycle (Milestone 5):
// "eligible for purge" means a mailbox already soft-deleted (status =
// deleted) whose deleted_at is older than the cutoff this package
// computes from the resolved retention+recovery policy. This adapter
// NEVER deletes a live mailbox — CountEligibleForPurge/
// PurgeBatchEligible's own WHERE clauses require status=deleted,
// mirroring the same structural guarantee PurgeByID already has.
// ScopeKind must be "tenant"; other scope kinds are not supported by
// this adapter and return 0 rather than silently matching everything.
type MailboxPurgeAdapter struct {
	repo *mailbox.AdminMailboxRepo
}

func NewMailboxPurgeAdapter(repo *mailbox.AdminMailboxRepo) *MailboxPurgeAdapter {
	return &MailboxPurgeAdapter{repo: repo}
}

func (a *MailboxPurgeAdapter) CountEligible(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time) (int, error) {
	if scopeKind != "tenant" {
		return 0, nil
	}
	return a.repo.CountEligibleForPurge(ctx, scopeID, olderThan)
}

func (a *MailboxPurgeAdapter) PurgeBatch(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time, batchSize int) (int, error) {
	if scopeKind != "tenant" {
		return 0, nil
	}
	n, err := a.repo.PurgeBatchEligible(ctx, scopeID, olderThan, batchSize)
	return int(n), err
}
