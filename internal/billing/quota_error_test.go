package billing

import (
	"testing"

	"github.com/orvix/orvix/internal/platform/kernel"
)

func TestQuotaCheckResult_AsError_AllowedReturnsNil(t *testing.T) {
	r := &QuotaCheckResult{Allowed: true}
	if err := r.AsError("domains"); err != nil {
		t.Fatalf("expected nil for an allowed result, got %v", err)
	}
}

func TestQuotaCheckResult_AsError_OverLimitIsQuotaExceeded(t *testing.T) {
	r := &QuotaCheckResult{Allowed: false, Limit: 5, Used: 5}
	err := r.AsError("domains")
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeQuotaExceeded {
		t.Fatalf("expected ErrCodeQuotaExceeded, got %v", err)
	}
}

func TestQuotaCheckResult_AsError_SuspendedSubscriptionIsForbidden(t *testing.T) {
	r := &QuotaCheckResult{Allowed: false, Reason: "subscription is suspended"}
	err := r.AsError("domains")
	apiErr, ok := err.(*kernel.Error)
	if !ok || apiErr.Code != kernel.ErrCodeForbidden {
		t.Fatalf("expected ErrCodeForbidden, got %v", err)
	}
}
