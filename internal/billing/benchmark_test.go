package billing

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func benchmarkSetup(b *testing.B) (*sql.DB, *Service) {
	b.Helper()
	db := newTestDB(b)
	svc := NewService(db)
	if err := svc.SeedDefaultPlans(); err != nil {
		b.Fatal(err)
	}
	return db, svc
}

func BenchmarkSeedDefaultPlans(b *testing.B) {
	for i := 0; i < b.N; i++ {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			b.Fatal(err)
		}
		CreateTables(db)
		svc := NewService(db)
		svc.SeedDefaultPlans()
		db.Close()
	}
}

func BenchmarkCreateSubscription(b *testing.B) {
	_, svc := benchmarkSetup(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tenantID := uint(i + 1)
		svc.CreateSubscription(tenantID, PlanFree, IntervalMonthly, 0)
	}
}

func BenchmarkGetSubscription(b *testing.B) {
	_, svc := benchmarkSetup(b)
	svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.GetSubscription(1)
	}
}

func BenchmarkTransitionState(b *testing.B) {
	_, svc := benchmarkSetup(b)
	svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	states := []SubscriptionStatus{SubPastDue, SubActive, SubCancelled, SubExpired}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.CreateSubscription(uint(i+2), PlanFree, IntervalMonthly, 0)
		svc.TransitionState(uint(i+2), states[i%len(states)])
	}
}

func BenchmarkListPlans(b *testing.B) {
	_, svc := benchmarkSetup(b)
	svc.SeedDefaultPlans()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.ListPlans()
	}
}

func BenchmarkUsageIncrement(b *testing.B) {
	db, svc := benchmarkSetup(b)
	svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	usageSvc := NewUsageService(db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		usageSvc.IncrementEmailsSent(1, 1)
	}
}

func BenchmarkGetCurrentUsage(b *testing.B) {
	db, svc := benchmarkSetup(b)
	svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	usageSvc := NewUsageService(db)
	usageSvc.IncrementEmailsSent(1, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		usageSvc.GetCurrentUsage(1)
	}
}

func BenchmarkQuotaCheck(b *testing.B) {
	db, svc := benchmarkSetup(b)
	svc.CreateSubscription(1, PlanFree, IntervalMonthly, 0)
	quotaSvc := NewQuotaService(db, svc)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		quotaSvc.CanCreateDomain(1, i%2)
	}
}
