package relay

// F7 write-time validation tests: unsafe targets, negative rate
// limits, and unbounded override lifetimes are rejected at the service
// boundary, not only at use time.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestF7_CreateProviderRejectsUnsafeTargetAtWriteTime(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "localhost", "::1"} {
		_, err := svc.CreateProvider(ctx, Provider{
			PoolID: pool.ID, TenantID: 1, Host: host, Port: 25,
			ConnSecurity: ConnSecurityStartTLS, Active: true,
		}, "", testActor)
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("host %q must be rejected at write time with ErrUnsafeTarget, got %v", host, err)
		}
	}
}

func TestF7_NegativeRateLimitRejected(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	_, err = svc.CreateProvider(ctx, Provider{
		PoolID: pool.ID, TenantID: 1, Host: "relay.t1.example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, Active: true, RateLimitPerMin: -3,
	}, "", testActor)
	if err == nil {
		t.Fatal("a negative rate limit must be rejected")
	}
	// Zero (unlimited) and positive are fine.
	for _, rl := range []int{0, 10} {
		if _, err := svc.CreateProvider(ctx, Provider{
			PoolID: pool.ID, TenantID: 1, Host: "relay.t1.example.com", Port: 587,
			ConnSecurity: ConnSecurityStartTLS, Active: true, RateLimitPerMin: rl,
		}, "", testActor); err != nil {
			t.Fatalf("rate limit %d must be accepted, got %v", rl, err)
		}
	}
}

func TestF7_NegativeRateLimitRejectedOnPlatformCreateAndUpdate(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	in := baseCreate()
	in.Name = "neg-rl"
	in.RateLimitPerMin = -1
	if _, err := svc.CreateRelay(ctx, in, testActor); err == nil {
		t.Fatal("a negative rate limit must be rejected on platform create")
	}
	ok, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	neg := -1
	if _, err := svc.UpdateRelay(ctx, ok.ID, ok.Version, RelayUpdateInput{RateLimitPerMin: &neg}, testActor); err == nil {
		t.Fatal("a negative rate limit must be rejected on update")
	}
}

func TestF7_EmergencyOverrideLifetimeCapped(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pool, err := svc.CreatePool(ctx, Pool{Scope: ScopeTenant, TenantID: 1, Name: "t1-pool", Strategy: StrategyPriority}, testActor)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	// 8 days exceeds the 7-day cap.
	_, err = svc.SetEmergencyOverride(ctx, 1, pool.ID, 99, "incident", time.Now().Add(8*24*time.Hour))
	if err == nil {
		t.Fatal("an 8-day override must be rejected")
	}
	// 6 days is within the cap.
	_, err = svc.SetEmergencyOverride(ctx, 1, pool.ID, 99, "incident", time.Now().Add(6*24*time.Hour))
	if err != nil {
		t.Fatalf("a 6-day override must be accepted, got %v", err)
	}
}
