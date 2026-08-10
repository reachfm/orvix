package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type DomainVerifier interface {
	VerifyDomain(context.Context, uint, uint) error
}

type WebhookProcessor interface {
	ProcessOutbox(context.Context, int) error
	ProcessPendingDeliveries(context.Context, int) error
}

func RegisterProductionHandlers(registry *Registry, domains DomainVerifier, webhooks WebhookProcessor) error {
	if domains != nil {
		if err := registry.Register(Definition{Type: "tenant.domain.verify", Scope: ScopeTenant, PayloadVersion: 1, Timeout: 2 * time.Minute, Validate: validateDomainVerification, Handle: func(ctx context.Context, execution Execution, payload json.RawMessage) (json.RawMessage, error) {
			var body domainVerificationPayload
			if err := json.Unmarshal(payload, &body); err != nil {
				return nil, &ExecutionError{Code: "INVALID_PAYLOAD", Message: "domain verification payload is invalid"}
			}
			if err := domains.VerifyDomain(ctx, execution.TenantID(), body.DomainID); err != nil {
				return nil, &ExecutionError{Code: "DOMAIN_VERIFICATION_FAILED", Message: "domain verification failed", Retryable: true}
			}
			return json.RawMessage(`{"verified":true}`), nil
		}}); err != nil {
			return err
		}
	}
	if webhooks != nil {
		definitions := []Definition{
			{Type: "platform.webhooks.outbox", Scope: ScopePlatform, PayloadVersion: 1, Timeout: time.Minute, Validate: validateEmptyPayload, Handle: func(ctx context.Context, _ Execution, _ json.RawMessage) (json.RawMessage, error) {
				if err := webhooks.ProcessOutbox(ctx, 100); err != nil {
					return nil, &ExecutionError{Code: "WEBHOOK_OUTBOX_FAILED", Message: "webhook outbox processing failed", Retryable: true}
				}
				return json.RawMessage(`{"processed":true}`), nil
			}},
			{Type: "platform.webhooks.deliveries", Scope: ScopePlatform, PayloadVersion: 1, Timeout: 2 * time.Minute, Validate: validateEmptyPayload, Handle: func(ctx context.Context, _ Execution, _ json.RawMessage) (json.RawMessage, error) {
				if err := webhooks.ProcessPendingDeliveries(ctx, 100); err != nil {
					return nil, &ExecutionError{Code: "WEBHOOK_DELIVERY_FAILED", Message: "webhook delivery processing failed", Retryable: true}
				}
				return json.RawMessage(`{"processed":true}`), nil
			}},
		}
		for _, definition := range definitions {
			if err := registry.Register(definition); err != nil {
				return err
			}
		}
	}
	return nil
}

type domainVerificationPayload struct {
	DomainID uint `json:"domain_id"`
}

func validateDomainVerification(payload json.RawMessage) error {
	var body domainVerificationPayload
	if json.Unmarshal(payload, &body) != nil || body.DomainID == 0 {
		return fmt.Errorf("domain_id is required")
	}
	return nil
}

func validateEmptyPayload(payload json.RawMessage) error {
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil || len(body) != 0 {
		return fmt.Errorf("payload must be an empty object")
	}
	return nil
}
