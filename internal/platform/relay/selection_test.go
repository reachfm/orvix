package relay

import "testing"

func TestSelectFromPool_PriorityPicksLowestNumberFirst(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Priority: 20, Active: true}, Available: true},
		{Provider: Provider{ID: 2, Priority: 10, Active: true}, Available: true},
		{Provider: Provider{ID: 3, Priority: 30, Active: true}, Available: true},
	}
	ordered, ok := selectFromPool(providers, StrategyPriority, 42)
	if !ok {
		t.Fatal("expected a route to be selected")
	}
	if ordered[0].ID != 2 || ordered[1].ID != 1 || ordered[2].ID != 3 {
		t.Fatalf("unexpected order: %+v", ordered)
	}
}

func TestSelectFromPool_ExcludesUnavailableAndInactive(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Priority: 10, Active: true}, Available: false}, // circuit open
		{Provider: Provider{ID: 2, Priority: 20, Active: false}, Available: true}, // inactive
		{Provider: Provider{ID: 3, Priority: 30, Active: true}, Available: true},
	}
	ordered, ok := selectFromPool(providers, StrategyPriority, 1)
	if !ok || len(ordered) != 1 || ordered[0].ID != 3 {
		t.Fatalf("expected only provider 3, got %+v (ok=%v)", ordered, ok)
	}
}

func TestSelectFromPool_NoneAvailableReturnsFalse(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Active: true}, Available: false},
	}
	_, ok := selectFromPool(providers, StrategyPriority, 1)
	if ok {
		t.Fatal("expected no route available")
	}
}

func TestSelectFromPool_WeightedSameSeedIsDeterministic(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Priority: 10, Weight: 1, Active: true}, Available: true},
		{Provider: Provider{ID: 2, Priority: 10, Weight: 5, Active: true}, Available: true},
		{Provider: Provider{ID: 3, Priority: 10, Weight: 1, Active: true}, Available: true},
	}
	a, _ := selectFromPool(providers, StrategyWeighted, 777)
	b, _ := selectFromPool(providers, StrategyWeighted, 777)
	if a[0].ID != b[0].ID {
		t.Fatalf("expected the same seed to produce the same primary choice, got %d vs %d", a[0].ID, b[0].ID)
	}
}

func TestSelectFromPool_WeightedRespectsBetterPriorityTierFirst(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Priority: 10, Weight: 1, Active: true}, Available: true},
		{Provider: Provider{ID: 2, Priority: 20, Weight: 100, Active: true}, Available: true}, // huge weight but worse tier
	}
	ordered, ok := selectFromPool(providers, StrategyWeighted, 5)
	if !ok || ordered[0].ID != 1 {
		t.Fatalf("expected the better-priority-tier provider first regardless of weight, got %+v", ordered)
	}
}

func TestSelectFromPool_WeightedDistributionFavorsHigherWeight(t *testing.T) {
	providers := []availableProvider{
		{Provider: Provider{ID: 1, Priority: 10, Weight: 1, Active: true}, Available: true},
		{Provider: Provider{ID: 2, Priority: 10, Weight: 9, Active: true}, Available: true},
	}
	counts := map[uint]int{}
	for seed := int64(0); seed < 200; seed++ {
		ordered, _ := selectFromPool(providers, StrategyWeighted, seed)
		counts[ordered[0].ID]++
	}
	// With a 9:1 weight ratio, provider 2 should win primary selection
	// substantially more often than provider 1 — not asserting an
	// exact ratio (this is a deterministic LCG, not a true PRNG), just
	// that weight visibly matters.
	if counts[2] <= counts[1] {
		t.Fatalf("expected the 9x-weighted provider to win primary selection more often: counts=%v", counts)
	}
}
