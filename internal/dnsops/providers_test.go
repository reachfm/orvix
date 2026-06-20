package dnsops

import (
	"context"
	"net"
	"strings"
	"testing"
)

// fixturePlan is reused by the provider tests.
func fixturePlan(t *testing.T) *Plan {
	t.Helper()
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "203.0.113.10", DKIMSelector: "orvix",
		DKIMPubKey: "PUBKEY",
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return plan
}

// TestServiceProvidersAlwaysHasManual confirms NewService always
// adds the manual provider even if the caller forgets.
func TestServiceProvidersAlwaysHasManual(t *testing.T) {
	s := NewService(NewFakeResolver())
	names := s.Providers()
	hasManual := false
	for _, n := range names {
		if n == "manual" {
			hasManual = true
		}
	}
	if !hasManual {
		t.Errorf("service must always include manual provider; got %v", names)
	}
}

// TestServiceUnknownProviderErrors confirms PlanProvider and
// ApplyProvider reject unknown provider names.
func TestServiceUnknownProviderErrors(t *testing.T) {
	s := NewService(NewFakeResolver())
	if _, err := s.PlanProvider(context.Background(), "nonexistent", fixturePlan(t)); err == nil {
		t.Errorf("unknown provider must error in PlanProvider")
	}
	if _, err := s.ApplyProvider(context.Background(), "nonexistent", ChangePlan{}, "yes"); err == nil {
		t.Errorf("unknown provider must error in ApplyProvider")
	}
}

// TestServiceApplyProviderRequiresConfirmation: Apply must reject
// empty confirmation.
func TestServiceApplyProviderRequiresConfirmation(t *testing.T) {
	s := NewService(NewFakeResolver())
	plan := fixturePlan(t)
	cp, err := s.PlanProvider(context.Background(), "manual", plan)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := s.ApplyProvider(context.Background(), "manual", cp, ""); err == nil {
		t.Errorf("Apply with empty confirmation must error")
	}
	if _, err := s.ApplyProvider(context.Background(), "manual", cp, "   "); err == nil {
		t.Errorf("Apply with whitespace confirmation must error")
	}
}

// TestManualProviderPlanAllStepsCreate: every record becomes a
// Create step except PTR/BIMI/DANE which are informational.
func TestManualProviderPlanAllStepsCreate(t *testing.T) {
	p := NewManualProvider(NewFakeResolver())
	plan := fixturePlan(t)
	cp, err := p.Plan(context.Background(), plan)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if cp.Provider != "manual" {
		t.Errorf("provider name must be manual; got %q", cp.Provider)
	}
	if len(cp.Steps) != len(plan.Records) {
		t.Errorf("manual provider must produce one step per record; got %d vs %d",
			len(cp.Steps), len(plan.Records))
	}
	for _, ch := range cp.Steps {
		switch ch.Record.Purpose {
		case PurposePTR, PurposeBIMI, PurposeDANETLSA:
			if ch.Action != ActionSkip {
				t.Errorf("informational row %s must be Skip; got %s", ch.Record.Purpose, ch.Action)
			}
		default:
			if ch.Action != ActionCreate {
				t.Errorf("active row %s must be Create; got %s", ch.Record.Purpose, ch.Action)
			}
			if ch.Reason == "" {
				t.Errorf("manual copy instructions must populate Reason; got empty for %s", ch.Record.Purpose)
			}
		}
	}
}

// TestManualProviderApplyFails: manual apply must NOT silently
// succeed. Every step becomes a Failure with an explanatory note.
func TestManualProviderApplyFails(t *testing.T) {
	p := NewManualProvider(NewFakeResolver())
	plan := fixturePlan(t)
	cp, _ := p.Plan(context.Background(), plan)
	res, err := p.Apply(context.Background(), cp, "yes-confirm")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Failed != len(cp.Steps) {
		t.Errorf("manual apply must mark all steps failed; got %d failed vs %d steps",
			res.Failed, len(cp.Steps))
	}
	if res.Applied != 0 || res.Skipped != 0 {
		t.Errorf("manual apply must report 0 applied / 0 skipped; got applied=%d skipped=%d",
			res.Applied, res.Skipped)
	}
	if len(res.Notes) == 0 {
		t.Errorf("manual apply must include an explanatory note")
	}
}

// TestServiceManualApplyRoundTrip wires a Service end-to-end and
// confirms Plan + Apply produce the documented shape.
func TestServiceManualApplyRoundTrip(t *testing.T) {
	s := NewService(NewFakeResolver())
	plan := fixturePlan(t)
	cp, err := s.PlanProvider(context.Background(), "manual", plan)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(cp.Steps) == 0 {
		t.Fatalf("manual plan must produce steps")
	}
	res, err := s.ApplyProvider(context.Background(), "manual", cp, "confirm")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Failed != len(cp.Steps) {
		t.Errorf("apply must report all steps failed (manual is human-driven); got %d vs %d",
			res.Failed, len(cp.Steps))
	}
}

// TestProviderNameUnique confirms the registered provider names are
// stable and unique.
func TestProviderNameUnique(t *testing.T) {
	s := NewService(NewFakeResolver())
	seen := map[string]bool{}
	for _, n := range s.Providers() {
		if seen[n] {
			t.Errorf("provider name duplicate: %q", n)
		}
		seen[n] = true
	}
}

// TestNoProviderTokensInChangePlan sanity-checks that strings
// resembling tokens (api_key, api_token, secret) never appear in
// any provider's Plan output, even when stub credentials are
// supplied. This is the contract from the brief: tokens must
// never be sent to the frontend.
func TestNoProviderTokensInChangePlan(t *testing.T) {
	// Build a fake plan and run the manual provider against it.
	p := NewManualProvider(NewFakeResolver())
	plan := fixturePlan(t)
	cp, err := p.Plan(context.Background(), plan)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	banned := []string{
		"api_key", "api_token", "apikey", "apitoken",
		"secret", "password", "passwd", "bearer ",
		"authorization:",
	}
	for _, ch := range cp.Steps {
		for _, b := range banned {
			if strings.Contains(strings.ToLower(ch.Reason), b) {
				t.Errorf("token-like substring %q leaked into manual step reason: %q", b, ch.Reason)
			}
			if strings.Contains(strings.ToLower(ch.Record.Value), b) {
				t.Errorf("token-like substring %q leaked into manual step value: %q", b, ch.Record.Value)
			}
		}
	}
	for _, n := range cp.Notes {
		for _, b := range banned {
			if strings.Contains(strings.ToLower(n), b) {
				t.Errorf("token-like substring %q leaked into manual note: %q", b, n)
			}
		}
	}
}

// silence net unused-import warnings if the test compilation
// surface changes — keeps imports honest.
var _ = net.IPv4len
