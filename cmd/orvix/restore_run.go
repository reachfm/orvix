package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/backup"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/restorecoord"
	"go.uber.org/zap"
)

// restore-run is the external, privileged restore coordinator. It is launched
// by orvix-restore.service (a oneshot systemd unit, User=root) which is in turn
// triggered by orvix-restore.path when the unprivileged Orvix API drops a job
// file into the queue. Because this process lives in a SEPARATE unit from
// orvix.service, `systemctl restart orvix` here does NOT kill it — so it can
// observe restart completion, probe the restarted service's health, and roll
// back on failure. None of that is possible inside the Orvix HTTP handler,
// which is why the API only submits a job and polls the durable result.

// restoreVerifyTimeout bounds each of the restart and post-restart health
// steps performed by the coordinator.
const restoreVerifyTimeout = 120 * time.Second

// restoreRunCommand is the `orvix restore-run` entrypoint. It drains the
// restore job queue under an exclusive lock and returns a process exit code.
func restoreRunCommand(args []string) int {
	logger, err := config.NewLogger(&config.LoggingConfig{Level: "info", Format: "console", Output: "stderr"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore-run: logger: %v\n", err)
		return 1
	}
	defer logger.Sync()

	cfg, err := config.Load(logger)
	if err != nil {
		logger.Error("restore-run: load config", zap.Error(err))
		return 1
	}
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		logger.Error("restore-run: open database", zap.Error(err))
		return 1
	}
	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("restore-run: sql.DB", zap.Error(err))
		return 1
	}

	coord := restorecoord.New(restoreJobsDir())
	if err := coord.EnsureDirs(); err != nil {
		logger.Error("restore-run: ensure dirs", zap.Error(err))
		return 1
	}

	release, ok, err := acquireExclusiveLock(coord.LockPath())
	if err != nil {
		logger.Error("restore-run: lock", zap.Error(err))
		return 1
	}
	if !ok {
		// Another coordinator is already draining; the path unit re-fired.
		logger.Info("restore-run: another coordinator holds the lock; exiting")
		return 0
	}
	defer release()

	ids, err := coord.PendingJobIDs()
	if err != nil {
		logger.Error("restore-run: list pending", zap.Error(err))
		return 1
	}
	if len(ids) == 0 {
		logger.Info("restore-run: no pending restore jobs")
		return 0
	}

	restart := func(ctx context.Context) error { return systemctlRestartOrvix(ctx) }
	health := func(ctx context.Context) error { return probeOrvixHealth(ctx, cfg) }

	rc := 0
	for _, id := range ids {
		svc, buildErr := buildRestoreService(cfg, sqlDB)
		if buildErr != nil {
			logger.Error("restore-run: build service", zap.String("job", id), zap.Error(buildErr))
			failJobResult(coord, id, fmt.Errorf("restore service unavailable: %w", buildErr), logger)
			rc = 1
			continue
		}
		if err := runOneRestoreJob(context.Background(), coord, svc, id, restart, health, logger); err != nil {
			logger.Error("restore-run: job failed", zap.String("job", id), zap.Error(err))
			rc = 1
		}
	}
	return rc
}

// restoreRunner is the subset of *backup.Service the coordinator drives. It
// keeps runOneRestoreJob unit-testable with a real backup.Service (exercising
// true ordering + durable state) rather than a mock.
type restoreRunner interface {
	RestoreBackup(ctx context.Context, id string) (*backup.RestoreStageResult, error)
	SetRestoreMaintenanceChecker(func(context.Context) error)
	SetRestoreRestart(func(context.Context) error)
	SetRestoreHealthCheck(func(context.Context) error)
	SetRestoreAuditHook(func(context.Context, string, string))
	SetRestoreVerifyTimeout(time.Duration)
}

// runOneRestoreJob drives a single restore job to a durable terminal state.
// The status file is advanced through activating -> restarting -> verifying ->
// (rolling_back) -> succeeded/failed so the polling API observes real progress
// across the Orvix restart. Success is written ONLY after RestoreBackup returns
// an activated result, i.e. after the actual restart AND post-restart health
// both succeeded.
func runOneRestoreJob(ctx context.Context, coord *restorecoord.Coordinator, svc restoreRunner, id string,
	restart, health func(context.Context) error, logger *zap.Logger) error {

	job, err := coord.ReadJob(id)
	if err != nil {
		// A malformed/tampered job cannot be trusted; record failure.
		failJobResult(coord, id, fmt.Errorf("read job: %w", err), logger)
		return err
	}
	res, err := coord.GetResult(id)
	if err != nil {
		return err
	}
	if res.Status.IsTerminal() {
		return nil // already processed
	}

	setStatus := func(s restorecoord.Status, msg string) {
		res.Status = s
		if msg != "" {
			res.Message = msg
		}
		if werr := coord.WriteResult(res); werr != nil {
			logger.Warn("restore-run: write status", zap.String("job", id), zap.Error(werr))
		}
	}

	// The external coordinator IS the authorized restore path; there is no
	// separate operator maintenance marker to check here.
	svc.SetRestoreMaintenanceChecker(func(context.Context) error { return nil })
	svc.SetRestoreVerifyTimeout(restoreVerifyTimeout)
	svc.SetRestoreRestart(func(c context.Context) error {
		setStatus(restorecoord.StatusRestarting, "restarting Orvix service")
		return restart(c)
	})
	svc.SetRestoreHealthCheck(func(c context.Context) error {
		setStatus(restorecoord.StatusVerifying, "verifying restarted service health")
		return health(c)
	})
	svc.SetRestoreAuditHook(func(_ context.Context, action, detail string) {
		logger.Info("restore audit", zap.String("job", id), zap.String("action", action), zap.String("detail", detail))
		if strings.Contains(action, "rollback_start") {
			setStatus(restorecoord.StatusRollingBack, "restore failed; rolling back to the pre-restore safety backup")
		}
	})

	setStatus(restorecoord.StatusActivating, "validating and activating the backup")
	stage, rerr := svc.RestoreBackup(ctx, job.BackupID)

	// Map the orchestration outcome onto the durable terminal result. Success
	// is only recorded when RestoreBackup returns an activated result.
	if stage != nil {
		res.SafetyBackupID = stage.SafetyBackupID
		res.RolledBack = stage.RolledBack
	}
	switch {
	case rerr == nil && stage != nil && stage.Status == backup.RestoreStatusActivated:
		res.Status = restorecoord.StatusSucceeded
		res.Message = "restore complete; restarted service health verified"
		res.Error = ""
	default:
		res.Status = restorecoord.StatusFailed
		if rerr != nil {
			res.Error = rerr.Error()
		}
		if stage != nil {
			res.Message = stage.Message
			if res.Error == "" {
				res.Error = stage.Message
			}
			if stage.RolledBack {
				res.RollbackError = "" // clean rollback; primary error is in res.Error
			}
		}
		if res.Message == "" {
			res.Message = "restore failed"
		}
	}
	if werr := coord.WriteResult(res); werr != nil {
		return fmt.Errorf("write terminal result: %w", werr)
	}
	// Drop the processed job from the queue so the systemd path unit settles;
	// the durable result remains for the API to poll.
	if rmErr := coord.RemoveJob(id); rmErr != nil {
		logger.Warn("restore-run: remove processed job", zap.String("job", id), zap.Error(rmErr))
	}
	if res.Status == restorecoord.StatusFailed {
		return fmt.Errorf("restore job %s failed: %s", id, res.Error)
	}
	return nil
}

func failJobResult(coord *restorecoord.Coordinator, id string, cause error, logger *zap.Logger) {
	res, err := coord.GetResult(id)
	if err != nil {
		logger.Error("restore-run: cannot load result to record failure", zap.String("job", id), zap.Error(err))
		return
	}
	res.Status = restorecoord.StatusFailed
	res.Error = cause.Error()
	res.Message = "restore failed before activation"
	if werr := coord.WriteResult(res); werr != nil {
		logger.Error("restore-run: write failure result", zap.String("job", id), zap.Error(werr))
	}
}

// systemctlRestartOrvix restarts the orvix service via systemd. It is
// observable (returns the real exit status) and fails closed when systemctl is
// unavailable. No shell, no backgrounding.
func systemctlRestartOrvix(ctx context.Context) error {
	unit := strings.TrimSpace(os.Getenv("ORVIX_SYSTEMD_UNIT"))
	if unit == "" {
		unit = "orvix"
	}
	bin, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("service manager unavailable: systemctl not found: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "restart", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s failed: %w (output: %s)", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// probeOrvixHealth polls the restarted service's HTTP health endpoint until it
// reports healthy or the bounded context expires.
func probeOrvixHealth(ctx context.Context, cfg *config.Config) error {
	// Test-only failure injection for the staging FAILURE-path acceptance. It
	// forces the post-restart health gate to fail so the rollback lifecycle
	// (reactivate safety backup -> real restart) is exercised with a REAL
	// service restart — not a simulated one. It is honored ONLY in a
	// non-production deployment; a production (PostgreSQL) install ignores it
	// entirely, so it can never be activated accidentally in production.
	if os.Getenv("ORVIX_RESTORE_FORCE_HEALTH_FAILURE") == "1" && (cfg == nil || !cfg.Database.IsProduction()) {
		return fmt.Errorf("forced post-restart health failure (test-only injection; non-production only)")
	}

	port := 8080
	if cfg != nil && cfg.Server.AdminPort > 0 {
		port = cfg.Server.AdminPort
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port)
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("restarted service did not become healthy: %w", lastErr)
			}
			return fmt.Errorf("restarted service did not become healthy: %w", err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && healthJSONOK(body) {
				return nil
			}
			lastErr = fmt.Errorf("health status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("restarted service did not become healthy: %w", lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}

func healthJSONOK(body []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	s, _ := m["status"].(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok", "healthy", "up", "pass":
		return true
	}
	return false
}

// restoreJobsDir resolves the restore job/result directory. It is a FIXED path
// (not derived from config) because the systemd orvix-restore.path unit watches
// it literally and cannot read the config; the API and this helper must agree
// with that watched path exactly. Overridable via ORVIX_RESTORE_JOBS_DIR only
// for test/staging harnesses that set it identically on both units.
func restoreJobsDir() string {
	if v := strings.TrimSpace(os.Getenv("ORVIX_RESTORE_JOBS_DIR")); v != "" {
		return v
	}
	return "/var/lib/orvix/restore-jobs"
}

// buildRestoreService constructs a backup.Service equivalent to the API's for
// the restore path (registry, paths, encryption).
func buildRestoreService(cfg *config.Config, sqlDB *sql.DB) (*backup.Service, error) {
	backupDir := "/var/backups/orvix/"
	if cfg != nil && cfg.Backup.Dir != "" {
		backupDir = cfg.Backup.Dir
	}
	mailDir, attachDir := "", ""
	if cfg != nil {
		mailDir = cfg.CoreMail.MailStorePath
		if mailDir == "" && cfg.CoreMail.DataPath != "" {
			mailDir = filepath.Join(cfg.CoreMail.DataPath, "mailstore")
		}
		if cfg.CoreMail.DataPath != "" {
			attachDir = filepath.Join(cfg.CoreMail.DataPath, "attachments")
		}
	}
	svc := backup.NewService(backupDir, sqlDB, sqlDB, mailDir, attachDir)
	svc.SetConfigPath("/etc/orvix/orvix.yaml")
	if cfg != nil {
		svc.SetDatabasePath(cfg.Database.SQLitePath)
		svc.SetPostgresDSN(cfg.Database.DSN)
		if cfg.Backup.EncryptionEnabled {
			if err := svc.SetEncryptionConfig(backup.BackupEncryptionConfig{
				Enabled: true,
				KeyFile: cfg.Backup.EncryptionKeyFile,
			}); err != nil {
				return nil, fmt.Errorf("backup encryption unavailable: %w", err)
			}
		} else if cfg.Database.IsProduction() {
			return nil, fmt.Errorf("backup encryption must be enabled in production")
		}
	}
	return svc, nil
}
