package providers

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dnsops"
)

// fixturePlan returns a deterministic plan used by the
// provider-integration tests below.
func fixturePlan(t *testing.T) *dnsops.Plan {
	t.Helper()
	g := dnsops.NewGenerator()
	plan, err := g.Generate(dnsops.Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", DKIMSelector: "orvix",
		DKIMPubKey: "PUBKEY",
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return plan
}

// TestCloudflareProviderNoCredentialsReturnsNotConfigured: without
// a token, Plan returns an empty change list and a single "not
// configured" note — never a fake success.
func TestCloudflareProviderNoCredentialsReturnsNotConfigured(t *testing.T) {
	p := NewCloudflareProvider(CloudflareConfig{}, dnsops.NewFakeResolver())
	cp, err := p.Plan(context.Background(), fixturePlan(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if cp.Provider != "cloudflare" {
		t.Errorf("provider name must be cloudflare; got %q", cp.Provider)
	}
	if len(cp.Steps) != 0 {
		t.Errorf("cloudflare without token must return 0 steps; got %d", len(cp.Steps))
	}
	if len(cp.Notes) == 0 {
		t.Errorf("cloudflare without token must include a 'not configured' note")
	}
}

// TestCloudflareProviderWithCredentialsDryRun: with credentials
// configured, Plan emits a dry-run change list. Apply still
// refuses (no external HTTP call).
func TestCloudflareProviderWithCredentialsDryRun(t *testing.T) {
	p := NewCloudflareProvider(CloudflareConfig{
		APIToken: "secret-token-do-not-leak",
		ZoneID:   "zone-1",
	}, dnsops.NewFakeResolver())
	cp, err := p.Plan(context.Background(), fixturePlan(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(cp.Steps) == 0 {
		t.Errorf("cloudflare with credentials must emit dry-run steps")
	}
	for _, ch := range cp.Steps {
		if strings.Contains(ch.Record.Value, "secret-token") {
			t.Errorf("token leaked into record value: %q", ch.Record.Value)
		}
		if strings.Contains(ch.Reason, "secret-token") {
			t.Errorf("token leaked into reason: %q", ch.Reason)
		}
	}
	for _, n := range cp.Notes {
		if strings.Contains(n, "secret-token") {
			t.Errorf("token leaked into notes: %q", n)
		}
	}
	res, err := p.Apply(context.Background(), cp, "confirm")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Failed != len(cp.Steps) {
		t.Errorf("cloudflare apply must mark all steps failed (no HTTP in this build); got %d failed",
			res.Failed)
	}
}

// TestCloudflareProviderRequiresConfirmation: empty confirm errors.
func TestCloudflareProviderRequiresConfirmation(t *testing.T) {
	p := NewCloudflareProvider(CloudflareConfig{APIToken: "x", ZoneID: "y"}, dnsops.NewFakeResolver())
	cp, _ := p.Plan(context.Background(), fixturePlan(t))
	if _, err := p.Apply(context.Background(), cp, ""); err == nil {
		t.Errorf("cloudflare apply with empty confirm must error")
	}
}

// TestCloudflareProviderApplyWithoutCredentials: refuse even when
// confirm is supplied if no token is configured.
func TestCloudflareProviderApplyWithoutCredentials(t *testing.T) {
	p := NewCloudflareProvider(CloudflareConfig{}, dnsops.NewFakeResolver())
	if _, err := p.Apply(context.Background(), dnsops.ChangePlan{}, "confirm"); err == nil {
		t.Errorf("cloudflare apply without credentials must error")
	}
}

// TestNamecheapProviderNoCredentialsReturnsNotConfigured: same
// shape as cloudflare.
func TestNamecheapProviderNoCredentialsReturnsNotConfigured(t *testing.T) {
	p := NewNamecheapProvider(NamecheapConfig{}, NewFakeNamecheapClient())
	cp, err := p.Plan(context.Background(), fixturePlan(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if cp.Provider != "namecheap" {
		t.Errorf("provider name must be namecheap; got %q", cp.Provider)
	}
	if len(cp.Steps) != 0 {
		t.Errorf("namecheap without token must return 0 steps; got %d", len(cp.Steps))
	}
}

// TestNamecheapProviderWithCredentialsDryRun: tokens never appear
// in the change plan; Apply succeeds against the fake client (no
// real HTTP), and the SetCalls log shows the merged set was
// submitted with the Orvix-managed records.
func TestNamecheapProviderWithCredentialsDryRun(t *testing.T) {
	client := NewFakeNamecheapClient()
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:      "user-secret-do-not-leak",
		APIKey:       "key-secret-do-not-leak",
		Username:     "u",
		EnableApply:  true,
	}, client)
	cp, err := p.Plan(context.Background(), fixturePlan(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, ch := range cp.Steps {
		for _, banned := range []string{"user-secret", "key-secret"} {
			if strings.Contains(ch.Record.Value, banned) {
				t.Errorf("token leaked into record value: %q", ch.Record.Value)
			}
			if strings.Contains(ch.Reason, banned) {
				t.Errorf("token leaked into reason: %q", ch.Reason)
			}
		}
	}
	for _, n := range cp.Notes {
		for _, banned := range []string{"user-secret", "key-secret"} {
			if strings.Contains(n, banned) {
				t.Errorf("token leaked into notes: %q", n)
			}
		}
	}
	res, err := p.Apply(context.Background(), cp, "apply-dns-changes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Apply should succeed against the fake client; the
	// real assertion is that no token-shaped substring
	// leaks into the response and that the fake client
	// recorded exactly one SetHosts call.
	if res.Failed != 0 {
		t.Errorf("namecheap apply against fake client must not fail; got %d failed", res.Failed)
	}
	calls := client.SetCalls()
	if len(calls) != 1 {
		t.Errorf("namecheap apply should call SetHosts exactly once; got %d", len(calls))
	}
	if len(calls) > 0 {
		for _, h := range calls[0].Hosts {
			if strings.Contains(h.Address, "user-secret") || strings.Contains(h.Address, "key-secret") {
				t.Errorf("token leaked into SetHosts address: %q", h.Address)
			}
		}
	}
}

// TestNamecheapProviderRequiresConfirmation: empty confirm errors.
func TestNamecheapProviderRequiresConfirmation(t *testing.T) {
	p := NewNamecheapProvider(NamecheapConfig{APIUser: "u", APIKey: "k", Username: "u"}, NewFakeNamecheapClient())
	if _, err := p.Apply(context.Background(), dnsops.ChangePlan{}, ""); err == nil {
		t.Errorf("namecheap apply with empty confirm must error")
	}
}

// silence net unused-import warnings if the test compilation
// surface changes — keeps imports honest.
var _ = net.IPv4len
