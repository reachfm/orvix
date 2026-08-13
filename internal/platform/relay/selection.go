package relay

import "sort"

// availableProvider is the input to selectFromPool: a provider plus
// its live availability (circuit state resolved against "now", rate
// limit checked) — selectFromPool itself does no I/O and no clock
// reads, so it is fully deterministic and unit-testable.
type availableProvider struct {
	Provider  Provider
	Available bool
}

// selectFromPool picks a primary provider and an ordered fallback
// chain from a pool's providers, given the pool's strategy.
// Deterministic: priority strategy always picks the lowest-priority
// available provider (ties broken by provider ID, ascending, for
// reproducibility — not by weight, which StrategyWeighted uses
// instead); weighted strategy picks among the best-priority tier
// using a deterministic weighted index derived from a caller-supplied
// seed (e.g. the message ID), NOT math/rand, so route selection for a
// given message is reproducible across retries/tests.
func selectFromPool(providers []availableProvider, strategy SelectionStrategy, seed int64) ([]Provider, bool) {
	var candidates []Provider
	for _, ap := range providers {
		if ap.Available && ap.Provider.Active {
			candidates = append(candidates, ap.Provider)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})

	if strategy != StrategyWeighted {
		return candidates, true
	}

	// Weighted: reorder providers sharing the best (lowest) priority
	// tier by a deterministic weighted pick; providers at worse tiers
	// remain as fallbacks in priority order after the tier.
	bestPriority := candidates[0].Priority
	var tier []Provider
	var rest []Provider
	for _, c := range candidates {
		if c.Priority == bestPriority {
			tier = append(tier, c)
		} else {
			rest = append(rest, c)
		}
	}
	ordered := weightedOrder(tier, seed)
	return append(ordered, rest...), true
}

// weightedOrder returns tier reordered by a deterministic
// weighted-random permutation seeded by `seed` (typically derived
// from the message/queue-entry ID) — same message always resolves to
// the same primary choice on retry, but different messages spread
// across providers roughly proportional to weight.
func weightedOrder(tier []Provider, seed int64) []Provider {
	if len(tier) <= 1 {
		return tier
	}
	total := 0
	for _, p := range tier {
		w := p.Weight
		if w <= 0 {
			w = 1
		}
		total += w
	}
	// A small deterministic LCG driven by seed — no math/rand global
	// state, no time-based entropy, fully reproducible.
	state := uint64(seed)
	next := func(mod int) int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) % uint64(mod))
	}

	remaining := make([]Provider, len(tier))
	copy(remaining, tier)
	weights := make([]int, len(remaining))
	for i, p := range remaining {
		w := p.Weight
		if w <= 0 {
			w = 1
		}
		weights[i] = w
	}

	var out []Provider
	for len(remaining) > 0 {
		sum := 0
		for _, w := range weights {
			sum += w
		}
		pick := next(sum)
		acc := 0
		idx := 0
		for i, w := range weights {
			acc += w
			if pick < acc {
				idx = i
				break
			}
		}
		out = append(out, remaining[idx])
		remaining = append(remaining[:idx], remaining[idx+1:]...)
		weights = append(weights[:idx], weights[idx+1:]...)
	}
	return out
}
