package webhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type EventID string

type SubscriptionScope string

const (
	ScopeTenant   SubscriptionScope = "tenant"
	ScopePlatform SubscriptionScope = "platform"
)

type Subscription struct {
	ID              uint              `json:"id"`
	TenantID        uint              `json:"tenant_id,omitempty"`
	Scope           SubscriptionScope `json:"scope"`
	URL             string            `json:"url"`
	Events          []string          `json:"events"`
	SecretEncrypted string            `json:"-"`
	Active          bool              `json:"active"`
	Suspended       bool              `json:"suspended"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Version         int               `json:"-"`
}

type Delivery struct {
	ID             uint       `json:"id"`
	EventID        string     `json:"event_id"`
	SubscriptionID uint       `json:"subscription_id"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	HTTPStatus     int        `json:"http_status,omitempty"`
	RedactedError  string     `json:"error,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Event struct {
	ID            string          `json:"id"`
	TenantID      uint            `json:"tenant_id"`
	Type          string          `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

func NewEvent(eventType string, tenantID uint, payload any, occurredAt time.Time) (Event, error) {
	if !AllowedEvents[eventType] {
		return Event{}, fmt.Errorf("%w: %s", ErrInvalidEvent, eventType)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode webhook event payload: %w", err)
	}
	id, err := newEventID()
	if err != nil {
		return Event{}, err
	}
	return Event{ID: id, TenantID: tenantID, Type: eventType, SchemaVersion: 1, OccurredAt: occurredAt.UTC(), Payload: body}, nil
}

func newEventID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook event id: %w", err)
	}
	return "evt_" + hex.EncodeToString(b), nil
}

var AllowedEvents = map[string]bool{
	"domain.created":    true,
	"domain.updated":    true,
	"domain.deleted":    true,
	"mailbox.created":   true,
	"mailbox.updated":   true,
	"mailbox.deleted":   true,
	"user.created":      true,
	"user.updated":      true,
	"incident.created":  true,
	"incident.resolved": true,
	"update.available":  true,
}

func Sign(secret, body []byte, ts int64) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%d.", ts)))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func Verify(secret, body []byte, ts int64, signature string, window time.Duration) bool {
	expected := Sign(secret, body, ts)
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return false
	}
	eventTime := time.Unix(ts, 0)
	if time.Since(eventTime) > window || time.Since(eventTime) < -window {
		return false
	}
	return true
}
