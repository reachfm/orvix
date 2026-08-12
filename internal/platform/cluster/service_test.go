package cluster

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, NewService(repo, nil, nil, nil)
}

func TestEnroll_SingleNodeBootstrapWorks(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	node, secret, err := svc.Enroll(ctx, Node{ID: "node-1", Role: "all-in-one", Capabilities: []string{"smtp", "delivery_worker"}, Version: "1.0.0"})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if secret == "" {
		t.Fatal("expected a non-empty raw enrollment secret")
	}
	if node.Status != NodeAlive {
		t.Fatalf("expected a freshly enrolled node to be alive, got %s", node.Status)
	}
	// A single enrolled node must be selectable for placement — the
	// single-node-compatibility requirement.
	selected, err := svc.SelectNode(ctx, "smtp")
	if err != nil {
		t.Fatalf("select node: %v", err)
	}
	if selected.ID != "node-1" {
		t.Fatalf("expected node-1 to be selected, got %s", selected.ID)
	}
}

func TestAuthenticate_RejectsWrongSecretAndRevokedNode(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	_, secret, _ := svc.Enroll(ctx, Node{ID: "node-1"})

	if err := svc.Authenticate(ctx, "node-1", secret); err != nil {
		t.Fatalf("expected the correct secret to authenticate, got %v", err)
	}
	if err := svc.Authenticate(ctx, "node-1", "wrong-secret"); err != ErrNodeUnauthorized {
		t.Fatalf("expected ErrNodeUnauthorized for a wrong secret, got %v", err)
	}
	if err := svc.Authenticate(ctx, "unknown-node", secret); err == nil {
		t.Fatal("expected an unknown node id to be rejected")
	}

	if err := svc.RevokeNode(ctx, "node-1", 99); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.Authenticate(ctx, "node-1", secret); err != ErrNodeUnauthorized {
		t.Fatalf("expected a revoked node to be rejected even with the correct secret, got %v", err)
	}
}

func TestHeartbeat_ExpiryTransitionsToSuspectThenUnavailable(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1"})

	// Directly age the lease just past expiry — but within the 30s
	// suspect grace window — simulating a single missed heartbeat
	// rather than a node gone for an hour (which would cross both
	// thresholds in one sweep; see the second phase below for that).
	recentlyExpired := time.Now().UTC().Add(-time.Second)
	if _, err := db.Exec(`UPDATE platform_cluster_nodes SET lease_expires_at=? WHERE id='node-1'`, recentlyExpired); err != nil {
		t.Fatal(err)
	}
	suspected, unavailable, err := svc.SweepExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if suspected != 1 || unavailable != 0 {
		t.Fatalf("expected 1 newly-suspected node, got suspected=%d unavailable=%d", suspected, unavailable)
	}
	n, _ := svc.GetNode(ctx, "node-1")
	if n.Status != NodeSuspect {
		t.Fatalf("expected suspect, got %s", n.Status)
	}

	// Age it further past the suspect->unavailable threshold (>30s stale).
	longExpired := time.Now().UTC().Add(-time.Hour)
	if _, err := db.Exec(`UPDATE platform_cluster_nodes SET lease_expires_at=? WHERE id='node-1'`, longExpired); err != nil {
		t.Fatal(err)
	}
	_, unavailable, err = svc.SweepExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if unavailable != 1 {
		t.Fatalf("expected the suspect node to become unavailable, got %d", unavailable)
	}
	n, _ = svc.GetNode(ctx, "node-1")
	if n.Status != NodeUnavailable {
		t.Fatalf("expected unavailable, got %s", n.Status)
	}
}

func TestHeartbeat_RecoversFromSuspectOnCheckIn(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1"})
	if _, err := db.Exec(`UPDATE platform_cluster_nodes SET status='suspect' WHERE id='node-1'`); err != nil {
		t.Fatal(err)
	}
	if err := svc.Heartbeat(ctx, "node-1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	n, _ := svc.GetNode(ctx, "node-1")
	if n.Status != NodeAlive {
		t.Fatalf("expected a fresh heartbeat to recover a suspect node to alive, got %s", n.Status)
	}
}

func TestHeartbeat_UnknownNodeReturnsNotFound(t *testing.T) {
	_, svc := newTestService(t)
	if err := svc.Heartbeat(context.Background(), "ghost"); err != ErrNodeNotFound {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}

// ── Cordon / drain / placement ────────────────────────────────────

func TestCordon_ExcludesNodeFromPlacement(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1", Capabilities: []string{"smtp"}})
	svc.Enroll(ctx, Node{ID: "node-2", Capabilities: []string{"smtp"}})

	if err := svc.Cordon(ctx, "node-1", "planned maintenance", 1); err != nil {
		t.Fatalf("cordon: %v", err)
	}
	selected, err := svc.SelectNode(ctx, "smtp")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if selected.ID != "node-2" {
		t.Fatalf("expected the cordoned node to be excluded, got %s", selected.ID)
	}
}

func TestCordon_RequiresReason(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1"})
	if err := svc.Cordon(ctx, "node-1", "", 1); err != ErrMaintenanceReasonRequired {
		t.Fatalf("expected ErrMaintenanceReasonRequired, got %v", err)
	}
}

func TestUncordon_RestoresPlaceability(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1", Capabilities: []string{"smtp"}})
	svc.Cordon(ctx, "node-1", "maintenance", 1)
	if _, err := svc.SelectNode(ctx, "smtp"); err != ErrNoPlaceableNode {
		t.Fatalf("expected no placeable node while cordoned, got %v", err)
	}
	if err := svc.Uncordon(ctx, "node-1", 1); err != nil {
		t.Fatalf("uncordon: %v", err)
	}
	selected, err := svc.SelectNode(ctx, "smtp")
	if err != nil || selected.ID != "node-1" {
		t.Fatalf("expected node-1 selectable again, got %v err=%v", selected, err)
	}
}

func TestDrain_ExcludesNodeAndRecordsReasonAndExpiry(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1", Capabilities: []string{"imap"}})
	until := time.Now().Add(2 * time.Hour)
	if err := svc.Drain(ctx, "node-1", "hardware replacement", &until, 1); err != nil {
		t.Fatalf("drain: %v", err)
	}
	n, _ := svc.GetNode(ctx, "node-1")
	if n.Status != NodeDraining || n.MaintenanceReason != "hardware replacement" || n.MaintenanceUntil == nil {
		t.Fatalf("unexpected node state after drain: %+v", n)
	}
	if _, err := svc.SelectNode(ctx, "imap"); err != ErrNoPlaceableNode {
		t.Fatalf("expected a draining node to be excluded from placement, got %v", err)
	}
}

func TestSelectNode_NoCapabilityMatchReturnsTypedError(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-1", Capabilities: []string{"imap"}})
	if _, err := svc.SelectNode(ctx, "pop3"); err != ErrNoPlaceableNode {
		t.Fatalf("expected ErrNoPlaceableNode, got %v", err)
	}
}

func TestSelectNode_DeterministicAcrossRepeatedCalls(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.Enroll(ctx, Node{ID: "node-b", Capabilities: []string{"smtp"}})
	svc.Enroll(ctx, Node{ID: "node-a", Capabilities: []string{"smtp"}})
	svc.Enroll(ctx, Node{ID: "node-c", Capabilities: []string{"smtp"}})
	for i := 0; i < 5; i++ {
		selected, err := svc.SelectNode(ctx, "smtp")
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if selected.ID != "node-a" {
			t.Fatalf("expected deterministic selection of node-a, got %s on iteration %d", selected.ID, i)
		}
	}
}

// ── Fenced leases / split-brain prevention ────────────────────────

func TestAcquireLease_SecondNodeCannotStealAnActiveLease(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	lease1, err := svc.AcquireLease(ctx, "smtp-primary", "node-a", time.Minute)
	if err != nil {
		t.Fatalf("node-a acquire: %v", err)
	}
	if lease1.NodeID != "node-a" || lease1.FenceToken != 1 {
		t.Fatalf("unexpected first lease: %+v", lease1)
	}
	_, err = svc.AcquireLease(ctx, "smtp-primary", "node-b", time.Minute)
	if err != ErrLeaseHeldByOther {
		t.Fatalf("expected ErrLeaseHeldByOther, got %v", err)
	}
}

func TestAcquireLease_TakeoverAfterExpiryIncrementsFenceToken(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()
	lease1, err := svc.AcquireLease(ctx, "smtp-primary", "node-a", time.Minute)
	if err != nil {
		t.Fatalf("node-a acquire: %v", err)
	}
	// Force the lease to look expired.
	if _, err := db.Exec(`UPDATE platform_cluster_leases SET expires_at=? WHERE resource_key='smtp-primary'`, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	lease2, err := svc.AcquireLease(ctx, "smtp-primary", "node-b", time.Minute)
	if err != nil {
		t.Fatalf("node-b takeover: %v", err)
	}
	if lease2.NodeID != "node-b" {
		t.Fatalf("expected node-b to take over, got %s", lease2.NodeID)
	}
	if lease2.FenceToken <= lease1.FenceToken {
		t.Fatalf("expected the fence token to strictly increase on takeover: before=%d after=%d", lease1.FenceToken, lease2.FenceToken)
	}

	// The old holder's stale token must now be rejected.
	if err := svc.ValidateFenceToken(ctx, "smtp-primary", lease1.FenceToken); err != ErrStaleFenceToken {
		t.Fatalf("expected the old holder's stale token to be rejected, got %v", err)
	}
	if err := svc.ValidateFenceToken(ctx, "smtp-primary", lease2.FenceToken); err != nil {
		t.Fatalf("expected the new holder's current token to validate, got %v", err)
	}
}

// TestAcquireLease_SimultaneousAcquisitionOnlyOneWins is the real
// split-brain proof: N goroutines racing to acquire the same never-
// before-held lease must result in exactly one winner.
func TestAcquireLease_SimultaneousAcquisitionOnlyOneWins(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]*LeaseHolder, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := svc.AcquireLease(ctx, "contested-resource", nodeName(i), time.Minute)
			if err == nil {
				results[i] = lease
			}
		}(i)
	}
	wg.Wait()

	winners := map[string]bool{}
	for _, r := range results {
		if r != nil {
			winners[r.NodeID] = true
		}
	}
	if len(winners) != 1 {
		t.Fatalf("expected exactly one node to win the contested lease, got winners=%v", winners)
	}
}

func nodeName(i int) string {
	names := []string{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}
	return names[i%len(names)]
}
