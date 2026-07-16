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
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

var (
	ErrWebhookAlreadyProcessed = errors.New("webhook event already processed")
	ErrWebhookSignatureInvalid = errors.New("webhook signature is invalid")
)

func NormalizeProvider(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

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
	db      *sql.DB
	dialect *dbdialect.Info
}

type billingDBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewWebhookService(db *sql.DB) *WebhookService {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &WebhookService{db: db, dialect: dialect}
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
	return s.recordEvent(ctx, s.db, rec)
}

func (s *WebhookService) recordEvent(ctx context.Context, exec billingDBTX, rec *WebhookEventRecord) error {
	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	rec.Provider = NormalizeProvider(rec.Provider)
	if rec.Provider == "" {
		return fmt.Errorf("webhook provider is required")
	}
	if rec.IdempotencyKey == "" {
		b := make([]byte, 16)
		rand.Read(b)
		rec.IdempotencyKey = hex.EncodeToString(b)
	} else {
		rec.IdempotencyKey = NormalizeProvider(rec.IdempotencyKey)
	}
	rec.ProcessedAt = nil
	rec.ProcessingError = ""

	var result sql.Result
	var err error
	if s.dialect.IsPostgres() {
		result, err = exec.ExecContext(ctx,
			`INSERT INTO webhook_events (id, provider, event_type, provider_sub_id, raw_payload, signature,
			received_at, idempotency_key, created_at)
			VALUES (`+s.dialect.Placeholders(9)+`) ON CONFLICT (provider, id) DO NOTHING`,
			rec.ID, rec.Provider, rec.EventType, rec.ProviderSubID, rec.RawPayload, rec.Signature,
			rec.ReceivedAt, rec.IdempotencyKey, time.Now().UTC())
	} else {
		result, err = exec.ExecContext(ctx,
			`INSERT OR IGNORE INTO webhook_events (id, provider, event_type, provider_sub_id, raw_payload, signature,
			received_at, idempotency_key, created_at)
			VALUES (`+s.dialect.Placeholders(9)+`)`,
			rec.ID, rec.Provider, rec.EventType, rec.ProviderSubID, rec.RawPayload, rec.Signature,
			rec.ReceivedAt, rec.IdempotencyKey, time.Now().UTC())
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrWebhookAlreadyProcessed
	}
	return nil
}

// ProcessEvent records a verified provider event and applies its database
// mutation in one transaction. A failed mutation rolls back the event claim,
// so the provider can safely retry the same event ID. Concurrent duplicates
// serialize on the provider-scoped unique key and observe the committed event.
func (s *WebhookService) ProcessEvent(ctx context.Context, rec *WebhookEventRecord, process func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin webhook transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.recordEvent(ctx, tx, rec); err != nil {
		return err
	}
	if process != nil {
		if err := process(tx); err != nil {
			return fmt.Errorf("process webhook event: %w", err)
		}
	}
	if err := s.markProcessed(ctx, tx, rec.ID, rec.Provider, nil); err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit webhook transaction: %w", err)
	}
	return nil
}

func (s *WebhookService) MarkProcessed(ctx context.Context, eventID, provider string, processingErr error) error {
	return s.markProcessed(ctx, s.db, eventID, provider, processingErr)
}

func (s *WebhookService) markProcessed(ctx context.Context, exec billingDBTX, eventID, provider string, processingErr error) error {
	provider = NormalizeProvider(provider)
	var errStr string
	if processingErr != nil {
		errStr = processingErr.Error()
	}
	_, err := exec.ExecContext(ctx,
		"UPDATE webhook_events SET processed_at = "+s.dialect.Placeholder(1)+", processing_error = "+s.dialect.Placeholder(2)+" WHERE id = "+s.dialect.Placeholder(3)+" AND provider = "+s.dialect.Placeholder(4),
		time.Now().UTC(), errStr, eventID, provider)
	return err
}
