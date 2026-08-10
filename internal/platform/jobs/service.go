package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

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
