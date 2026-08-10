package relay

import "time"

// CircuitBreaker is pure decision logic over a Provider's persisted
// circuit fields — it never touches the database itself; the service
// layer reads the fields, calls these functions, and persists the
// result. This keeps the breaker's state machine trivially unit
// testable without a DB.
type CircuitBreaker struct {
	FailureThreshold int           // consecutive failures before opening
	CooldownPeriod   time.Duration // how long the circuit stays open before trying half-open
}

func NewCircuitBreaker(failureThreshold int, cooldown time.Duration) CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	return CircuitBreaker{FailureThreshold: failureThreshold, CooldownPeriod: cooldown}
}

// IsAvailable reports whether a provider in the given state may be
// selected right now. A half-open circuit IS available — that's the
// one-trial-request the half-open state exists to allow — the caller
// selecting it is what constitutes the trial.
func (cb CircuitBreaker) IsAvailable(state CircuitState, openedAt *time.Time, now time.Time) (CircuitState, bool) {
	switch state {
	case CircuitClosed:
		return CircuitClosed, true
	case CircuitOpen:
		if openedAt != nil && now.Sub(*openedAt) >= cb.CooldownPeriod {
			return CircuitHalfOpen, true
		}
		return CircuitOpen, false
	case CircuitHalfOpen:
		return CircuitHalfOpen, true
	default:
		return CircuitClosed, true
	}
}

// OnSuccess returns the next state after a successful delivery
// attempt: closed with the failure counter reset, from any prior
// state (a half-open trial succeeding is exactly what recovers the
// circuit).
func (cb CircuitBreaker) OnSuccess() (state CircuitState, failures int) {
	return CircuitClosed, 0
}

// OnFailure returns the next state after a failed delivery attempt.
// From half-open, a single failure re-opens immediately (no
// forgiveness — the trial failed). From closed, failures accumulate
// until FailureThreshold is reached.
func (cb CircuitBreaker) OnFailure(state CircuitState, currentFailures int, now time.Time) (nextState CircuitState, failures int, openedAt *time.Time) {
	if state == CircuitHalfOpen {
		return CircuitOpen, currentFailures + 1, &now
	}
	failures = currentFailures + 1
	if failures >= cb.FailureThreshold {
		return CircuitOpen, failures, &now
	}
	return CircuitClosed, failures, nil
}
