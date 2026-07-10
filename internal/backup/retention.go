package backup

import (
	"context"
	"fmt"
)

func (s *Service) RunRetention(ctx context.Context) (int, error) {
	cfg, err := s.GetScheduleConfig(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.RetentionCount <= 0 {
		return 0, fmt.Errorf("retention count must be at least 1, got %d", cfg.RetentionCount)
	}
	backups, err := s.listCompletedBackups(ctx)
	if err != nil {
		return 0, err
	}
	if len(backups) <= cfg.RetentionCount {
		return 0, nil
	}

	// Never delete the most recent backup — the one just created.
	toDelete := backups[cfg.RetentionCount:]
	// If the most recent backup is in the "to delete" range, exclude it.
	if len(backups) > 0 && len(toDelete) > 0 {
		if toDelete[len(toDelete)-1].ID == backups[0].ID {
			toDelete = toDelete[:len(toDelete)-1]
		}
	}
	if len(toDelete) == 0 {
		return 0, nil
	}

	for _, b := range toDelete {
		if err := s.DeleteBackup(ctx, b.ID); err != nil {
			return 0, fmt.Errorf("delete backup %s: %w", b.ID, err)
		}
	}
	return len(toDelete), nil
}

func (s *Service) listCompletedBackups(ctx context.Context) ([]Backup, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, status, size_bytes, sha256, created_at, completed_at FROM backup_registry WHERE status = "+s.dialect.Placeholder(1)+" ORDER BY created_at DESC", string(StatusCompleted))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Backup
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.Name, &b.Status, &b.SizeBytes, &b.SHA256, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}
