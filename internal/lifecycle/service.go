package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// BackupCreator creates a safety backup before upgrade.
type BackupCreator interface {
	CreateBackup(ctx context.Context, name string) (interface{}, error)
}

// BackupRestorer restores a backup on rollback.
type BackupRestorer interface {
	RestoreBackup(ctx context.Context, id string) interface{}
	GetBackup(ctx context.Context, id string) (interface{}, error)
	ListBackups(ctx context.Context) (interface{}, error)
}

// PolicyLoader reloads policy engine from DB.
type PolicyLoader interface{ LoadFromDB(ctx context.Context) error }

// TrustLoader reloads trust engine from DB.
type TrustLoader interface{ LoadFromDB(ctx context.Context) error }

// RuntimeReloader reloads runtime after upgrade/rollback.
type RuntimeReloader interface{ Reload() error }

// HealthChecker provides subsystem health.
type HealthChecker interface {
	SMTPHealthy() bool
	IMAPHealthy() bool
	POP3Healthy() bool
	JMAPHealthy() bool
	DatabaseHealthy() bool
	MailStoreHealthy() bool
}

// Service provides lifecycle management.
type Service struct {
	db             *sql.DB
	dialect        *dbdialect.Info
	backupCreator  BackupCreator
	backupRestorer BackupRestorer
	policy         PolicyLoader
	trust          TrustLoader
	runtime        RuntimeReloader
	health         HealthChecker
}

// NewService creates a lifecycle service.
func NewService(db *sql.DB) *Service {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{db: db, dialect: dialect}
}

// SetBackupCreator attaches a backup creator for safety snapshots.
func (s *Service) SetBackupCreator(b BackupCreator) { s.backupCreator = b }

// SetBackupRestorer attaches a backup restorer for rollback.
func (s *Service) SetBackupRestorer(b BackupRestorer) { s.backupRestorer = b }

// SetPolicyLoader attaches a policy engine.
func (s *Service) SetPolicyLoader(p PolicyLoader) { s.policy = p }

// SetTrustLoader attaches a trust engine.
func (s *Service) SetTrustLoader(t TrustLoader) { s.trust = t }

// SetRuntimeReloader attaches a runtime.
func (s *Service) SetRuntimeReloader(r RuntimeReloader) { s.runtime = r }

// SetHealthChecker attaches a health checker.
func (s *Service) SetHealthChecker(h HealthChecker) { s.health = h }

// EnsureSchema creates required tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	// PostgreSQL schema is created by models.MigrateAllPostgres.
	if s.dialect.IsPostgres() {
		return nil
	}
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── Version Registry ────────────────────────────────────

func (s *Service) CurrentVersion(ctx context.Context) (*VersionRecord, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, version, installed_at, installed_by, notes FROM coremail_versions ORDER BY id DESC LIMIT 1")
	var v VersionRecord
	err := row.Scan(&v.ID, &v.Version, &v.InstalledAt, &v.InstalledBy, &v.Notes)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	return &v, nil
}

func (s *Service) VersionHistory(ctx context.Context) ([]VersionRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, version, installed_at, installed_by, notes FROM coremail_versions ORDER BY id DESC")
	if err != nil { return nil, err }
	defer rows.Close()
	var versions []VersionRecord
	for rows.Next() {
		var v VersionRecord
		if err := rows.Scan(&v.ID, &v.Version, &v.InstalledAt, &v.InstalledBy, &v.Notes); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *Service) RecordVersion(ctx context.Context, version, installedBy, notes string) (*VersionRecord, error) {
	v := &VersionRecord{Version: version, InstalledBy: installedBy, Notes: notes, InstalledAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, "INSERT INTO coremail_versions (version, installed_at, installed_by, notes) VALUES (?, ?, ?, ?)",
		v.Version, v.InstalledAt, v.InstalledBy, v.Notes)
	if err != nil { return nil, err }
	id, _ := res.LastInsertId()
	v.ID = uint(id)
	return v, nil
}

// ── Preflight Validation ────────────────────────────────

func (s *Service) RunPreflight(ctx context.Context) *PreflightResult {
	result := &PreflightResult{Pass: true}
	add := func(name, status, detail string) {
		result.Checks = append(result.Checks, PreflightCheck{Name: name, Status: status, Detail: detail})
		if status == "fail" { result.Pass = false }
	}

	if s.health != nil {
		check := func(name string, healthy bool) {
			if healthy { add(name, "pass", "") } else { add(name, "fail", "service unhealthy") }
		}
		check("SMTP", s.health.SMTPHealthy())
		check("IMAP", s.health.IMAPHealthy())
		check("POP3", s.health.POP3Healthy())
		check("JMAP", s.health.JMAPHealthy())
		check("Database", s.health.DatabaseHealthy())
		check("MailStore", s.health.MailStoreHealthy())
	}

	// Database reachable.
	if s.db != nil {
		if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); err != nil {
			add("Database", "fail", "cannot reach database")
		} else {
			add("Database Connection", "pass", "")
		}
	}

	// Backup service.
	if s.backupCreator != nil {
		add("Backup Service", "pass", "available")
	} else {
		add("Backup Service", "warning", "not configured")
	}

	// Runtime reload.
	if s.runtime != nil {
		add("Runtime Reload", "pass", "available")
	} else {
		add("Runtime Reload", "warning", "not configured")
	}

	return result
}

// ── Upgrade ──────────────────────────────────────────────

func (s *Service) Upgrade(ctx context.Context, fromVersion, toVersion string) *UpgradeRecord {
	record := &UpgradeRecord{
		FromVersion: fromVersion, ToVersion: toVersion,
		Status: UpgradeRunning, StartedAt: time.Now().UTC(),
	}
	s.saveUpgrade(ctx, record)

	// Step 1: Create safety backup.
	if s.backupCreator != nil {
		b, err := s.backupCreator.CreateBackup(ctx, fmt.Sprintf("pre-upgrade-%s", toVersion))
		if err != nil {
			record.Status = UpgradeFailed
			s.updateUpgrade(ctx, record)
			return record
		}
		_ = b
	}

	// Step 2: Run preflight.
	preflight := s.RunPreflight(ctx)
	if !preflight.Pass {
		s.rollback(ctx, record)
		return record
	}

	// Step 3: Record version.
	s.RecordVersion(ctx, toVersion, "system", "upgrade")

	// Step 4: Reload engines.
	if s.policy != nil { s.policy.LoadFromDB(ctx) }
	if s.trust != nil { s.trust.LoadFromDB(ctx) }
	if s.runtime != nil { s.runtime.Reload() }

	// Step 5: Mark completed.
	now := time.Now().UTC()
	record.Status = UpgradeCompleted
	record.CompletedAt = &now
	s.updateUpgrade(ctx, record)
	return record
}

// ── Rollback ────────────────────────────────────────────

func (s *Service) Rollback(ctx context.Context) *UpgradeRecord {
	// Find last completed upgrade to rollback from.
	row := s.db.QueryRowContext(ctx, "SELECT id, from_version, to_version, started_at FROM upgrade_history WHERE status=? ORDER BY id DESC LIMIT 1", UpgradeCompleted)
	var last UpgradeRecord
	if err := row.Scan(&last.ID, &last.FromVersion, &last.ToVersion, &last.StartedAt); err != nil {
		return &UpgradeRecord{Status: UpgradeFailed}
	}
	return s.rollbackTo(ctx, &last)
}

func (s *Service) rollback(ctx context.Context, record *UpgradeRecord) {
	s.rollbackTo(ctx, record)
}

func (s *Service) rollbackTo(ctx context.Context, record *UpgradeRecord) *UpgradeRecord {
	record.Status = UpgradeRolledBack
	now := time.Now().UTC()
	record.CompletedAt = &now
	s.updateUpgrade(ctx, record)

	// Reload engines after rollback.
	if s.policy != nil { s.policy.LoadFromDB(ctx) }
	if s.trust != nil { s.trust.LoadFromDB(ctx) }
	if s.runtime != nil { s.runtime.Reload() }

	return record
}

// ── Upgrade History ─────────────────────────────────────

func (s *Service) UpgradeHistory(ctx context.Context) ([]UpgradeRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, from_version, to_version, status, started_at, completed_at FROM upgrade_history ORDER BY id DESC")
	if err != nil { return nil, err }
	defer rows.Close()
	var records []UpgradeRecord
	for rows.Next() {
		var r UpgradeRecord
		var completedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.FromVersion, &r.ToVersion, &r.Status, &r.StartedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid { r.CompletedAt = &completedAt.Time }
		records = append(records, r)
	}
	return records, rows.Err()
}

// ── Internal ────────────────────────────────────────────

func (s *Service) saveUpgrade(ctx context.Context, r *UpgradeRecord) {
	var id uint
	if s.dialect.IsPostgres() {
		err := s.db.QueryRowContext(ctx,
			"INSERT INTO upgrade_history (from_version, to_version, status, started_at) VALUES ($1, $2, $3, $4) RETURNING id",
			r.FromVersion, r.ToVersion, string(r.Status), r.StartedAt).Scan(&id)
		if err == nil {
			r.ID = id
		}
		return
	}
	s.db.ExecContext(ctx, "INSERT INTO upgrade_history (from_version, to_version, status, started_at) VALUES (?, ?, ?, ?)",
		r.FromVersion, r.ToVersion, string(r.Status), r.StartedAt)
	// Set ID from last insert (best-effort).
	s.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&id)
	r.ID = id
}

func (s *Service) updateUpgrade(ctx context.Context, r *UpgradeRecord) {
	s.db.ExecContext(ctx,
		"UPDATE upgrade_history SET status="+s.dialect.Placeholder(1)+", completed_at="+s.dialect.Placeholder(2)+" WHERE id="+s.dialect.Placeholder(3),
		string(r.Status), r.CompletedAt, r.ID)
}
