package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/webhooks/ssrf"
)

const (
	maxSecretBytes       = 64
	maxBackoff           = 5 * time.Minute
	defaultMaxAttempts   = 8
	defaultSuspendAfter  = 8
	defaultLeaseDuration = 45 * time.Second
	defaultResponseLimit = 64 << 10
)

type Service struct {
	repo         *Repository
	ssrfAllow    *ssrf.Allowlist
	outbox       *kernel.OutboxRepository
	clock        kernel.Clock
	httpOptions  ssrf.ClientOptions
	maxAttempts  int
	suspendAfter int
	lease        time.Duration
	responseMax  int64
}

func NewService(repo *Repository, allow *ssrf.Allowlist) *Service {
	return &Service{repo: repo, ssrfAllow: allow, clock: kernel.SystemClock{}, maxAttempts: defaultMaxAttempts, suspendAfter: defaultSuspendAfter, lease: defaultLeaseDuration, responseMax: defaultResponseLimit}
}

func (s *Service) WithOutbox(outbox *kernel.OutboxRepository) *Service { s.outbox = outbox; return s }
func (s *Service) WithClock(clock kernel.Clock) *Service {
	if clock != nil {
		s.clock = clock
	}
	return s
}
func (s *Service) WithHTTPOptions(options ssrf.ClientOptions) *Service {
	s.httpOptions = options
	return s
}
func (s *Service) WithRetryPolicy(maxAttempts, suspendAfter int, lease time.Duration) *Service {
	if maxAttempts > 0 {
		s.maxAttempts = maxAttempts
	}
	if suspendAfter > 0 {
		s.suspendAfter = suspendAfter
	}
	if lease > 0 {
		s.lease = lease
	}
	return s
}

func (s *Service) EnsureSchema(ctx context.Context) error { return s.repo.EnsureSchema(ctx) }

func normalizeEvents(events []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if !AllowedEvents[event] {
			return nil, fmt.Errorf("%w: %s", ErrInvalidEvent, event)
		}
		if !seen[event] {
			seen[event] = true
			out = append(out, event)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one event is required", ErrInvalidEvent)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) CreateSubscription(ctx context.Context, tenantID uint, scope SubscriptionScope, url string, events []string, secret []byte) (*Subscription, error) {
	sub, _, err := s.CreateSubscriptionWithSecret(ctx, tenantID, scope, url, events, secret)
	return sub, err
}

func (s *Service) CreateSubscriptionWithSecret(ctx context.Context, tenantID uint, scope SubscriptionScope, url string, events []string, secret []byte) (*Subscription, string, error) {
	if tenantID == 0 || scope != ScopeTenant {
		return nil, "", ErrTenantRequired
	}
	if err := ssrf.ValidateURL(url, s.ssrfAllow); err != nil {
		return nil, "", fmt.Errorf("%w: destination rejected", ErrInvalidURL)
	}
	normalized, err := normalizeEvents(events)
	if err != nil {
		return nil, "", err
	}
	if len(secret) == 0 {
		secret, err = GenerateSecret()
		if err != nil {
			return nil, "", err
		}
	}
	encrypted, err := config.Encrypt(secret)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt webhook secret: %w", err)
	}
	sub := &Subscription{TenantID: tenantID, Scope: ScopeTenant, URL: url, Events: normalized, Active: true, SecretEncrypted: encrypted}
	if err := s.repo.InsertSubscription(ctx, sub); err != nil {
		return nil, "", err
	}
	return sub, hex.EncodeToString(secret), nil
}

func GenerateSecret() ([]byte, error) {
	b := make([]byte, maxSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Dispatch is retained for internal callers that already hold a complete
// event. Production business mutations use OutboxPublisher instead.
func (s *Service) Dispatch(ctx context.Context, eventType, scope string, tenantID uint, payload []byte) (string, error) {
	if scope != string(ScopeTenant) || tenantID == 0 {
		return "", ErrTenantRequired
	}
	var data any = json.RawMessage(payload)
	event, err := NewEvent(eventType, tenantID, data, s.clock.Now())
	if err != nil {
		return "", err
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = s.repo.InsertEventAndFanoutTx(ctx, tx, event); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return event.ID, nil
}

func hasEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}

func (s *Service) ProcessOutbox(ctx context.Context, batchSize int) error {
	if s.outbox == nil {
		return nil
	}
	now := s.clock.Now()
	items, err := s.outbox.ClaimTopicBatch(ctx, s.repo.db, OutboxTopic, batchSize, now)
	if err != nil {
		return err
	}
	for _, item := range items {
		var event Event
		if err := json.Unmarshal(item.Payload, &event); err != nil || event.ID == "" || event.TenantID == 0 || !AllowedEvents[event.Type] || event.SchemaVersion <= 0 {
			_ = s.outbox.MarkRetry(ctx, s.repo.db, item.ID, item.Attempts+1, s.maxAttempts, "invalid webhook event envelope", now.Add(backoffDuration(item.Attempts+1)), now)
			continue
		}
		tx, err := s.repo.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		_, err = s.repo.InsertEventAndFanoutTx(ctx, tx, event)
		if err == nil {
			err = s.outbox.MarkDone(ctx, tx, item.ID, now)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			_ = s.outbox.MarkRetry(ctx, s.repo.db, item.ID, item.Attempts+1, s.maxAttempts, "webhook event fanout failed", now.Add(backoffDuration(item.Attempts+1)), now)
		}
	}
	return nil
}

func (s *Service) ProcessPendingDeliveries(ctx context.Context, batchSize int) error {
	now := s.clock.Now()
	deliveries, err := s.repo.ClaimDeliveries(ctx, batchSize, now, s.lease)
	if err != nil {
		return err
	}
	var combined error
	for _, delivery := range deliveries {
		if err := s.deliver(ctx, delivery); err != nil {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}

func (s *Service) deliver(ctx context.Context, d Delivery) error {
	now := s.clock.Now()
	attemptID, err := s.repo.InsertAttempt(ctx, d, now)
	if err != nil {
		return err
	}
	event, err := s.repo.GetEvent(ctx, d.EventID)
	if err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "event unavailable", "", false)
	}
	sub, err := s.repo.GetSubscription(ctx, d.SubscriptionID)
	if err != nil || !sub.Active || sub.Suspended {
		return s.finishFailure(ctx, d, attemptID, 0, "subscription unavailable", "", false)
	}
	if err := ssrf.ValidateURLContext(ctx, sub.URL, s.ssrfAllow, s.httpOptions.Resolver); err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "destination rejected", "", false)
	}
	secret, err := config.Decrypt(sub.SecretEncrypted)
	if err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "signing secret unavailable", "", false)
	}
	body, err := json.Marshal(event)
	if err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "event encoding failed", "", false)
	}
	timestamp := now.Unix()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "request creation failed", "", false)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Orvix-Webhooks/1.0")
	request.Header.Set("X-Orvix-Event-ID", event.ID)
	request.Header.Set("X-Orvix-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Orvix-Signature", Sign(secret, body, timestamp))
	options := s.httpOptions
	options.Allowlist = s.ssrfAllow
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	client := ssrf.SafeHTTPClientWithOptions(options)
	response, err := client.Do(request)
	if err != nil {
		return s.finishFailure(ctx, d, attemptID, 0, "delivery transport failed", "", true)
	}
	defer response.Body.Close()
	limited, readErr := io.ReadAll(io.LimitReader(response.Body, s.responseMax+1))
	if readErr != nil {
		return s.finishFailure(ctx, d, attemptID, response.StatusCode, "response read failed", "", true)
	}
	if int64(len(limited)) > s.responseMax {
		return s.finishFailure(ctx, d, attemptID, response.StatusCode, "response body exceeded limit", "", true)
	}
	excerpt := safeExcerpt(limited)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := s.repo.CompleteAttempt(ctx, attemptID, "delivered", response.StatusCode, "", excerpt, now); err != nil {
			return err
		}
		if err := s.repo.CompleteDelivery(ctx, d, "delivered", response.StatusCode, "", excerpt, nil, now); err != nil {
			return err
		}
		return s.repo.ResetFailures(ctx, sub)
	}
	retryable := response.StatusCode == 408 || response.StatusCode == 425 || response.StatusCode == 429 || response.StatusCode >= 500
	return s.finishFailure(ctx, d, attemptID, response.StatusCode, fmt.Sprintf("http %d", response.StatusCode), excerpt, retryable)
}

func (s *Service) finishFailure(ctx context.Context, d Delivery, attemptID uint, httpStatus int, safeErr, excerpt string, retryable bool) error {
	now := s.clock.Now()
	attempt := d.AttemptCount + 1
	terminal := !retryable || attempt >= s.maxAttempts
	status := "retrying"
	var next *time.Time
	if terminal {
		status = "terminal"
	} else {
		at := now.Add(backoffDuration(attempt))
		next = &at
	}
	if err := s.repo.CompleteAttempt(ctx, attemptID, status, httpStatus, safeErr, excerpt, now); err != nil {
		return err
	}
	if err := s.repo.CompleteDelivery(ctx, d, status, httpStatus, safeErr, excerpt, next, now); err != nil {
		return err
	}
	sub, err := s.repo.GetSubscription(ctx, d.SubscriptionID)
	if err == nil {
		if err = s.repo.RecordFailure(ctx, sub, s.suspendAfter); err != nil {
			return err
		}
		if sub.Suspended && status != "delivered" {
			_, _ = s.repo.db.ExecContext(ctx, s.repo.q(`UPDATE webhook_deliveries SET status='terminal',next_attempt_at=NULL WHERE id=? AND status='retrying'`), d.ID)
		}
	}
	return nil
}

func backoffDuration(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<uint(min(attempt-1, 20))) * time.Second
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}
func safeExcerpt(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return fmt.Sprintf("response body: %d bytes", len(body))
}

var (
	ErrNotFound       = &whError{"webhook resource not found"}
	ErrInvalidURL     = &whError{"invalid webhook URL"}
	ErrInvalidEvent   = &whError{"invalid webhook event type"}
	ErrInvalidStatus  = &whError{"invalid webhook delivery status"}
	ErrTenantRequired = &whError{"tenant webhook scope is required"}
)

func (s *Service) ListSubscriptions(ctx context.Context, tenantID uint, scope string, onlyActive bool) ([]Subscription, error) {
	return s.repo.ListSubscriptions(ctx, tenantID, scope, onlyActive)
}
func (s *Service) GetSubscription(ctx context.Context, id uint) (*Subscription, error) {
	return s.repo.GetSubscription(ctx, id)
}
func (s *Service) GetSubscriptionForTenant(ctx context.Context, id, tenantID uint) (*Subscription, error) {
	return s.repo.GetSubscriptionForTenant(ctx, id, tenantID)
}

func (s *Service) UpdateSubscription(ctx context.Context, id, tenantID uint, url string, events []string, active *bool, version int) (*Subscription, error) {
	sub, err := s.repo.GetSubscriptionForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if version > 0 && version != sub.Version {
		return nil, errStale
	}
	if url != "" {
		if err := ssrf.ValidateURL(url, s.ssrfAllow); err != nil {
			return nil, ErrInvalidURL
		}
		sub.URL = url
	}
	if events != nil {
		sub.Events, err = normalizeEvents(events)
		if err != nil {
			return nil, err
		}
	}
	if active != nil {
		sub.Active = *active
	}
	if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}
func (s *Service) Disable(ctx context.Context, id, tenantID uint) (*Subscription, error) {
	active := false
	return s.UpdateSubscription(ctx, id, tenantID, "", nil, &active, 0)
}
func (s *Service) Delete(ctx context.Context, id, tenantID uint) error {
	return s.repo.DeleteSubscription(ctx, id, tenantID)
}

func (s *Service) RotateSecret(ctx context.Context, id uint) (*Subscription, string, error) {
	return s.RotateSecretForTenant(ctx, id, 0)
}
func (s *Service) RotateSecretForTenant(ctx context.Context, id, tenantID uint) (*Subscription, string, error) {
	sub, err := s.repo.GetSubscriptionForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, "", err
	}
	secret, err := GenerateSecret()
	if err != nil {
		return nil, "", err
	}
	sub.SecretEncrypted, err = config.Encrypt(secret)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt webhook secret: %w", err)
	}
	if err = s.repo.UpdateSubscription(ctx, sub); err != nil {
		return nil, "", err
	}
	return sub, hex.EncodeToString(secret), nil
}
func (s *Service) Reactivate(ctx context.Context, id uint) (*Subscription, error) {
	return s.ReactivateForTenant(ctx, id, 0)
}
func (s *Service) ReactivateForTenant(ctx context.Context, id, tenantID uint) (*Subscription, error) {
	sub, err := s.repo.GetSubscriptionForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	sub.Suspended = false
	sub.Active = true
	sub.FailureCount = 0
	if err = s.repo.UpdateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}
func (s *Service) DeliveryHistory(ctx context.Context, subscriptionID uint, limit int) ([]Delivery, error) {
	return s.repo.DeliveryHistory(ctx, subscriptionID, limit)
}
func (s *Service) DeliveryHistoryForTenant(ctx context.Context, subscriptionID, tenantID uint, limit, offset int) ([]Delivery, error) {
	if _, err := s.repo.GetSubscriptionForTenant(ctx, subscriptionID, tenantID); err != nil {
		return nil, err
	}
	return s.repo.DeliveryHistoryForTenant(ctx, subscriptionID, tenantID, limit, offset)
}
func (s *Service) DeliveryHistoryFiltered(ctx context.Context, subscriptionID, tenantID uint, status string, limit, offset int) ([]Delivery, error) {
	if _, err := s.repo.GetSubscriptionForTenant(ctx, subscriptionID, tenantID); err != nil {
		return nil, err
	}
	switch status {
	case "", "pending", "processing", "retrying", "delivered", "terminal", "failed", "suspended":
	default:
		return nil, ErrInvalidStatus
	}
	return s.repo.DeliveryHistoryFiltered(ctx, subscriptionID, tenantID, status, limit, offset)
}
func (s *Service) DeliveryForTenant(ctx context.Context, id, tenantID uint) (*Delivery, []Attempt, error) {
	delivery, err := s.repo.GetDeliveryForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, nil, err
	}
	attempts, err := s.repo.Attempts(ctx, id)
	return delivery, attempts, err
}
func (s *Service) ReplayForTenant(ctx context.Context, id, tenantID uint) (*Delivery, error) {
	delivery, err := s.repo.GetDeliveryForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if delivery.Status != "terminal" && delivery.Status != "failed" && delivery.Status != "suspended" {
		return nil, fmt.Errorf("delivery is not replayable")
	}
	return s.repo.CreateManualReplay(ctx, *delivery)
}
func (s *Service) RetryDelivery(ctx context.Context, id uint) error {
	delivery, err := s.repo.GetDelivery(ctx, id)
	if err != nil {
		return err
	}
	if delivery.Status != "failed" && delivery.Status != "terminal" && delivery.Status != "suspended" {
		return fmt.Errorf("delivery is not retryable")
	}
	_, err = s.repo.CreateManualReplay(ctx, *delivery)
	return err
}
