package domain

// Tests for the canonical domain operability guard (Phase 8 C1).

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

func seedOperabilityDomain(t *testing.T, db *sql.DB, tenantID uint, name, status string) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := db.Exec(
		"INSERT INTO coremail_domains (tenant_id, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		tenantID, name, status, now, now)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed domain id: %v", err)
	}
	return uint(id)
}

func TestCheckOperabilityTx_Active(t *testing.T) {
	db := newDomainTestDB(t)
	seedOperabilityDomain(t, db, 1, "active.example", "active")
	repo := NewDomainAdminRepo(db)

	out := repo.CheckOperabilityTx(context.Background(), "active.example", 1, false)
	if !out.Operational() {
		t.Fatalf("expected operational, got err=%v", out.Err)
	}
	if out.DomainID == 0 {
		t.Fatal("expected a non-zero domain id for an operational domain")
	}
}

func TestCheckOperabilityTx_EveryNonActiveStatus(t *testing.T) {
	cases := []struct {
		status  string
		wantErr error
	}{
		{"disabled", ErrDomainDisabled},
		{"suspended", ErrDomainSuspended},
		{"locked", ErrDomainLocked},
		{"some-legacy-value-nobody-wrote", ErrDomainUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			db := newDomainTestDB(t)
			seedOperabilityDomain(t, db, 1, "d.example", tc.status)
			repo := NewDomainAdminRepo(db)

			out := repo.CheckOperabilityTx(context.Background(), "d.example", 1, false)
			if out.Operational() {
				t.Fatalf("expected rejection for status=%s, got operational", tc.status)
			}
			if !errors.Is(out.Err, tc.wantErr) {
				t.Fatalf("status=%s: expected %v, got %v", tc.status, tc.wantErr, out.Err)
			}
		})
	}
}

func TestCheckOperabilityTx_UnknownNameIsNotFound(t *testing.T) {
	db := newDomainTestDB(t)
	repo := NewDomainAdminRepo(db)

	out := repo.CheckOperabilityTx(context.Background(), "nope.example", 1, false)
	if !errors.Is(out.Err, ErrDomainNotFound) {
		t.Fatalf("expected ErrDomainNotFound, got %v", out.Err)
	}
}

func TestCheckOperabilityTx_CrossTenantIsNotFoundNotForbidden(t *testing.T) {
	db := newDomainTestDB(t)
	seedOperabilityDomain(t, db, 1, "tenant1.example", "active")
	repo := NewDomainAdminRepo(db)

	// Same domain name, wrong tenant in the request.
	out := repo.CheckOperabilityTx(context.Background(), "tenant1.example", 2, false)
	if !errors.Is(out.Err, ErrDomainNotFound) {
		t.Fatalf("cross-tenant lookup must resolve to the same ErrDomainNotFound as a genuinely missing domain (no existence disclosure), got %v", out.Err)
	}
}

func TestCheckOperabilityTx_SoftDeletedIsNotFound(t *testing.T) {
	db := newDomainTestDB(t)
	id := seedOperabilityDomain(t, db, 1, "gone.example", "active")
	if _, err := db.Exec("UPDATE coremail_domains SET deleted_at = ? WHERE id = ?", time.Now().UTC(), id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	repo := NewDomainAdminRepo(db)

	out := repo.CheckOperabilityTx(context.Background(), "gone.example", 1, false)
	if !errors.Is(out.Err, ErrDomainNotFound) {
		t.Fatalf("expected ErrDomainNotFound for a soft-deleted domain, got %v", out.Err)
	}
}

func TestCheckOperabilityTx_RepositoryFailureIsNotSilentlyActive(t *testing.T) {
	db := newDomainTestDB(t)
	seedOperabilityDomain(t, db, 1, "active.example", "active")
	repo := NewDomainAdminRepo(db)
	db.Close() // force every subsequent query to fail

	out := repo.CheckOperabilityTx(context.Background(), "active.example", 1, false)
	if out.Operational() {
		t.Fatal("a repository failure must never be reported as operational")
	}
	if errors.Is(out.Err, ErrDomainNotFound) {
		t.Fatal("a repository failure must not be misreported as ErrDomainNotFound (that implies the domain genuinely doesn't exist)")
	}
	if out.Err == nil {
		t.Fatal("expected a non-nil error")
	}
}

func TestCheckOperabilityByIDTx_MatchesByNameEquivalent(t *testing.T) {
	db := newDomainTestDB(t)
	id := seedOperabilityDomain(t, db, 1, "byid.example", "suspended")
	repo := NewDomainAdminRepo(db)

	out := repo.CheckOperabilityByIDTx(context.Background(), id, 1, false)
	if !errors.Is(out.Err, ErrDomainSuspended) {
		t.Fatalf("expected ErrDomainSuspended, got %v", out.Err)
	}

	// Wrong tenant for the same id.
	out2 := repo.CheckOperabilityByIDTx(context.Background(), id, 2, false)
	if !errors.Is(out2.Err, ErrDomainNotFound) {
		t.Fatalf("cross-tenant by-id lookup must be ErrDomainNotFound, got %v", out2.Err)
	}
}

func TestCheckOperabilityTx_TransactionBoundUse(t *testing.T) {
	db := newDomainTestDB(t)
	seedOperabilityDomain(t, db, 1, "tx.example", "active")
	repo := NewDomainAdminRepo(db)

	tx, err := repo.BeginTx(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	txRepo := repo.WithTx(tx)

	out := txRepo.CheckOperabilityTx(context.Background(), "tx.example", 1, true)
	if !out.Operational() {
		t.Fatalf("expected operational inside the transaction, got %v", out.Err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCheckOperabilityTx_ConcurrentDeactivationVsLockedRead proves the
// FOR UPDATE lock (Postgres-only; this test runs against SQLite where
// it's a documented no-op, so it instead proves the SQLite path is at
// least serialized by the DB's own write-lock and never observes a
// half-applied state) — a deactivation committed after this check
// started must not be visible mid-check, and the check must not block
// forever.
func TestCheckOperabilityTx_ConcurrentDeactivationVsLockedRead(t *testing.T) {
	db := newDomainTestDB(t)
	id := seedOperabilityDomain(t, db, 1, "race.example", "active")
	repo := NewDomainAdminRepo(db)

	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan OperabilityOutcome, 8)
	go func() {
		defer wg.Done()
		for i := 0; i < 4; i++ {
			results <- repo.CheckOperabilityTx(context.Background(), "race.example", 1, false)
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE id = ?", id)
	}()
	wg.Wait()
	close(results)
	for r := range results {
		// Every observed result must be a coherent, real status — never
		// a corrupted/partial read (impossible to assert byte-for-byte
		// with SQLite's coarse locking, but the outcome must always be
		// one of the two real states, never a spurious error).
		if r.Err != nil && !errors.Is(r.Err, ErrDomainDisabled) {
			t.Fatalf("unexpected outcome during concurrent status change: %v", r.Err)
		}
	}
}
