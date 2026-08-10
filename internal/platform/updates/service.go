package updates

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// Service is the staged-update lifecycle: verify a signed artifact,
// stage it to disk (pure file/hash operation, nothing executed), and
// expose apply/rollback as hand-off points to an external coordinator
// — this process never restarts or replaces itself in-process.
type Service struct {
	repo       *Repository
	verifier   *Verifier
	stageDir   string
	audit      *audit.ExtendedStore
	outbox     *kernel.OutboxRepository
	clock      kernel.Clock
	currentVer string // the running build's version, for "wrong version" rejection
}

func NewService(repo *Repository, verifier *Verifier, stageDir, currentVersion string, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	if stageDir == "" {
		stageDir = "/var/lib/orvix/update-staging"
	}
	return &Service{repo: repo, verifier: verifier, stageDir: stageDir, audit: auditStore, outbox: outbox, clock: clock, currentVer: currentVersion}
}

// SubmitArtifact verifies and stages an update artifact end-to-end:
//  1. manifest signature must verify against a trusted key (unsigned or
//     invalid signature is rejected before anything else runs).
//  2. artifact bytes must hash to exactly what the signed manifest says
//     (tamper detection).
//  3. manifest.Version must equal expectedVersion (the operator-supplied
//     next version for this staged rollout — never inferred from the
//     artifact itself).
//  4. manifest.Platform/Arch must equal this deployment's platform/arch.
//
// Only after all four checks pass is the artifact written to the
// staging directory — a pure file write, never executed, never shelled
// out to. Rollback metadata (previous version/hash) is captured now,
// before any apply is possible.
func (s *Service) SubmitArtifact(ctx context.Context, artifact, manifestJSON, signature []byte, expectedVersion, expectedPlatform, expectedArch string, actorID uint) (*Record, error) {
	if err := s.verifier.VerifyManifest(manifestJSON, signature); err != nil {
		s.recordFailure(ctx, actorID, "unknown", err)
		return nil, err
	}
	manifest, err := ParseManifest(manifestJSON)
	if err != nil {
		s.recordFailure(ctx, actorID, "unknown", err)
		return nil, err
	}
	if err := VerifyArtifactHash(artifact, manifest); err != nil {
		s.recordFailure(ctx, actorID, manifest.Version, err)
		return nil, err
	}
	if expectedVersion != "" && manifest.Version != expectedVersion {
		s.recordFailure(ctx, actorID, manifest.Version, ErrVersionMismatch)
		return nil, ErrVersionMismatch
	}
	if (expectedPlatform != "" && manifest.Platform != expectedPlatform) || (expectedArch != "" && manifest.Arch != expectedArch) {
		s.recordFailure(ctx, actorID, manifest.Version, ErrPlatformMismatch)
		return nil, ErrPlatformMismatch
	}

	now := s.clock.Now()
	prevVersion := s.currentVer
	prevRec, _ := s.repo.Latest(ctx)
	prevHash := ""
	if prevRec != nil {
		prevHash = prevRec.ArtifactHash
	}

	rec := &Record{
		Version:      manifest.Version,
		Platform:     manifest.Platform,
		Arch:         manifest.Arch,
		ArtifactHash: manifest.SHA256,
		State:        StateVerified,
		PrevVersion:  prevVersion,
		PrevHash:     prevHash,
		ActorID:      actorID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.Insert(ctx, rec); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "insert update record", err)
	}

	// Stage: pure file write, keyed by hash so a re-submission of the
	// identical artifact is idempotent at the filesystem level.
	if err := os.MkdirAll(s.stageDir, 0o750); err != nil {
		_ = s.repo.UpdateState(ctx, rec.ID, StateRejected, "stage dir unavailable: "+err.Error(), now)
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "prepare staging directory", err)
	}
	path := filepath.Join(s.stageDir, manifest.SHA256+".artifact")
	if err := os.WriteFile(path, artifact, 0o640); err != nil {
		_ = s.repo.UpdateState(ctx, rec.ID, StateRejected, "stage write failed: "+err.Error(), now)
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "stage artifact", err)
	}
	rec.ArtifactPath = path
	rec.State = StateStaged
	if err := s.repo.UpdateState(ctx, rec.ID, StateStaged, "", now); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "update record state", err)
	}

	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "update.artifact.staged", ActorID: actorID, Result: "success", Target: "update:" + manifest.Version, After: manifest.SHA256})
	}
	// Staging is non-destructive (a file write, nothing applied), so no
	// outbox event is emitted here; TriggerApply/Rollback are the
	// consequential transitions and are audited individually below.
	return rec, nil
}

func (s *Service) recordFailure(ctx context.Context, actorID uint, version string, err error) {
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "update.artifact.rejected", ActorID: actorID, Result: "failure", Reason: err.Error(), Target: "update:" + version})
	}
}

// GetStatus returns one staged-update record.
func (s *Service) GetStatus(ctx context.Context, id uint) (*Record, error) {
	rec, err := s.repo.Get(ctx, id)
	if err == sql.ErrNoRows {
		return nil, ErrRecordNotFound
	}
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get update record", err)
	}
	return rec, nil
}

// LatestStatus returns the most recently submitted update record, if any.
func (s *Service) LatestStatus(ctx context.Context) (*Record, error) {
	rec, err := s.repo.Latest(ctx)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get latest update record", err)
	}
	return rec, nil
}

// History returns recent update records, newest first.
func (s *Service) History(ctx context.Context, limit int) ([]Record, error) {
	out, err := s.repo.List(ctx, limit)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list update records", err)
	}
	return out, nil
}

// ApplyCoordinator is the narrow port to an external apply mechanism
// (e.g. a systemd oneshot unit, mirroring internal/restorecoord's
// design) that actually performs the swap/restart. No implementation
// of this interface exists yet in this codebase; TriggerApply reports
// ErrNoCoordinator until one is wired, rather than silently pretending
// to apply.
type ApplyCoordinator interface {
	// Submit hands the staged artifact path off for external
	// application and returns a coordinator-tracked job ID.
	Submit(ctx context.Context, artifactPath, version string) (jobID string, err error)
}

// TriggerApply hands a Staged record off to the ApplyCoordinator, if
// one is configured. It NEVER applies the update itself (no in-process
// restart/replace) — see the package doc. Without a configured
// coordinator it returns ErrNoCoordinator and leaves the record in
// Staged, which is the safe, honest default.
func (s *Service) TriggerApply(ctx context.Context, id uint, coordinator ApplyCoordinator, actorID uint) (*Record, string, error) {
	rec, err := s.GetStatus(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if rec.State != StateStaged {
		return nil, "", ErrInvalidTransition
	}
	if coordinator == nil {
		return rec, "", ErrNoCoordinator
	}
	jobID, err := coordinator.Submit(ctx, rec.ArtifactPath, rec.Version)
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "submit apply job", err)
	}
	now := s.clock.Now()
	if err := s.repo.UpdateState(ctx, rec.ID, StateApplied, "", now); err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "update record state", err)
	}
	rec.State = StateApplied
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "update.apply.submitted", ActorID: actorID, Result: "success", Target: "update:" + rec.Version, After: jobID})
	}
	return rec, jobID, nil
}

// Rollback marks a previously Applied record RolledBack. Like Apply,
// this package never performs the actual rollback in-process — it
// records the operator's decision and the pre-recorded rollback
// metadata (PrevVersion/PrevHash); the same external coordinator that
// applies updates is responsible for actually reverting.
func (s *Service) Rollback(ctx context.Context, id uint, reason string, actorID uint) (*Record, error) {
	rec, err := s.GetStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.State != StateApplied {
		return nil, ErrInvalidTransition
	}
	now := s.clock.Now()
	if err := s.repo.UpdateState(ctx, rec.ID, StateRolledBack, reason, now); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "update record state", err)
	}
	rec.State = StateRolledBack
	rec.FailureNote = reason
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "update.rollback", ActorID: actorID, Result: "success", Reason: reason, Target: "update:" + rec.Version, After: rec.PrevVersion})
	}
	return rec, nil
}
