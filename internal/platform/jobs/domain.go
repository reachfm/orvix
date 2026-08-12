package jobs

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Scope string

const (
	ScopeTenant   Scope = "tenant"
	ScopePlatform Scope = "platform"
)

var allowedTransitions = map[Status]map[Status]bool{
	StatusQueued:  {StatusRunning: true, StatusCancelled: true},
	StatusRunning: {StatusQueued: true, StatusSucceeded: true, StatusFailed: true, StatusCancelled: true},
	StatusFailed:  {StatusQueued: true},
}

func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func (s Status) CanTransition(to Status) bool { return allowedTransitions[s][to] }

type Job struct {
	ID                  uint            `json:"id"`
	TenantID            uint            `json:"tenant_id,omitempty"`
	Scope               Scope           `json:"scope"`
	Actor               string          `json:"actor"`
	Type                string          `json:"type"`
	PayloadVersion      int             `json:"payload_version"`
	Payload             json.RawMessage `json:"-"`
	Status              Status          `json:"status"`
	Progress            int             `json:"progress"`
	Attempt             int             `json:"attempt_count"`
	MaxAttempts         int             `json:"max_attempts"`
	RunAfter            time.Time       `json:"run_after"`
	LeaseOwner          string          `json:"-"`
	LeaseToken          string          `json:"-"`
	LeaseVersion        int             `json:"-"`
	LeaseExpiresAt      *time.Time      `json:"-"`
	HeartbeatAt         *time.Time      `json:"-"`
	CancellationAskedAt *time.Time      `json:"cancellation_requested_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           *time.Time      `json:"started_at,omitempty"`
	CompletedAt         *time.Time      `json:"completed_at,omitempty"`
	CancelledAt         *time.Time      `json:"cancelled_at,omitempty"`
	Result              json.RawMessage `json:"result,omitempty"`
	ErrorCode           string          `json:"error_code,omitempty"`
	ErrorMessage        string          `json:"error_message,omitempty"`
	IdempotencyKey      string          `json:"-"`
	IdempotencyScope    string          `json:"-"`
	RequestHash         string          `json:"-"`
	ManualRetryKey      string          `json:"-"`
	CorrelationID       string          `json:"correlation_id,omitempty"`
	Version             int             `json:"version"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type Submission struct {
	TenantID       uint
	Scope          Scope
	Actor          string
	Type           string
	PayloadVersion int
	Payload        json.RawMessage
	IdempotencyKey string
	CorrelationID  string
	MaxAttempts    int
	RunAfter       time.Time
}

type ListFilter struct {
	TenantID uint
	Scope    Scope
	Status   Status
	Type     string
	Page     kernel.PageRequest
}

type Lease struct {
	JobID        uint
	Owner        string
	Token        string
	LeaseVersion int
}

var (
	ErrNotFound          = kernel.NotFound("automation job")
	ErrInvalidTransition = kernel.NewError(kernel.ErrCodeStateTransition, "invalid automation job state transition")
	ErrLeaseLost         = kernel.Conflict("automation job lease is no longer owned by this worker")
	ErrCancellationAsked = kernel.Conflict("automation job cancellation was requested")
	ErrIdempotencyReuse  = kernel.NewError(kernel.ErrCodeIdempotencyReuse, "idempotency key was reused with a different automation job request")
	ErrUnknownJobType    = kernel.ValidationError(map[string]string{"type": "unsupported automation job type"})
)

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
