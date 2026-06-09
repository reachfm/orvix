package delivery

import (
	"fmt"
	"math/rand"
	"time"
)

// RetryPolicy defines the backoff strategy for delivery retries.
type RetryPolicy struct {
	// BaseDelay is the delay for the first retry attempt.
	BaseDelay time.Duration
	// Multiplier is the exponential growth factor (e.g. 5x means 1min → 5min → 25min).
	Multiplier float64
	// MaxDelay caps the maximum delay between retries.
	MaxDelay time.Duration
	// MaxAttempts is the total number of delivery attempts before dead letter.
	MaxAttempts int
	// JitterPercent is the percentage (0.0–1.0) of delay to randomize.
	JitterPercent float64
}

// DefaultRetryPolicy returns a safe enterprise retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		BaseDelay:     1 * time.Minute,
		Multiplier:    5.0,
		MaxDelay:      24 * time.Hour,
		MaxAttempts:   16,
		JitterPercent: 0.1,
	}
}

// FastRetryPolicy returns a conservative policy for testing.
func FastRetryPolicy() RetryPolicy {
	return RetryPolicy{
		BaseDelay:     10 * time.Millisecond,
		Multiplier:    2.0,
		MaxDelay:      1 * time.Second,
		MaxAttempts:   5,
		JitterPercent: 0.05,
	}
}

// RetrySchedule returns the delay before the next retry attempt.
// The schedule follows: BaseDelay * Multiplier^(attempt-1), capped at MaxDelay.
func (p RetryPolicy) RetrySchedule(attemptCount int) (time.Duration, error) {
	if attemptCount < 1 {
		return 0, fmt.Errorf("retry attempt count must be >= 1, got %d", attemptCount)
	}
	if p.MaxAttempts > 0 && attemptCount > p.MaxAttempts {
		return 0, fmt.Errorf("retry limit exceeded: %d > %d", attemptCount, p.MaxAttempts)
	}

	// Calculate base delay: BaseDelay * Multiplier^(attempt-1)
	base := float64(p.BaseDelay)
	factor := 1.0
	for i := 1; i < attemptCount; i++ {
		factor *= p.Multiplier
	}
	delay := time.Duration(base * factor)

	// Cap at MaxDelay.
	if p.MaxDelay > 0 && delay > p.MaxDelay {
		delay = p.MaxDelay
	}

	// Add jitter.
	if p.JitterPercent > 0 {
		jitterRange := int64(float64(delay) * p.JitterPercent)
		if jitterRange > 0 {
			jitter := rand.Int63n(jitterRange*2 + 1) - jitterRange
			delay += time.Duration(jitter)
			if delay < 0 {
				delay = 0
			}
		}
	}

	return delay, nil
}

// NextAttemptAt calculates the time for the next retry attempt.
func (p RetryPolicy) NextAttemptAt(attemptCount int, now time.Time) (time.Time, error) {
	delay, err := p.RetrySchedule(attemptCount)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(delay), nil
}

// RetryDecision determines what should happen after a delivery attempt.
type RetryDecision int

const (
	DecisionDelivered   RetryDecision = iota // Success — remove from queue
	DecisionRetry                            // Temporary failure — schedule retry
	DecisionDeadLetter                       // Permanent failure or max attempts — move to DLQ
)

// ClassifyResult inspects a DeliveryResult and the current attempt count to
// determine the next action, using the retry policy.
func (p RetryPolicy) ClassifyResult(res *DeliveryResult, currentAttempt int) (RetryDecision, time.Time, error) {
	if res.Success {
		return DecisionDelivered, time.Time{}, nil
	}

	// Permanent failure (5xx non-retryable) or max attempts exceeded.
	if !res.TempFail || currentAttempt >= p.MaxAttempts {
		return DecisionDeadLetter, time.Time{}, nil
	}

	// Temporary failure — schedule retry.
	nextAttempt, err := p.NextAttemptAt(currentAttempt+1, time.Now().UTC())
	if err != nil {
		return DecisionDeadLetter, time.Time{}, fmt.Errorf("schedule retry: %w", err)
	}

	return DecisionRetry, nextAttempt, nil
}
