package webhooks

import (
	"context"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

const OutboxTopic = "webhook.event"

// OutboxPublisher writes immutable webhook events through the shared kernel
// outbox. The caller supplies the same transaction used by its business
// mutation, so neither side can commit independently.
type OutboxPublisher struct {
	outbox *kernel.OutboxRepository
}

func NewOutboxPublisher(outbox *kernel.OutboxRepository) *OutboxPublisher {
	return &OutboxPublisher{outbox: outbox}
}

func (p *OutboxPublisher) Publish(ctx context.Context, q kernel.Querier, eventType, aggregateID string, tenantID uint, payload any, occurredAt time.Time) (string, error) {
	event, err := NewEvent(eventType, tenantID, payload, occurredAt)
	if err != nil {
		return "", err
	}
	if err := p.outbox.Enqueue(ctx, q, OutboxTopic, event.ID, event, event.OccurredAt); err != nil {
		return "", err
	}
	return event.ID, nil
}
