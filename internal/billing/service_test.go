package billing

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSeedDefaultPlans(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	plans, err := svc.ListPlans()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 4 {
		t.Fatalf("expected 4 plans, got %d", len(plans))
	}
}

func TestCreateSubscription(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	sub, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != SubActive {
		t.Fatalf("expected active, got %s", sub.Status)
	}
}

func TestDuplicateSubscriptionFails(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != ErrTenantAlreadyHasSub {
		t.Fatalf("expected ErrTenantAlreadyHasSub, got %v", err)
	}
}

func TestSubscriptionStateTransitions(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.TransitionState(1, SubPastDue); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.GetSubscription(1)
	if sub.Status != SubPastDue {
		t.Fatalf("expected past_due, got %s", sub.Status)
	}
}

func TestInvalidTransition(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.TransitionState(1, SubCancelled); err != nil {
		t.Fatal(err)
	}
	if err := svc.TransitionState(1, SubActive); err != ErrInvalidTransition {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestUsageIncrement(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	usageSvc := NewUsageService(db)
	if err := usageSvc.IncrementEmailsSent(1, 5); err != nil {
		t.Fatal(err)
	}
	if err := usageSvc.IncrementEmailsSent(1, 3); err != nil {
		t.Fatal(err)
	}
	rec, err := usageSvc.GetCurrentUsage(1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.EmailsSent != 8 {
		t.Fatalf("expected 8 emails sent, got %d", rec.EmailsSent)
	}
}

func TestQuotaService(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0); err != nil {
		t.Fatal(err)
	}
	quotaSvc := NewQuotaService(db, svc)
	r := quotaSvc.CanCreateDomain(1, 0)
	if !r.Allowed {
		t.Fatalf("expected allowed, got %s", r.Reason)
	}
	r = quotaSvc.CanCreateDomain(1, 1)
	if r.Allowed {
		t.Fatal("expected blocked after limit")
	}
}

func TestNoopPaymentProvider(t *testing.T) {
	p := NewNoopPaymentProvider()
	s, err := p.CreateCheckout(1, PlanBusiness, IntervalMonthly, "https://example.com/return")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID == "" {
		t.Fatal("expected session ID")
	}
	ev, err := p.VerifyWebhook([]byte(`{}`), "sig")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "checkout.session.completed" {
		t.Fatalf("expected checkout.session.completed, got %s", ev.Type)
	}
}
