package backup

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Service) ensureScheduleTable(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, tables[1])
	return err
}

func (s *Service) GetScheduleConfig(ctx context.Context) (*ScheduleConfig, error) {
	if err := s.ensureScheduleTable(ctx); err != nil {
		return nil, err
	}
	if s.db == nil {
		return defaultScheduleConfig(), nil
	}

	row := s.db.QueryRowContext(ctx, `SELECT enabled, frequency, retention_count, last_run_at, next_run_at, updated_at FROM backup_schedule_config WHERE id = 1`)

	var enabled int
	var freqStr string
	var retentionCount int
	var lastRunAt, nextRunAt sql.NullTime
	var updatedAt time.Time

	err := row.Scan(&enabled, &freqStr, &retentionCount, &lastRunAt, &nextRunAt, &updatedAt)
	if err == sql.ErrNoRows {
		return defaultScheduleConfig(), nil
	}
	if err != nil {
		return nil, err
	}

	cfg := &ScheduleConfig{
		Enabled:        enabled != 0,
		Frequency:      Frequency(freqStr),
		RetentionCount: retentionCount,
		UpdatedAt:      updatedAt,
	}
	if lastRunAt.Valid {
		cfg.LastRunAt = &lastRunAt.Time
	}
	if nextRunAt.Valid {
		cfg.NextRunAt = &nextRunAt.Time
	}
	return cfg, nil
}

func defaultScheduleConfig() *ScheduleConfig {
	return &ScheduleConfig{
		Enabled:        false,
		Frequency:      FrequencyManual,
		RetentionCount: 10,
		UpdatedAt:      time.Now().UTC(),
	}
}

func (s *Service) SetScheduleConfig(ctx context.Context, cfg *ScheduleConfig) (*ScheduleConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Frequency != FrequencyManual && cfg.Frequency != FrequencyDaily && cfg.Frequency != FrequencyWeekly {
		return nil, fmt.Errorf("invalid frequency: %s", cfg.Frequency)
	}
	if cfg.RetentionCount <= 0 {
		return nil, fmt.Errorf("retention count must be at least 1")
	}
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := s.ensureScheduleTable(ctx); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	cfg.UpdatedAt = now

	if cfg.Enabled && cfg.Frequency != FrequencyManual {
		nextRun := calculateNextRun(cfg.Frequency, now)
		cfg.NextRunAt = &nextRun
	} else {
		cfg.NextRunAt = nil
	}

	enabledInt := 0
	if cfg.Enabled {
		enabledInt = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO backup_schedule_config (id, enabled, frequency, retention_count, last_run_at, next_run_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
	`, enabledInt, string(cfg.Frequency), cfg.RetentionCount, cfg.LastRunAt, cfg.NextRunAt, cfg.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return s.GetScheduleConfig(ctx)
}

func (s *Service) RunScheduledBackupIfNeeded(ctx context.Context) (*Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.GetScheduleConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || cfg.Frequency == FrequencyManual {
		return nil, nil
	}
	if cfg.NextRunAt == nil || cfg.NextRunAt.After(time.Now()) {
		return nil, nil
	}

	name := fmt.Sprintf("scheduled-%s-%s", cfg.Frequency, time.Now().UTC().Format("20060102-150405"))
	backup, err := s.createBackupLocked(ctx, name)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	nextRun := calculateNextRun(cfg.Frequency, now)

	_, err = s.db.ExecContext(ctx, `UPDATE backup_schedule_config SET last_run_at = ?, next_run_at = ?, updated_at = ? WHERE id = 1`,
		now, nextRun, now)
	if err != nil {
		return nil, err
	}

	return backup, nil
}

func calculateNextRun(freq Frequency, after time.Time) time.Time {
	switch freq {
	case FrequencyDaily:
		return after.Add(24 * time.Hour)
	case FrequencyWeekly:
		return after.Add(7 * 24 * time.Hour)
	default:
		return time.Time{}
	}
}
