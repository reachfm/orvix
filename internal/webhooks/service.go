package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/webhooks/ssrf"
)

const (
	maxSecretBytes    = 64
	maxSecretAttempts = 8
	maxBackoff        = 300 * time.Second
	maxAttempts       = 8
)

// Service is the webhook platform service.
type Service struct {
	repo      *Repository
	ssrfAllow *ssrf.Allowlist
}

func NewService(repo *Repository, ssrfAllow *ssrf.Allowlist) *Service {
	return &Service{repo: repo, ssrfAllow: ssrfAllow}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return s.repo.EnsureSchema(ctx)
}

// CreateSubscription registers a webhook subscription.
func (s *Service) CreateSubscription(ctx context.Context, tenantID uint, scope SubscriptionScope, url string, events []string, secret []byte) (*Subscription, error) {
	sub, _, err := s.CreateSubscriptionWithSecret(ctx, tenantID, scope, url, events, secret)
	return sub, err
}

// CreateSubscriptionWithSecret creates a subscription and returns the
// plaintext secret exactly once to the caller. Only the encrypted value is
// persisted and subsequent reads never expose plaintext.
func (s *Service) CreateSubscriptionWithSecret(ctx context.Context, tenantID uint, scope SubscriptionScope, url string, events []string, secret []byte) (*Subscription, string, error) {
	if err := ssrf.ValidateURL(url, s.ssrfAllow); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	// Validate events against allowlist
	for _, ev := range events {
		if !AllowedEvents[ev] {
			return nil, "", fmt.Errorf("%w: %s", ErrInvalidEvent, ev)
		}
	}
	if len(secret) == 0 {
		var err error
		secret, err = GenerateSecret()
		if err != nil {
			return nil, "", err
		}
	}
	encrypted, err := config.Encrypt(secret)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt webhook secret: %w", err)
	}
	sub := &Subscription{
		TenantID:        tenantID,
		Scope:           scope,
		URL:             url,
		Events:          events,
		Active:          true,
		SecretEncrypted: encrypted,
	}
	if err := s.repo.InsertSubscription(ctx, sub); err != nil {
		return nil, "", err
	}
	return sub, hex.EncodeToString(secret), nil
}

// GenerateSecret creates a new webhook signing secret.
func GenerateSecret() ([]byte, error) {
	b := make([]byte, maxSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Dispatch enqueues an event for delivery to all matching subscriptions.
func (s *Service) Dispatch(ctx context.Context, eventType string, scopeStr string, tenantID uint, payload []byte) (string, error) {
	if !AllowedEvents[eventType] {
		return "", fmt.Errorf("%w: %s", ErrInvalidEvent, eventType)
	}
	id, err := newEventID()
	if err != nil {
		return "", err
	}
	// Find matching subscriptions
	subs, err := s.repo.ListSubscriptions(ctx, 0, scopeStr, true)
	if err != nil {
		return "", err
	}
	for _, sub := range subs {
		if !sub.Active || sub.Suspended {
			continue
		}
		if !hasEvent(sub.Events, eventType) {
			continue
		}
		// Check tenant scope
		if sub.Scope == ScopeTenant && sub.TenantID != tenantID {
			continue
		}
		d := &Delivery{EventID: id, SubscriptionID: sub.ID, Status: "pending"}
		if derr := s.repo.InsertDelivery(ctx, d); derr != nil {
			return "", derr
		}
	}
	return id, nil
}

func hasEvent(events []string, target string) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}

// ProcessPendingDeliveries processes pending/failed deliveries.
func (s *Service) ProcessPendingDeliveries(ctx context.Context, batchSize int) error {
	deliveries, err := s.repo.PendingDeliveries(ctx, batchSize)
	if err != nil {
		return err
	}
	for i := range deliveries {
		d := &deliveries[i]
		sub, err := s.repo.GetSubscription(ctx, d.SubscriptionID)
		if err != nil {
			d.Status = "failed"
			d.RedactedError = "subscription not found"
			s.repo.UpdateDelivery(ctx, d)
			continue
		}
		if !sub.Active || sub.Suspended {
			d.Status = "failed"
			d.RedactedError = "subscription inactive"
			s.repo.UpdateDelivery(ctx, d)
			continue
		}
		secret, err := config.Decrypt(sub.SecretEncrypted)
		if err != nil {
			d.Status = "failed"
			d.RedactedError = "subscription secret unavailable"
			s.repo.UpdateDelivery(ctx, d)
			continue
		}
		payload := []byte(fmt.Sprintf(`{"event_id":"%s","type":"%s"}`, d.EventID, "domain.created"))
		ts := time.Now().Unix()
		sig := Sign(secret, payload, ts)
		hc := ssrf.SafeHTTPClient(30*time.Second, s.ssrfAllow)
		req, _ := http.NewRequest("POST", sub.URL, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Timestamp", fmt.Sprintf("%d", ts))
		req.Header.Set("X-Webhook-Signature", sig)
		resp, err := hc.Do(req)
		d.AttemptCount++
		if err != nil {
			d.Status = "failed"
			d.RedactedError = redactError(err)
			d.NextAttemptAt = ptr(time.Now().UTC().Add(backoffDuration(d.AttemptCount)))
			if d.AttemptCount >= maxAttempts {
				d.Status = "suspended"
				sub.Suspended = true
				s.repo.UpdateSubscription(ctx, sub)
			}
		} else {
			resp.Body.Close()
			d.HTTPStatus = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				d.Status = "delivered"
			} else {
				d.Status = "failed"
				d.RedactedError = fmt.Sprintf("http %d", resp.StatusCode)
				if d.AttemptCount >= maxAttempts {
					d.Status = "suspended"
					sub.Suspended = true
					s.repo.UpdateSubscription(ctx, sub)
				}
			}
		}
		s.repo.UpdateDelivery(ctx, d)
	}
	return nil
}

func backoffDuration(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

func ptr[T any](v T) *T { return &v }

func redactError(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func newEventID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "evt_" + hex.EncodeToString(b), nil
}

// ErrNotFound, ErrInvalidURL, ErrInvalidEvent are stable typed errors.
var (
	ErrNotFound     = &whError{"webhook subscription not found"}
	ErrInvalidURL   = &whError{"invalid webhook URL"}
	ErrInvalidEvent = &whError{"invalid webhook event type"}
)

// ListSubscriptions returns subscriptions matching the filters.
func (s *Service) ListSubscriptions(ctx context.Context, tenantID uint, scope string, onlyActive bool) ([]Subscription, error) {
	return s.repo.ListSubscriptions(ctx, tenantID, scope, onlyActive)
}

// GetSubscription returns one subscription by ID.
func (s *Service) GetSubscription(ctx context.Context, id uint) (*Subscription, error) {
	return s.repo.GetSubscription(ctx, id)
}

// RotateSecret replaces the signing secret and returns the new plaintext
// exactly once. The previous secret is not retained for future delivery.
func (s *Service) RotateSecret(ctx context.Context, id uint) (*Subscription, string, error) {
	sub, err := s.repo.GetSubscription(ctx, id)
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
	if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
		return nil, "", err
	}
	return sub, hex.EncodeToString(secret), nil
}

// Reactivate clears suspension after an operator explicitly confirms the
// endpoint is healthy again.
func (s *Service) Reactivate(ctx context.Context, id uint) (*Subscription, error) {
	sub, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	sub.Suspended = false
	sub.Active = true
	if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// DeliveryHistory returns delivery history for a subscription.
func (s *Service) DeliveryHistory(ctx context.Context, subscriptionID uint, limit int) ([]Delivery, error) {
	return s.repo.DeliveryHistory(ctx, subscriptionID, limit)
}

// RetryDelivery requeues a failed or suspended delivery without exposing
// its request body or secret material.
func (s *Service) RetryDelivery(ctx context.Context, id uint) error {
	d, err := s.repo.GetDelivery(ctx, id)
	if err != nil {
		return err
	}
	if d.Status != "failed" && d.Status != "suspended" {
		return fmt.Errorf("delivery is not retryable")
	}
	d.Status = "pending"
	d.NextAttemptAt = nil
	d.RedactedError = ""
	return s.repo.UpdateDelivery(ctx, d)
}
