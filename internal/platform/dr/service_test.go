package dr

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fakeLeases is an in-memory LeaseAcquirer used to test coordination
// logic without a real cluster schema.
type fakeLeases struct {
	mu      sync.Mutex
	holders map[string]string
}

func newFakeLeases() *fakeLeases { return &fakeLeases{holders: map[string]string{}} }

func (f *fakeLeases) AcquireLease(ctx context.Context, resourceKey, nodeID string, duration time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.holders[resourceKey]; ok && existing != nodeID {
		return existing, nil // held by someone else — Service compares holder != its own nodeID
	}
	f.holders[resourceKey] = nodeID
	return nodeID, nil
}

type fakeBackups struct {
	mu         sync.Mutex
	created    []string
	restored   []string
	restoreErr error
	verified   []VerifiedBackup
	callCount  int
}

func (f *fakeBackups) CreateBackup(ctx context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	id := "backup-" + name
	f.created = append(f.created, id)
	return id, nil
}

func (f *fakeBackups) RestoreBackup(ctx context.Context, backupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.restored = append(f.restored, backupID)
	return nil
}

func (f *fakeBackups) ListVerifiedBackups(ctx context.Context) ([]VerifiedBackup, error) {
	return f.verified, nil
}

func newTestService(t *testing.T, nodeID string, leases *fakeLeases, backups *fakeBackups) (*sql.DB, *Service) {
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
	return db, NewService(repo, leases, backups, nil, nil, nodeID, nil)
}

func TestCoordinatedBackup_Succeeds(t *testing.T) {
	_, svc := newTestService(t, "node-a", newFakeLeases(), &fakeBackups{})
	id, err := svc.CoordinatedBackup(context.Background(), "nightly", 1)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty backup id")
	}
}

// TestCoordinatedBackup_TwoDifferentNodesRacingOnlyOneWins proves the
// cross-node property the durable lease exists for: two DISTINCT
// nodes (as in a real cluster, each its own process) racing to start
// a backup at the same moment — exactly one must win. (A lease
// legitimately permits its OWN current holder to renew/reacquire —
// that's correct behavior for a long-running operation extending its
// lease — so concurrent calls from the SAME node/Service instance are
// a separate concern, guarded by Service's own process-local mutex;
// see TestCoordinatedBackup_SameNodeConcurrentCallsAreSerialized.)
func TestCoordinatedBackup_TwoDifferentNodesRacingOnlyOneWins(t *testing.T) {
	leases := newFakeLeases()
	backups := &fakeBackups{}
	_, svc1 := newTestService(t, "node-a", leases, backups)
	svc2 := &Service{repo: svc1.repo, leases: leases, backups: backups, nodeID: "node-b", clock: svc1.clock, resources: map[string]*sync.Mutex{}}

	const n = 10
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := svc1
			if i%2 == 0 {
				svc = svc2
			}
			_, err := svc.CoordinatedBackup(context.Background(), "concurrent", 1)
			results[i] = err
		}(i)
	}
	wg.Wait()

	winner := map[string]bool{}
	for i, err := range results {
		if err == nil {
			node := "node-b"
			if i%2 != 0 {
				node = "node-a"
			}
			winner[node] = true
		}
	}
	if len(winner) != 1 {
		t.Fatalf("expected exactly one node to ever win the lease across the race, got %v", winner)
	}
}

// TestCoordinatedBackup_SameNodeConcurrentCallsAreSerialized proves
// the process-local mutex property: even though a lease legitimately
// permits its own holder to renew, two goroutines within the SAME
// Service instance must never have overlapping backup operations —
// proven here by a fakeBackups that fails if CreateBackup is ever
// entered while another call is still in flight.
func TestCoordinatedBackup_SameNodeConcurrentCallsAreSerialized(t *testing.T) {
	backups := &trackingBackups{}
	_, svc := newTestService(t, "node-a", newFakeLeases(), nil)
	svc.backups = backups

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.CoordinatedBackup(context.Background(), "serialized", 1)
		}()
	}
	wg.Wait()

	if backups.maxConcurrent > 1 {
		t.Fatalf("expected backup operations from the same node to never overlap, observed max concurrency %d", backups.maxConcurrent)
	}
	if backups.calls != n {
		t.Fatalf("expected all %d calls to eventually complete, got %d", n, backups.calls)
	}
}

// trackingBackups records the maximum number of concurrently
// in-flight CreateBackup calls it ever observed.
type trackingBackups struct {
	mu            sync.Mutex
	inFlight      int
	maxConcurrent int
	calls         int
}

func (f *trackingBackups) CreateBackup(ctx context.Context, name string) (string, error) {
	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.maxConcurrent {
		f.maxConcurrent = f.inFlight
	}
	f.calls++
	f.mu.Unlock()

	time.Sleep(2 * time.Millisecond) // widen the window a real race would need to land in

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return "backup-x", nil
}

func (f *trackingBackups) RestoreBackup(ctx context.Context, backupID string) error { return nil }

func (f *trackingBackups) ListVerifiedBackups(ctx context.Context) ([]VerifiedBackup, error) {
	return nil, nil
}

func TestCoordinatedRestore_RequiresExactConfirmationPhrase(t *testing.T) {
	_, svc := newTestService(t, "node-a", newFakeLeases(), &fakeBackups{})
	err := svc.CoordinatedRestore(context.Background(), "backup-1", "wrong-phrase", 1)
	if err != ErrConfirmationRequired {
		t.Fatalf("expected ErrConfirmationRequired, got %v", err)
	}
}

func TestCoordinatedRestore_SucceedsWithCorrectConfirmation(t *testing.T) {
	backups := &fakeBackups{}
	_, svc := newTestService(t, "node-a", newFakeLeases(), backups)
	if err := svc.CoordinatedRestore(context.Background(), "backup-1", RestoreConfirmationPhrase, 1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(backups.restored) != 1 || backups.restored[0] != "backup-1" {
		t.Fatalf("expected backup-1 to have been restored, got %v", backups.restored)
	}
}

func TestCoordinatedRestore_LeaseHeldByAnotherNodeIsRejected(t *testing.T) {
	leases := newFakeLeases()
	leases.holders[RestoreLeaseResource] = "other-node"
	_, svc := newTestService(t, "node-a", leases, &fakeBackups{})
	err := svc.CoordinatedRestore(context.Background(), "backup-1", RestoreConfirmationPhrase, 1)
	if err != ErrOperationInProgress {
		t.Fatalf("expected ErrOperationInProgress, got %v", err)
	}
}

func TestReadiness_NoBackupsAlertsMissing(t *testing.T) {
	_, svc := newTestService(t, "node-a", newFakeLeases(), &fakeBackups{})
	r, err := svc.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if !r.MissingBackupAlert {
		t.Fatal("expected MissingBackupAlert=true with zero verified backups")
	}
}

func TestReadiness_RecentVerifiedBackupNoAlert(t *testing.T) {
	now := time.Now().UTC()
	backups := &fakeBackups{verified: []VerifiedBackup{{ID: "b1", CompletedAt: &now}}}
	_, svc := newTestService(t, "node-a", newFakeLeases(), backups)
	r, err := svc.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.MissingBackupAlert {
		t.Fatal("expected no alert for a just-completed backup")
	}
	if r.LastVerifiedBackupAt == nil || !r.LastVerifiedBackupAt.Equal(now) {
		t.Fatalf("expected LastVerifiedBackupAt=%v, got %v", now, r.LastVerifiedBackupAt)
	}
}

func TestReadiness_StaleBackupAlertsMissing(t *testing.T) {
	old := time.Now().UTC().Add(-72 * time.Hour)
	backups := &fakeBackups{verified: []VerifiedBackup{{ID: "b1", CompletedAt: &old}}}
	_, svc := newTestService(t, "node-a", newFakeLeases(), backups)
	svc.MissingBackupThreshold = 48 * time.Hour
	r, err := svc.Readiness(context.Background())
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if !r.MissingBackupAlert {
		t.Fatal("expected an alert for a 72h-old backup with a 48h threshold")
	}
}

func TestRecordDrillAndReadiness_ReflectsLastSuccessfulDrill(t *testing.T) {
	_, svc := newTestService(t, "node-a", newFakeLeases(), &fakeBackups{})
	ctx := context.Background()
	if _, err := svc.RecordDrill(ctx, "backup-1", DrillFailed, 5000, "staging disk full", 1); err != nil {
		t.Fatalf("record failed drill: %v", err)
	}
	if _, err := svc.RecordDrill(ctx, "backup-2", DrillSucceeded, 12000, "", 1); err != nil {
		t.Fatalf("record succeeded drill: %v", err)
	}

	r, err := svc.Readiness(ctx)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if r.LastSuccessfulDrillAt == nil {
		t.Fatal("expected LastSuccessfulDrillAt to be set")
	}
	if r.LastDrillDurationMS != 12000 {
		t.Fatalf("expected the successful drill's duration (12000ms), got %d", r.LastDrillDurationMS)
	}

	drills, err := svc.ListDrills(ctx, 10)
	if err != nil {
		t.Fatalf("list drills: %v", err)
	}
	if len(drills) != 2 {
		t.Fatalf("expected 2 drills recorded, got %d", len(drills))
	}
}
