package domainlifecycle

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/domainregistry"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "dlc.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestService(t *testing.T) (*Service, *Repository, *domainregistry.Service) {
	t.Helper()
	db := newTestDB(t)
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	regRepo := domainregistry.NewRepository(db)
	if err := regRepo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure registry table: %v", err)
	}
	regSvc := domainregistry.NewService(regRepo)
	svc := NewService(repo, regSvc, nil)
	return svc, repo, regSvc
}

func TestRegister_StartsInPendingState(t *testing.T) {
	svc, _, _ := newTestService(t)
	d, err := svc.Register(context.Background(), 1, "example.test")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if d.State != StatePending {
		t.Fatalf("expected pending, got %s", d.State)
	}
}

func TestRegister_DuplicateNameIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, 1, "dup.test"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err := svc.Register(ctx, 1, "dup.test")
	if err != ErrDomainNameTaken {
		t.Fatalf("expected ErrDomainNameTaken, got %v", err)
	}
}

func TestTransition_FollowsHappyPathToActive(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Register(ctx, 1, "happy.test")

	for _, next := range []State{StatePendingDNS, StateVerifying, StateActive} {
		var err error
		d, err = svc.Transition(ctx, d.ID, next)
		if err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
		if d.State != next {
			t.Fatalf("expected state %s, got %s", next, d.State)
		}
	}
}

func TestTransition_SkippingStatesIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Register(ctx, 1, "skip.test")

	_, err := svc.Transition(ctx, d.ID, StateActive)
	if err == nil {
		t.Fatal("expected pending->active to be rejected without passing through pending_dns/verifying")
	}
}

func TestTransition_FromDeletedIsTerminalAndRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Register(ctx, 1, "term.test")
	d, _ = svc.Transition(ctx, d.ID, StateDeleting)
	d, err := svc.Transition(ctx, d.ID, StateDeleted)
	if err != nil {
		t.Fatalf("deleting->deleted: %v", err)
	}
	if _, err := svc.Transition(ctx, d.ID, StateActive); err == nil {
		t.Fatal("expected deleted to be a terminal state with no valid outgoing transitions")
	}
}

func TestTransition_SyncsProtocolFacingRegistryStatus(t *testing.T) {
	svc, _, regSvc := newTestService(t)
	ctx := context.Background()
	if _, err := regSvc.CreateDomain(ctx, &domainregistry.CreateDomainRequest{Name: "sync.test"}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	d, _ := svc.Register(ctx, 1, "sync.test")
	d, _ = svc.Transition(ctx, d.ID, StatePendingDNS)
	d, _ = svc.Transition(ctx, d.ID, StateVerifying)
	if _, err := svc.Transition(ctx, d.ID, StateActive); err != nil {
		t.Fatalf("transition to active: %v", err)
	}

	reg, err := regSvc.GetByName(ctx, "sync.test")
	if err != nil || reg == nil {
		t.Fatalf("expected registry row, err=%v", err)
	}
	if reg.Status != domainregistry.DomainActive {
		t.Fatalf("expected registry status active, got %s", reg.Status)
	}

	if _, err := svc.Transition(ctx, d.ID, StateSuspended); err != nil {
		t.Fatalf("transition to suspended: %v", err)
	}
	reg, _ = regSvc.GetByName(ctx, "sync.test")
	if reg.Status != domainregistry.DomainSuspended {
		t.Fatalf("expected registry status suspended after suspend, got %s", reg.Status)
	}
}

// TestTransition_ConcurrentTransitionsOnlyOneWins proves the optimistic
// concurrency guard actually prevents a lost update: two goroutines
// racing to move the same domain out of "active" must not both
// succeed.
func TestTransition_ConcurrentTransitionsOnlyOneWins(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Register(ctx, 1, "race.test")
	d, _ = svc.Transition(ctx, d.ID, StatePendingDNS)
	d, _ = svc.Transition(ctx, d.ID, StateVerifying)
	d, _ = svc.Transition(ctx, d.ID, StateActive)

	// Both goroutines race for the SAME target (active->deleting), which
	// is only valid once: whichever wins moves the row to "deleting",
	// and a retry of the loser then sees deleting->deleting, which is
	// not in the transition table (no self-transition), so the second
	// attempt must fail even after its retry — this is what actually
	// proves mutual exclusion, unlike racing for two targets that
	// happen to chain into each other.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Transition(ctx, d.ID, StateDeleting)
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one concurrent transition to win, got %d successes: %v", successes, results)
	}

	final, err := svc.repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get final: %v", err)
	}
	if final.State != StateDeleting {
		t.Fatalf("unexpected final state %s", final.State)
	}
}

func TestCanTransition_TableMatchesSpecifiedLifecycle(t *testing.T) {
	cases := []struct {
		from, to State
		want     bool
	}{
		{StatePending, StatePendingDNS, true},
		{StatePending, StateActive, false},
		{StateActive, StateDegraded, true},
		{StateDegraded, StateActive, true},
		{StateSuspended, StateActive, true},
		{StateDeleted, StatePending, false},
		{StateFailed, StatePendingDNS, true},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
