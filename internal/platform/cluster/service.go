package cluster

import (
	"context"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// DefaultLeaseDuration is how long a node's heartbeat lease is valid
// before it is considered missed. HeartbeatIntervalHint (returned to
// nodes, informational) should be well under this.
const DefaultLeaseDuration = 30 * time.Second

type Service struct {
	repo   *Repository
	audit  *audit.ExtendedStore
	outbox *kernel.OutboxRepository
	clock  kernel.Clock
}

func NewService(repo *Repository, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, audit: auditStore, outbox: outbox, clock: clock}
}

// Enroll registers a new node (or re-enrolls an existing ID with a
// fresh secret) and returns the raw enrollment secret exactly once —
// it is never retrievable again, matching the project's
// generate-once-hash-forever secret pattern.
func (s *Service) Enroll(ctx context.Context, n Node) (*Node, string, error) {
	raw, hash, err := GenerateEnrollmentSecret()
	if err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "generate enrollment secret", err)
	}
	now := s.clock.Now()
	n.Status = NodeAlive
	n.LastHeartbeatAt = now
	n.LeaseExpiresAt = now.Add(DefaultLeaseDuration)
	n.CreatedAt = now
	if err := s.repo.UpsertNode(ctx, &n, hash, now); err != nil {
		return nil, "", kernel.Wrap(kernel.ErrCodeInternal, "enroll node", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "cluster.node.enroll", Target: "node:" + n.ID, Result: "success"})
	}
	return &n, raw, nil
}

// EnsureSelfNode is the single-node auto-bootstrap entrypoint: on
// first boot it enrolls nodeID (typically derived from the local
// hostname) and returns the raw secret for the caller to persist
// locally if it wants inter-node auth later; on every subsequent boot
// it finds the existing row and simply heartbeats it — the secret is
// never regenerated or re-logged after the first enrollment, so a
// restart never leaks it a second time. A single-node deployment that
// never calls the multi-node APIs behaves exactly as it did before
// this package existed.
func (s *Service) EnsureSelfNode(ctx context.Context, nodeID string, n Node) (alreadyEnrolled bool, rawSecret string, err error) {
	existing, err := s.repo.GetNode(ctx, nodeID)
	if err != nil && err != ErrNodeNotFound {
		return false, "", kernel.Wrap(kernel.ErrCodeInternal, "check self node", err)
	}
	if existing != nil {
		if hbErr := s.Heartbeat(ctx, nodeID); hbErr != nil {
			return true, "", hbErr
		}
		return true, "", nil
	}
	n.ID = nodeID
	_, raw, err := s.Enroll(ctx, n)
	if err != nil {
		return false, "", err
	}
	return false, raw, nil
}

// Authenticate verifies a heartbeat/command's presented secret against
// the stored hash and rejects revoked nodes — the single choke point
// every node-originated request must pass through.
func (s *Service) Authenticate(ctx context.Context, nodeID, rawSecret string) error {
	hash, revoked, err := s.repo.GetNodeAuth(ctx, nodeID)
	if err != nil {
		return err
	}
	if revoked {
		return ErrNodeUnauthorized
	}
	if !VerifySecret(rawSecret, hash) {
		return ErrNodeUnauthorized
	}
	return nil
}

func (s *Service) RevokeNode(ctx context.Context, nodeID string, actorID uint) error {
	if err := s.repo.RevokeNode(ctx, nodeID); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "revoke node", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "cluster.node.revoke", Target: "node:" + nodeID, ActorID: actorID, Result: "success"})
	}
	return nil
}

// Heartbeat is called by an authenticated node on its own interval.
// Authentication is the caller's responsibility (Authenticate above);
// this only updates liveness.
func (s *Service) Heartbeat(ctx context.Context, nodeID string) error {
	ok, err := s.repo.Heartbeat(ctx, nodeID, s.clock.Now(), DefaultLeaseDuration)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "record heartbeat", err)
	}
	if !ok {
		return ErrNodeNotFound
	}
	return nil
}

// SweepExpiredLeases transitions Alive->Suspect->Unavailable for
// nodes whose lease has lapsed. Intended to run on a periodic
// background tick; also safe to call inline before a placement
// decision so a stale node is never selected.
func (s *Service) SweepExpiredLeases(ctx context.Context) (suspected, unavailable int64, err error) {
	suspected, unavailable, err = s.repo.MarkExpiredNodes(ctx, s.clock.Now())
	if err != nil {
		return 0, 0, kernel.Wrap(kernel.ErrCodeInternal, "sweep expired leases", err)
	}
	return suspected, unavailable, nil
}

func (s *Service) ListNodes(ctx context.Context) ([]Node, error) {
	return s.repo.ListNodes(ctx)
}

func (s *Service) GetNode(ctx context.Context, id string) (*Node, error) {
	return s.repo.GetNode(ctx, id)
}

// ── Operator maintenance commands ───────────────────────────────

func (s *Service) Cordon(ctx context.Context, nodeID, reason string, actorID uint) error {
	return s.transitionMaintenance(ctx, nodeID, NodeCordoned, reason, nil, actorID, "cluster.node.cordon")
}

func (s *Service) Uncordon(ctx context.Context, nodeID string, actorID uint) error {
	n, err := s.repo.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	ok, err := s.repo.TransitionMaintenance(ctx, nodeID, n.Status, NodeAlive, "", nil, n.RowVersion, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "uncordon node", err)
	}
	if !ok {
		return ErrVersionConflict
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "cluster.node.uncordon", Target: "node:" + nodeID, ActorID: actorID, Result: "success"})
	}
	return nil
}

func (s *Service) Drain(ctx context.Context, nodeID, reason string, until *time.Time, actorID uint) error {
	return s.transitionMaintenance(ctx, nodeID, NodeDraining, reason, until, actorID, "cluster.node.drain")
}

func (s *Service) Resume(ctx context.Context, nodeID string, actorID uint) error {
	return s.Uncordon(ctx, nodeID, actorID)
}

func (s *Service) transitionMaintenance(ctx context.Context, nodeID string, next NodeStatus, reason string, until *time.Time, actorID uint, auditAction string) error {
	if reason == "" {
		return ErrMaintenanceReasonRequired
	}
	n, err := s.repo.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	ok, err := s.repo.TransitionMaintenance(ctx, nodeID, n.Status, next, reason, until, n.RowVersion, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "transition node maintenance state", err)
	}
	if !ok {
		return ErrVersionConflict
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: auditAction, Target: "node:" + nodeID, ActorID: actorID, Result: "success", Reason: reason})
	}
	return nil
}

// ── Placement ────────────────────────────────────────────────────

// SelectNode picks a deterministic node for work requiring
// requiredCapability: the placeable (Alive-only), capability-matching
// node with the lexicographically smallest ID. Deterministic
// selection (not random) means repeated placement calls for the same
// requirement, absent membership changes, always agree — useful for
// idempotent scheduling and for tests.
func (s *Service) SelectNode(ctx context.Context, requiredCapability string) (*Node, error) {
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list nodes for placement", err)
	}
	var best *Node
	for i := range nodes {
		n := &nodes[i]
		if !n.IsPlaceable() {
			continue
		}
		if requiredCapability != "" && !n.HasCapability(requiredCapability) {
			continue
		}
		if best == nil || n.ID < best.ID {
			best = n
		}
	}
	if best == nil {
		return nil, ErrNoPlaceableNode
	}
	return best, nil
}

// ── Fenced leases (service ownership / failover) ─────────────────

func (s *Service) AcquireLease(ctx context.Context, resourceKey, nodeID string, duration time.Duration) (*LeaseHolder, error) {
	if duration <= 0 {
		duration = DefaultLeaseDuration
	}
	lease, err := s.repo.AcquireLease(ctx, resourceKey, nodeID, s.clock.Now(), duration)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "acquire lease", err)
	}
	if lease == nil || lease.NodeID != nodeID {
		return nil, ErrLeaseHeldByOther
	}
	return lease, nil
}

// ValidateFenceToken is what every owner-gated mutation must call
// before acting: presenting a token older than the current one means
// this caller lost ownership (a newer holder already took over) and
// must stop acting as owner immediately — this is the split-brain
// guard.
func (s *Service) ValidateFenceToken(ctx context.Context, resourceKey string, presentedToken int64) error {
	lease, err := s.repo.GetLease(ctx, resourceKey)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "get lease for fence validation", err)
	}
	if lease == nil || presentedToken < lease.FenceToken {
		return ErrStaleFenceToken
	}
	return nil
}
