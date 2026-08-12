package deliverability

import (
	"context"
	"fmt"

	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/queue"
)

// DeliveryAdapter implements both
// internal/coremail/delivery.SuppressionChecker and
// .DeliverabilityRecorder, wiring this control plane into the real
// outbound path.
type DeliveryAdapter struct {
	svc *Service
}

func NewDeliveryAdapter(svc *Service) *DeliveryAdapter { return &DeliveryAdapter{svc: svc} }

var (
	_ delivery.SuppressionChecker     = (*DeliveryAdapter)(nil)
	_ delivery.DeliverabilityRecorder = (*DeliveryAdapter)(nil)
)

func (a *DeliveryAdapter) IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error) {
	return a.svc.IsSuppressed(ctx, tenantID, address)
}

func (a *DeliveryAdapter) RecordOutcome(ctx context.Context, entry *queue.QueueEntry, tenantID uint, relayProviderName string, result *delivery.DeliveryResult, attemptNumber int) {
	eventKey := fmt.Sprintf("attempt:%d:%d", entry.ID, attemptNumber)
	sendingDomain := domainOf(entry.FromAddress)
	_ = a.svc.RecordFromBounce(ctx, eventKey, tenantID, sendingDomain, entry.ToAddress, relayProviderName, result, attemptNumber)
}
