package organization

import (
	"context"
	"database/sql"
	"time"
)

type DeletionJob struct {
	db *sql.DB
}

func NewDeletionJob(db *sql.DB) *DeletionJob {
	return &DeletionJob{db: db}
}

func (j *DeletionJob) ProcessExpiredDeletions(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := j.db.QueryContext(ctx,
		`SELECT d.organization_id FROM org_deletions d
		WHERE d.state = 'deletion_requested'
		AND d.retention_expires_at IS NOT NULL
		AND d.retention_expires_at <= ?
		AND d.cancelled_at IS NULL
		ORDER BY d.retention_expires_at ASC LIMIT 50`, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	for rows.Next() {
		var orgID uint
		if err := rows.Scan(&orgID); err != nil {
			return count, err
		}
		// Mark deletion as confirmed
		if _, err := tx.ExecContext(ctx,
			`UPDATE org_deletions SET confirmed_at=?, state='completed' WHERE organization_id=? AND cancelled_at IS NULL`,
			now, orgID); err != nil {
			return count, err
		}
		// Hard-delete tenant
		if _, err := tx.ExecContext(ctx,
			`UPDATE tenants SET deleted_at=? WHERE id=?`, now, orgID); err != nil {
			return count, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}
