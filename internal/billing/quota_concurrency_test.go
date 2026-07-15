package billing

import (
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// setupConcurrencyDB opens an in-memory database with
// MaxOpenConns=1 so all goroutines share the same connection.
func setupConcurrencyDB(t *testing.T) (*sql.DB, *Service, *QuotaService, *UsageService) {
	t.Helper()
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	quotaSvc := NewQuotaService(db, svc)
	usageSvc := NewUsageService(db)
	return db, svc, quotaSvc, usageSvc
}

func TestQuotaConcurrency_CannotExceedDomainLimit(t *testing.T) {
	_, svc, quotaSvc, _ := setupConcurrencyDB(t)
	tenantID := uint(1)

	if _, err := svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}

	var allowed int64
	var wg sync.WaitGroup
	goroutines := 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(current int) {
			defer wg.Done()
			result := quotaSvc.CanCreateDomain(tenantID, current)
			if result.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}(i)
	}
	wg.Wait()

	plan, _ := svc.GetPlan(PlanFree)
	if int(allowed) > plan.MaxDomains {
		t.Errorf("quota concurrency: allowed %d domain creations, expected at most %d (plan limit)", allowed, plan.MaxDomains)
	}
	if allowed == 0 {
		t.Error("quota concurrency: expected at least 1 allowed (current=0 should pass)")
	}
	t.Logf("pass: allowed=%d (plan limit=%d)", allowed, plan.MaxDomains)
}

func TestQuotaConcurrency_CannotExceedMailboxLimit(t *testing.T) {
	_, svc, quotaSvc, _ := setupConcurrencyDB(t)
	tenantID := uint(1)

	if _, err := svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	plan, _ := svc.GetPlan(PlanFree)
	maxMailboxes := plan.MaxMailboxes

	var allowed int64
	var wg sync.WaitGroup
	goroutines := 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(current int) {
			defer wg.Done()
			result := quotaSvc.CanCreateMailbox(tenantID, current)
			if result.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}(i)
	}
	wg.Wait()

	if int(allowed) > maxMailboxes {
		t.Errorf("quota concurrency: allowed %d mailbox creations, expected at most %d (plan limit)", allowed, maxMailboxes)
	}
	if allowed == 0 {
		t.Error("quota concurrency: expected at least 1 allowed (current=0 should pass)")
	}
	t.Logf("pass: allowed=%d (plan limit=%d)", allowed, maxMailboxes)
}

func TestQuotaConcurrency_SendLimit(t *testing.T) {
	_, svc, quotaSvc, _ := setupConcurrencyDB(t)
	tenantID := uint(1)

	if _, err := svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	plan, _ := svc.GetPlan(PlanFree)

	var wg sync.WaitGroup
	workers := 20

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := quotaSvc.CanSendEmail(tenantID, int64(plan.SendLimitDay))
			if result.Allowed {
				t.Error("send limit: at limit should be denied")
			}
			result2 := quotaSvc.CanSendEmail(tenantID, int64(plan.SendLimitDay-1))
			if !result2.Allowed {
				t.Error("send limit: below limit should be allowed")
			}
		}()
	}
	wg.Wait()
}

func TestQuotaConcurrency_ParallelReadsConsistent(t *testing.T) {
	_, svc, quotaSvc, _ := setupConcurrencyDB(t)
	tenantID := uint(1)

	if _, err := svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	workers := 20

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := quotaSvc.CanCreateDomain(tenantID, 0)
			if !result.Allowed {
				t.Error("parallel read: CanCreateDomain(0) should be allowed")
			}
			result2 := quotaSvc.CanCreateMailbox(tenantID, 999)
			if result2.Allowed {
				t.Error("parallel read: CanCreateMailbox(999) should be denied")
			}
		}()
	}
	wg.Wait()
}
