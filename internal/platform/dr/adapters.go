package dr

import (
	"context"
	"time"

	"github.com/orvix/orvix/internal/backup"
	"github.com/orvix/orvix/internal/platform/cluster"
)

// ClusterLeaseAdapter implements LeaseAcquirer over the real
// internal/platform/cluster.Service fenced-lease primitive.
type ClusterLeaseAdapter struct {
	svc *cluster.Service
}

func NewClusterLeaseAdapter(svc *cluster.Service) *ClusterLeaseAdapter {
	return &ClusterLeaseAdapter{svc: svc}
}

func (a *ClusterLeaseAdapter) AcquireLease(ctx context.Context, resourceKey, nodeID string, duration time.Duration) (string, error) {
	lease, err := a.svc.AcquireLease(ctx, resourceKey, nodeID, duration)
	if err != nil {
		return "", err
	}
	return lease.NodeID, nil
}

// BackupServiceAdapter implements BackupOperator over the real
// internal/backup.Service — the actual backup/restore mechanics stay
// exactly as implemented there; this only adapts the method shapes
// this package needs.
type BackupServiceAdapter struct {
	svc *backup.Service
}

func NewBackupServiceAdapter(svc *backup.Service) *BackupServiceAdapter {
	return &BackupServiceAdapter{svc: svc}
}

func (a *BackupServiceAdapter) CreateBackup(ctx context.Context, name string) (string, error) {
	b, err := a.svc.CreateBackup(ctx, name)
	if err != nil {
		return "", err
	}
	return b.ID, nil
}

func (a *BackupServiceAdapter) RestoreBackup(ctx context.Context, backupID string) error {
	_, err := a.svc.RestoreBackup(ctx, backupID)
	return err
}

func (a *BackupServiceAdapter) ListVerifiedBackups(ctx context.Context) ([]VerifiedBackup, error) {
	all, err := a.svc.ListBackups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]VerifiedBackup, 0, len(all))
	for _, b := range all {
		if b.Status == backup.StatusVerified || b.Status == backup.StatusCompleted {
			out = append(out, VerifiedBackup{ID: b.ID, CompletedAt: b.CompletedAt})
		}
	}
	return out, nil
}
