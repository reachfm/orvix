package billing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrWebhookAlreadyProcessed = errors.New("webhook event already processed")
	ErrWebhookSignatureInvalid = errors.New("webhook signature is invalid")
	ErrWebhookTimestampExpired = errors.New("webhook timestamp is outside tolerance window")
)

type WebhookEventRecord struct {
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	EventType       string     `json:"event_type"`
	ProviderSubID   string     `json:"provider_sub_id"`
	RawPayload      []byte     `json:"-"`
	Signature       string     `json:"-"`
	ReceivedAt      time.Time  `json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	ProcessingError string     `json:"processing_error,omitempty"`
	IdempotencyKey  string     `json:"idempotency_key"`
	CreatedAt       time.Time  `json:"created_at"`
}

type WebhookService struct {
	db *sql.DB
}

func NewWebhookService(db *sql.DB) *WebhookService {
	return &WebhookService{db: db}
}

func (s *WebhookService) VerifySignature(payload []byte, signature, secret string, timestamp int64, tolerance time.Duration) (string, error) {
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if time.Duration(diff)*time.Second > tolerance {
		return "", ErrWebhookTimestampExpired
	}

	signedPayload := fmt.Sprintf("%d.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return "", ErrWebhookSignatureInvalid
	}

	return hex.EncodeToString(mac.Sum(nil))[:32], nil
}

func (s *WebhookService) RecordEvent(ctx context.Context, rec *WebhookEventRecord) error {
	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	if rec.IdempotencyKey == "" {
		b := make([]byte, 16)
		rand.Read(b)
		rec.IdempotencyKey = hex.EncodeToString(b)
	}
	rec.ProcessedAt = nil
	rec.ProcessingError = ""

	var existing string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM webhook_events WHERE idempotency_key = ?", rec.IdempotencyKey).Scan(&existing)
	if err == nil {
		return ErrWebhookAlreadyProcessed
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO webhook_events (id, provider, event_type, provider_sub_id, raw_payload, signature,
		received_at, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Provider, rec.EventType, rec.ProviderSubID, rec.RawPayload, rec.Signature,
		rec.ReceivedAt, rec.IdempotencyKey, time.Now().UTC())
	return err
}

func (s *WebhookService) MarkProcessed(ctx context.Context, eventID string, processingErr error) error {
	var errStr string
	if processingErr != nil {
		errStr = processingErr.Error()
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE webhook_events SET processed_at = ?, processing_error = ? WHERE id = ?",
		time.Now().UTC(), errStr, eventID)
	return err
}
