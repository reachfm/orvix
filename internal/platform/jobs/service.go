package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

const maxPayloadBytes = 64 << 10

type Service struct {
	repo     *Repository
	registry *Registry
	clock    kernel.Clock
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, registry: NewRegistry(), clock: kernel.SystemClock{}}
}

func NewServiceWithRegistry(repo *Repository, registry *Registry, clock kernel.Clock) *Service {
	if registry == nil {
		registry = NewRegistry()
	}
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, registry: registry, clock: clock}
}

func (s *Service) EnsureSchema(ctx context.Context) error { return s.repo.EnsureSchema(ctx) }

func (s *Service) Submit(ctx context.Context, submission Submission) (*Job, bool, error) {
	definition, ok := s.registry.Lookup(strings.TrimSpace(submission.Type))
	if !ok {
		return nil, false, ErrUnknownJobType
	}
	if submission.Scope != definition.Scope {
		return nil, false, kernel.ValidationError(map[string]string{"scope": "job type is not allowed in this scope"})
	}
	if submission.Scope == ScopeTenant && submission.TenantID == 0 {
		return nil, false, kernel.ValidationError(map[string]string{"tenant_id": "tenant context is required"})
	}
	if submission.Scope == ScopePlatform && submission.TenantID != 0 {
		return nil, false, kernel.ValidationError(map[string]string{"tenant_id": "platform jobs cannot carry a tenant"})
	}
	if strings.TrimSpace(submission.Actor) == "" || strings.TrimSpace(submission.IdempotencyKey) == "" {
		return nil, false, kernel.ValidationError(map[string]string{"idempotency_key": "Idempotency-Key is required", "actor": "authenticated actor is required"})
	}
	payload, err := normalizeSafeJSON(submission.Payload)
	if err != nil {
		return nil, false, err
	}
	if err = definition.Validate(payload); err != nil {
		return nil, false, kernel.ValidationError(map[string]string{"payload": "payload failed validation"})
	}
	submission.Type = definition.Type
	submission.PayloadVersion = definition.PayloadVersion
	submission.Payload = payload
	requestHash := submissionHash(submission)
	idempotencyScope := fmt.Sprintf("%s|%d|%s|%s", submission.Scope, submission.TenantID, strings.TrimSpace(submission.Actor), definition.Type)
	return s.repo.SubmitIdempotent(ctx, submission, requestHash, idempotencyScope, s.clock.Now())
}

func normalizeSafeJSON(payload []byte) (json.RawMessage, error) {
	if len(payload) == 0 || len(payload) > maxPayloadBytes {
		return nil, kernel.ValidationError(map[string]string{"payload": "payload must be non-empty and at most 64 KiB"})
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, kernel.ValidationError(map[string]string{"payload": "payload must be valid JSON"})
	}
	if containsSensitiveField(value) {
		return nil, kernel.ValidationError(map[string]string{"payload": "credentials and secrets are not permitted"})
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "normalize automation job payload", err)
	}
	return normalized, nil
}

func containsSensitiveField(value any) bool {
	sensitive := map[string]bool{"authorization": true, "password": true, "secret": true, "token": true, "api_key": true, "apikey": true, "cookie": true, "private_key": true}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if sensitive[normalized] || containsSensitiveField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveField(child) {
				return true
			}
		}
	}
	return false
}

func submissionHash(submission Submission) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%d\x00%s", submission.Type, submission.PayloadVersion, submission.Payload)
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Service) Get(ctx context.Context, id, tenantID uint, scope Scope) (*Job, error) {
	return s.repo.GetForScope(ctx, id, tenantID, scope)
}

func (s *Service) List(ctx context.Context, filter ListFilter) (kernel.PageResponse[Job], error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return kernel.PageResponse[Job]{}, kernel.ValidationError(map[string]string{"status": "invalid job status"})
	}
	if filter.Scope == ScopeTenant && filter.TenantID == 0 {
		return kernel.PageResponse[Job]{}, kernel.ValidationError(map[string]string{"tenant_id": "tenant context is required"})
	}
	return s.repo.List(ctx, filter)
}

func (s *Service) Claim(ctx context.Context, owner string, leaseDuration time.Duration) (*Job, error) {
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "generate automation job lease token", err)
	}
	return s.repo.ClaimOne(ctx, owner, hex.EncodeToString(tokenBytes), s.clock.Now(), leaseDuration)
}

func leaseFor(job *Job) Lease {
	return Lease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseVersion: job.LeaseVersion}
}

func (s *Service) Heartbeat(ctx context.Context, lease Lease, extension time.Duration) error {
	return s.repo.Heartbeat(ctx, lease, s.clock.Now(), extension)
}

func (s *Service) UpdateProgress(ctx context.Context, lease Lease, progress int) error {
	if progress < 0 || progress > 100 {
		return kernel.ValidationError(map[string]string{"progress": "must be between 0 and 100"})
	}
	return s.repo.UpdateProgress(ctx, lease, progress, s.clock.Now())
}

func (s *Service) Complete(ctx context.Context, lease Lease, result json.RawMessage) error {
	normalized, err := normalizeSafeResult(result)
	if err != nil {
		return err
	}
	return s.repo.Complete(ctx, lease, normalized, s.clock.Now())
}

func normalizeSafeResult(result []byte) (json.RawMessage, error) {
	if len(result) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return normalizeSafeJSON(result)
}

func (s *Service) Fail(ctx context.Context, lease Lease, code, message string, retryable bool) error {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "JOB_EXECUTION_FAILED"
	}
	message = safeErrorMessage(message)
	if message == "" {
		message = "automation job execution failed"
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return s.repo.Fail(ctx, lease, code, message, retryable, s.clock.Now())
}

func safeErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "automation job execution failed"
	}
	lower := strings.ToLower(message)
	if kernel.IsSecretField(message) || strings.Contains(lower, "postgres://") || strings.Contains(lower, "sqlite:") {
		return "automation job execution failed"
	}
	return message
}

func (s *Service) RequestCancellation(ctx context.Context, id, tenantID uint, scope Scope) (*Job, error) {
	return s.repo.RequestCancellation(ctx, id, tenantID, scope, s.clock.Now())
}

func (s *Service) CancellationRequested(ctx context.Context, lease Lease) (bool, error) {
	return s.repo.CancellationRequested(ctx, lease)
}

func (s *Service) FinishCancellation(ctx context.Context, lease Lease) error {
	return s.repo.FinishCancellation(ctx, lease, s.clock.Now())
}

func (s *Service) RecoverExpired(ctx context.Context, limit int) (int, error) {
	return s.repo.RecoverExpired(ctx, s.clock.Now(), limit)
}

func (s *Service) ManualRetry(ctx context.Context, id, tenantID uint, scope Scope, idempotencyKey string) (*Job, bool, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, false, kernel.ValidationError(map[string]string{"idempotency_key": "Idempotency-Key is required"})
	}
	return s.repo.ManualRetry(ctx, id, tenantID, scope, strings.TrimSpace(idempotencyKey), s.clock.Now())
}
