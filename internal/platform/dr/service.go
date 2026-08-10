package dr

import (
	"context"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// LeaseAcquirer is the narrow port onto internal/platform/cluster's
// fenced-lease primitive — defined here (consumer side) so this
// package depends on a port, not the concrete cluster.Service type,
// and so tests can substitute a fake instead of standing up a full
// cluster schema.
type LeaseAcquirer interface {
	// AcquireLease returns nil, ErrLeaseHeldByOther-equivalent when the
	// resource is already held by a different caller; the concrete
	// error type is opaque to this package — Service treats any
	// non-nil error as "could not acquire", conservatively.
	AcquireLease(ctx context.Context, resourceKey, nodeID string, duration time.Duration) (holderNodeID string, err error)
}

// BackupOperator is the narrow port onto internal/backup.Service —
// only the operations this package coordinates, not the full backup
// mechanics (which stay exactly as implemented there).
type BackupOperator interface {
	CreateBackup(ctx context.Context, name string) (backupID string, err error)
	RestoreBackup(ctx context.Context, backupID string) error
	ListVerifiedBackups(ctx context.Context) ([]VerifiedBackup, error)
}

// VerifiedBackup is the minimal shape this package needs from
// internal/backup.Backup for RPO reporting.
type VerifiedBackup struct {
	ID          string
	CompletedAt *time.Time
}

type Service struct {
	repo    *Repository
	leases  LeaseAcquirer
	backups BackupOperator
	audit   *audit.ExtendedStore
	outbox  *kernel.OutboxRepository
	clock   kernel.Clock
	nodeID  string
	// MissingBackupThreshold is how long since the last verified
	// backup before Readiness reports MissingBackupAlert.
	MissingBackupThreshold time.Duration

	// localMu serializes calls to this Service instance per resource
	// key. The durable lease alone is not sufficient for intra-process
	// mutual exclusion: a lease legitimately allows its CURRENT holder
	// to renew/reacquire (that's correct — a long-running operation
	// must be able to extend its own lease), which means two
	// concurrent goroutines in the SAME process presenting the same
	// node identity would both be treated as "the legitimate holder
	// renewing" and both proceed. localMu closes that gap for this
	// process; the lease is what closes it ACROSS processes/nodes.
	localMu   sync.Mutex
	resources map[string]*sync.Mutex
}

func NewService(repo *Repository, leases LeaseAcquirer, backups BackupOperator, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, nodeID string, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, leases: leases, backups: backups, audit: auditStore, outbox: outbox, nodeID: nodeID, clock: clock, MissingBackupThreshold: 48 * time.Hour, resources: make(map[string]*sync.Mutex)}
}

// resourceLock returns the process-local mutex for a resource key,
// creating it on first use.
func (s *Service) resourceLock(key string) *sync.Mutex {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	m, ok := s.resources[key]
	if !ok {
		m = &sync.Mutex{}
		s.resources[key] = m
	}
	return m
}

// CoordinatedBackup acquires the durable backup lease before calling
// through to internal/backup.Service.CreateBackup, so a second
// concurrent caller (another admin request, or in a multi-node
// deployment another node) gets a clean typed rejection instead of
// racing the first operation.
func (s *Service) CoordinatedBackup(ctx context.Context, name string, actorID uint) (string, error) {
	lock := s.resourceLock(BackupLeaseResource)
	lock.Lock()
	defer lock.Unlock()

	holder, err := s.leases.AcquireLease(ctx, BackupLeaseResource, s.nodeID, 10*time.Minute)
	if err != nil || holder != s.nodeID {
		return "", ErrOperationInProgress
	}
	id, err := s.backups.CreateBackup(ctx, name)
	if err != nil {
		return "", kernel.Wrap(kernel.ErrCodeInternal, "coordinated backup", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "dr.backup.create", ActorID: actorID, Result: "success", After: id})
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "dr.backup.completed", id, map[string]any{"name": name}, s.clock.Now())
	}
	return id, nil
}

// CoordinatedRestore requires the exact typed confirmation phrase
// (RestoreConfirmationPhrase) in addition to the durable restore
// lease — restoring is the single most destructive operation this
// platform exposes, so it gets both guards.
func (s *Service) CoordinatedRestore(ctx context.Context, backupID, confirmation string, actorID uint) error {
	if confirmation != RestoreConfirmationPhrase {
		return ErrConfirmationRequired
	}
	lock := s.resourceLock(RestoreLeaseResource)
	lock.Lock()
	defer lock.Unlock()

	holder, err := s.leases.AcquireLease(ctx, RestoreLeaseResource, s.nodeID, 30*time.Minute)
	if err != nil || holder != s.nodeID {
		return ErrOperationInProgress
	}
	if err := s.backups.RestoreBackup(ctx, backupID); err != nil {
		if s.audit != nil {
			_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "dr.restore.execute", ActorID: actorID, Result: "failure", Reason: err.Error(), Target: "backup:" + backupID})
		}
		return kernel.Wrap(kernel.ErrCodeInternal, "coordinated restore", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "dr.restore.execute", ActorID: actorID, Result: "success", Target: "backup:" + backupID})
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "dr.restore.completed", backupID, map[string]any{}, s.clock.Now())
	}
	return nil
}

// RecordDrill logs a restore-drill outcome — a drill is a real staged
// restore exercised against isolated test data (per the platform
// rule: never a destructive real restore), then discarded/rolled
// back; the caller (an operator runbook or a scheduled job) is
// responsible for actually performing the isolated drill and
// reporting the outcome here.
func (s *Service) RecordDrill(ctx context.Context, backupID string, outcome DrillOutcome, durationMS int64, failureReason string, actorID uint) (*Drill, error) {
	d := &Drill{BackupID: backupID, Outcome: outcome, DurationMS: durationMS, FailureReason: failureReason, ActorID: actorID, StartedAt: s.clock.Now()}
	if err := s.repo.RecordDrill(ctx, d); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "record dr drill", err)
	}
	return d, nil
}

func (s *Service) ListDrills(ctx context.Context, limit int) ([]Drill, error) {
	return s.repo.ListDrills(ctx, limit)
}

// Readiness computes DR posture from real evidence — no fabricated
// "all green" default.
func (s *Service) Readiness(ctx context.Context) (*Readiness, error) {
	r := &Readiness{}
	now := s.clock.Now()

	if s.backups != nil {
		backups, err := s.backups.ListVerifiedBackups(ctx)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "list verified backups", err)
		}
		var latest *time.Time
		for _, b := range backups {
			if b.CompletedAt != nil && (latest == nil || b.CompletedAt.After(*latest)) {
				latest = b.CompletedAt
			}
		}
		r.LastVerifiedBackupAt = latest
		if latest != nil {
			gap := now.Sub(*latest)
			r.RPOGap = &gap
			r.MissingBackupAlert = gap > s.MissingBackupThreshold
		} else {
			r.MissingBackupAlert = true
		}
	}

	drill, err := s.repo.LastSuccessfulDrill(ctx)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get last successful drill", err)
	}
	if drill != nil {
		r.LastSuccessfulDrillAt = &drill.StartedAt
		r.LastDrillDurationMS = drill.DurationMS
	}
	return r, nil
}
