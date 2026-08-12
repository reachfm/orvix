package billing

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

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
	return db, NewService(db, repo, nil, nil, nil)
}

func TestApplyAdjustment_CreditIncreasesBalance(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	_, err := svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 5000, "usd", "goodwill credit", 99, "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	b, err := svc.GetBalance(ctx, 1)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if b.BalanceCents != 5000 {
		t.Fatalf("expected balance 5000, got %d", b.BalanceCents)
	}
}

func TestApplyAdjustment_DebitDecreasesBalance(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 10000, "usd", "credit", 99, "")
	svc.ApplyAdjustment(ctx, 1, AdjustmentDebit, 3000, "usd", "correction", 99, "")
	b, _ := svc.GetBalance(ctx, 1)
	if b.BalanceCents != 7000 {
		t.Fatalf("expected balance 7000 (10000-3000), got %d", b.BalanceCents)
	}
}

func TestApplyAdjustment_RejectsNonPositiveAmount(t *testing.T) {
	_, svc := newTestService(t)
	_, err := svc.ApplyAdjustment(context.Background(), 1, AdjustmentCredit, 0, "usd", "reason", 1, "")
	if err != ErrInvalidAmount {
		t.Fatalf("expected ErrInvalidAmount for 0, got %v", err)
	}
	_, err = svc.ApplyAdjustment(context.Background(), 1, AdjustmentCredit, -100, "usd", "reason", 1, "")
	if err != ErrInvalidAmount {
		t.Fatalf("expected ErrInvalidAmount for negative, got %v", err)
	}
}

func TestApplyAdjustment_RequiresReason(t *testing.T) {
	_, svc := newTestService(t)
	_, err := svc.ApplyAdjustment(context.Background(), 1, AdjustmentCredit, 100, "usd", "", 1, "")
	if err != ErrReasonRequired {
		t.Fatalf("expected ErrReasonRequired, got %v", err)
	}
}

func TestApplyAdjustment_CurrencyMismatchRejected(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 100, "usd", "first", 1, "")
	_, err := svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 100, "eur", "second", 1, "")
	if err != ErrCurrencyMismatch {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}
}

func TestApplyAdjustment_IdempotentOnRetryKey(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	a1, err := svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 1000, "usd", "retry test", 1, "idem-1")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	a2, err := svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 1000, "usd", "retry test", 1, "idem-1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("expected the retry to return the same adjustment, got %d vs %d", a1.ID, a2.ID)
	}
	b, _ := svc.GetBalance(ctx, 1)
	if b.BalanceCents != 1000 {
		t.Fatalf("expected the balance to reflect only ONE application (1000), got %d", b.BalanceCents)
	}
}

func TestApplyAdjustment_TenantIsolation(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 5000, "usd", "t1 credit", 1, "")
	b2, err := svc.GetBalance(ctx, 2)
	if err != nil {
		t.Fatalf("get balance t2: %v", err)
	}
	if b2 != nil {
		t.Fatalf("expected tenant 2 to have no balance row, got %+v", b2)
	}
}

// TestApplyAdjustment_ConcurrentCreditsNeverLoseAnUpdate is the money-
// safety proof: N concurrent credits of a fixed amount must sum
// exactly — a lost update here would mean charging or crediting a
// customer the wrong amount.
func TestApplyAdjustment_ConcurrentCreditsNeverLoseAnUpdate(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 100, "usd", "concurrent credit", 1, fmt.Sprintf("concurrent-%d", i))
		}(i)
	}
	wg.Wait()

	b, err := svc.GetBalance(ctx, 1)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if b.BalanceCents != n*100 {
		t.Fatalf("expected balance = %d (no lost updates), got %d", n*100, b.BalanceCents)
	}
}

func TestApplyAdjustment_ConcurrentIdenticalIdempotencyKeyAppliesOnce(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.ApplyAdjustment(ctx, 1, AdjustmentCredit, 500, "usd", "race", 1, "same-key")
		}()
	}
	wg.Wait()

	b, err := svc.GetBalance(ctx, 1)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if b.BalanceCents != 500 {
		t.Fatalf("expected exactly one application of the idempotent adjustment (500), got %d", b.BalanceCents)
	}
}
