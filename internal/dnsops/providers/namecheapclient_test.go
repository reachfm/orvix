package providers

// Unit tests for the Namecheap client abstraction
// (DNS-AUTOMATION-2G) and the read-merge-write logic
// in the Namecheap provider.
//
// The tests use a FakeNamecheapClient so no real HTTP is
// ever issued. The assertion strategy is:
//
//   - pre-seed live state
//   - run Plan / Apply
//   - inspect the captured SetHosts call(s) to confirm the
//     merged set preserved unrelated records and contained
//     exactly the Orvix-managed records the provider
//     intended to write
//   - assert no token-shaped substring leaks through
//     ChangePlan / ApplyResult / Notes / SetHosts payloads

import (
	"context"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dnsops"
)

// fixtureNamecheapPlan returns a deterministic dnsops.Plan
// with the Orvix-managed records populated. The plan's
// domain is "example.com" so it pairs with splitDomain.
func fixtureNamecheapPlan(t *testing.T) *NamecheapHost {
	t.Helper()
	// helper wraps the dnsops fixture so callers can
	// share the canonical Orvix-managed set; we build
	// the plan here directly because the Namecheap
	// provider's read-merge-write works on the
	// dnsops.Record identity, not on a full plan.
	return nil // unused — see fixturePlan below
}

func fixtureNCPlan(t *testing.T, sld, tld string) []NamecheapHost {
	t.Helper()
	// Unrelated website records that must be preserved.
	unrelated := []NamecheapHost{
		{Name: "www", Type: "A", Address: "203.0.113.50", TTL: "1800"},
		{Name: "blog", Type: "CNAME", Address: "www.example.com.", TTL: "1800"},
		{Name: "@", Type: "TXT", Address: "google-site-verification=abc123", TTL: "1800"},
		{Name: "_acme-challenge", Type: "TXT", Address: "third-party-verification", TTL: "60"},
	}
	// Orvix-managed records (current live state) that
	// differ from the desired plan. The provider should
	// update the apex SPF to the new value but preserve
	// the unrelated records.
	orvixStale := []NamecheapHost{
		{Name: "@", Type: "MX", Address: "mail.example.com.", MXPref: "10", TTL: "1800"},
		{Name: "mail", Type: "A", Address: "198.51.100.10", TTL: "1800"},
		{Name: "@", Type: "TXT", Address: "v=spf1 mx ip4:198.51.100.10 -all", TTL: "1800"},
	}
	out := make([]NamecheapHost, 0, len(unrelated)+len(orvixStale))
	out = append(out, unrelated...)
	out = append(out, orvixStale...)
	return out
}

// TestNamecheapClientGetHostsNoCreds: NetNamecheapClient
// refuses to make an HTTP call when credentials are missing.
func TestNamecheapClientGetHostsNoCreds(t *testing.T) {
	c := NewNetNamecheapClient("", "", "", "", false)
	_, err := c.GetHosts(context.Background(), "example", "com")
	if err == nil {
		t.Errorf("GetHosts without credentials must error")
	}
}

// TestNamecheapClientSetHostsNoCreds: same for SetHosts.
func TestNamecheapClientSetHostsNoCreds(t *testing.T) {
	c := NewNetNamecheapClient("", "", "", "", false)
	_, err := c.SetHosts(context.Background(), "example", "com", nil)
	if err == nil {
		t.Errorf("SetHosts without credentials must error")
	}
}

// TestNamecheapClientFakeGetHosts: FakeNamecheapClient returns
// the canned host list.
func TestNamecheapClientFakeGetHosts(t *testing.T) {
	f := NewFakeNamecheapClient()
	f.SetLive("example", "com", []NamecheapHost{
		{Name: "@", Type: "A", Address: "203.0.113.1", TTL: "1800"},
		{Name: "www", Type: "CNAME", Address: "@", TTL: "1800"},
	})
	got, err := f.GetHosts(context.Background(), "example", "com")
	if err != nil {
		t.Fatalf("GetHosts: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 hosts; got %d", len(got))
	}
}

// TestNamecheapClientFakeGetHostsUnknown: a fresh fake returns
// an empty list (not an error) for an unknown domain.
func TestNamecheapClientFakeGetHostsUnknown(t *testing.T) {
	f := NewFakeNamecheapClient()
	got, err := f.GetHosts(context.Background(), "example", "com")
	if err != nil {
		t.Errorf("unknown domain should not error; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown domain should return empty list; got %d", len(got))
	}
}

// TestNamecheapClientFakeGetHostsError: SetGetError surfaces.
func TestNamecheapClientFakeGetHostsError(t *testing.T) {
	f := NewFakeNamecheapClient()
	f.SetGetError(errFake)
	if _, err := f.GetHosts(context.Background(), "example", "com"); err == nil {
		t.Errorf("GetHosts should return the seeded error")
	}
}

// TestNamecheapClientFakeSetHostsRecordsCall: SetHosts is
// recorded.
func TestNamecheapClientFakeSetHostsRecordsCall(t *testing.T) {
	f := NewFakeNamecheapClient()
	_, err := f.SetHosts(context.Background(), "example", "com", []NamecheapHost{
		{Name: "@", Type: "A", Address: "203.0.113.1", TTL: "1800"},
	})
	if err != nil {
		t.Fatalf("SetHosts: %v", err)
	}
	calls := f.SetCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SetHosts call; got %d", len(calls))
	}
	if calls[0].SLD != "example" || calls[0].TLD != "com" {
		t.Errorf("SetHosts SLD/TLD mismatch: %+v", calls[0])
	}
	if len(calls[0].Hosts) != 1 {
		t.Errorf("expected 1 host in call; got %d", len(calls[0].Hosts))
	}
}

// TestNamecheapProviderPreservesUnrelatedRecords is the
// canonical safety test for the read-merge-write: when
// the live zone already contains the Orvix-managed records
// at the desired values PLUS unrelated records, the apply
// must preserve the unrelated records and leave the
// already-correct Orvix-managed records in place. This test
// uses a fixture where the unrelated records do NOT share
// (Name, Type) keys with the Orvix-managed set, so no
// conflict is raised and the apply proceeds. The companion
// test TestNamecheapProviderNoDestructiveDeletes covers
// the conflict path.
func TestNamecheapProviderPreservesUnrelatedRecords(t *testing.T) {
	client := NewFakeNamecheapClient()
	// Unrelated records: www A, blog CNAME, _acme-challenge
	// TXT. Orvix-managed records that already match the
	// desired values (so they are kept as-is, not
	// overwritten).
	client.SetLive("example", "com", []NamecheapHost{
		{Name: "www", Type: "A", Address: "203.0.113.50", TTL: "1800"},
		{Name: "blog", Type: "CNAME", Address: "www.example.com.", TTL: "1800"},
		{Name: "_acme-challenge", Type: "TXT", Address: "third-party-verification", TTL: "60"},
		{Name: "@", Type: "MX", Address: "mail.example.com", MXPref: "10", TTL: "1800"},
		{Name: "mail", Type: "A", Address: "203.0.113.10", TTL: "1800"},
		{Name: "@", Type: "TXT", Address: "v=spf1 mx ip4:203.0.113.10 -all", TTL: "1800"},
		{Name: "mta-sts", Type: "A", Address: "203.0.113.10", TTL: "1800"},
	})
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, client)

	cp, err := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// The plan should NOT include the unrelated records
	// as Steps (Steps are only Orvix-managed records).
	for _, s := range cp.Steps {
		if s.Record.Name == "www" || s.Record.Name == "blog" || s.Record.Name == "_acme-challenge" {
			t.Errorf("plan steps must not include unrelated records; got %s/%s", s.Record.Name, s.Record.Type)
		}
	}
	// Apply the plan with the canonical confirmation.
	res, err := p.Apply(context.Background(), cp, "apply-dns-changes")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Failed != 0 {
		t.Errorf("apply must not fail; got Failed=%d", res.Failed)
	}
	// Inspect the captured SetHosts call. The merged set
	// MUST contain every unrelated record and the
	// already-correct Orvix-managed records.
	calls := client.SetCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SetHosts call; got %d", len(calls))
	}
	merged := calls[0].Hosts
	gotNames := make(map[string]bool, len(merged))
	for _, h := range merged {
		gotNames[h.Name+"|"+h.Type] = true
	}
	for _, want := range []string{
		"www|A", "blog|CNAME", "_acme-challenge|TXT",
		"@|MX", "mail|A", "mta-sts|A", "@|TXT",
	} {
		if !gotNames[want] {
			t.Errorf("merged set missing %s; got %d hosts", want, len(merged))
		}
	}
}

// TestNamecheapProviderRejectsBadConfirmation: Apply refuses
// any confirm value that is not the literal apply-dns-changes.
func TestNamecheapProviderRejectsBadConfirmation(t *testing.T) {
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, NewFakeNamecheapClient())
	if _, err := p.Apply(context.Background(), dnsops.ChangePlan{}, "yes-i-confirm"); err == nil {
		t.Errorf("Apply must reject the legacy 'yes-i-confirm' confirm")
	}
}

// TestNamecheapProviderRejectsEmptyConfirmation: empty
// confirm refuses.
func TestNamecheapProviderRejectsEmptyConfirmation(t *testing.T) {
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, NewFakeNamecheapClient())
	if _, err := p.Apply(context.Background(), dnsops.ChangePlan{}, ""); err == nil {
		t.Errorf("Apply must reject empty confirm")
	}
}

// TestNamecheapProviderRefusesWhenEnableApplyFalse: even with
// valid credentials, Apply refuses when the kill switch is off.
func TestNamecheapProviderRefusesWhenEnableApplyFalse(t *testing.T) {
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: false,
	}, NewFakeNamecheapClient())
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	if _, err := p.Apply(context.Background(), cp, "apply-dns-changes"); err == nil {
		t.Errorf("Apply must refuse when EnableApply=false")
	}
}

// TestNamecheapProviderApplyTokenNeverInPayload: the merged
// set, the ApplyResult, and the per-step Change records never
// contain the API key, the API user, or any token-shaped
// substring.
func TestNamecheapProviderApplyTokenNeverInPayload(t *testing.T) {
	client := NewFakeNamecheapClient()
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "user-secret-leak-1234",
		APIKey:      "key-secret-leak-5678",
		Username:    "u",
		EnableApply: true,
	}, client)
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	res, _ := p.Apply(context.Background(), cp, "apply-dns-changes")
	banned := []string{"user-secret-leak", "key-secret-leak"}
	for _, ch := range cp.Steps {
		for _, b := range banned {
			if strings.Contains(ch.Record.Value, b) || strings.Contains(ch.Reason, b) {
				t.Errorf("token leaked into Change: %s", b)
			}
		}
	}
	for _, n := range cp.Notes {
		for _, b := range banned {
			if strings.Contains(n, b) {
				t.Errorf("token leaked into Notes: %s", b)
			}
		}
	}
	for _, n := range res.Notes {
		for _, b := range banned {
			if strings.Contains(n, b) {
				t.Errorf("token leaked into ApplyResult.Notes: %s", b)
			}
		}
	}
	for _, c := range client.SetCalls() {
		for _, h := range c.Hosts {
			for _, b := range banned {
				if strings.Contains(h.Address, b) {
					t.Errorf("token leaked into SetHosts address: %s", b)
				}
			}
		}
	}
}

// TestNamecheapProviderPlanUnreadableZone: when the live read
// fails, Plan still emits a desired-state shape with
// Action=create. Apply refuses because the read-merge-write
// contract cannot be honoured without a baseline.
func TestNamecheapProviderPlanUnreadableZone(t *testing.T) {
	client := NewFakeNamecheapClient()
	client.SetGetError(errFake)
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, client)
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	if len(cp.Steps) == 0 {
		t.Errorf("Plan must emit Steps even when live read fails")
	}
	for _, s := range cp.Steps {
		if s.Action != dnsops.ActionCreate {
			t.Errorf("unreadable live zone must yield Action=create; got %s", s.Action)
		}
	}
	// Apply must refuse: no safe merge baseline.
	cp2, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	if _, err := p.Apply(context.Background(), cp2, "apply-dns-changes"); err == nil {
		t.Errorf("Apply must refuse when live read fails")
	}
}

// TestNamecheapProviderStatusRefuses: when the live SetHosts
// returns an error, the apply result reports Failed > 0.
func TestNamecheapProviderStatusRefuses(t *testing.T) {
	client := NewFakeNamecheapClient()
	client.SetSetError(errFake)
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, client)
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	res, err := p.Apply(context.Background(), cp, "apply-dns-changes")
	if err != nil {
		t.Fatalf("Apply returned a hard error: %v", err)
	}
	if res.Failed == 0 {
		t.Errorf("Apply must report Failed > 0 when SetHosts errors")
	}
}

// TestNamecheapProviderNoDestructiveDeletes: the merged set
// never silently overwrites an unrelated record. The test
// fixture includes a live @ TXT with the value
// "google-site-verification=abc123" and a desired @ TXT
// with the value "v=spf1 mx ip4:203.0.113.10 -all". These
// share the (Name, Type) key so the provider MUST treat the
// live value as a conflict, not overwrite it. The Apply
// path therefore refuses — the operator must remove the
// unrelated @ TXT before retrying.
func TestNamecheapProviderNoDestructiveDeletes(t *testing.T) {
	client := NewFakeNamecheapClient()
	client.SetLive("example", "com", fixtureNCPlan(t, "example", "com"))
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, client)
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	// The plan must include a Conflict step for the @ TXT
	// record (the live value "google-site-verification=..."
	// conflicts with the desired SPF value).
	hasConflict := false
	for _, s := range cp.Steps {
		if s.Action == dnsops.ActionConflict {
			hasConflict = true
		}
	}
	if !hasConflict {
		t.Errorf("plan must surface Action=Conflict for the unrelated @ TXT live record")
	}
	// Apply must refuse because of the conflict.
	res, err := p.Apply(context.Background(), cp, "apply-dns-changes")
	if err != nil {
		t.Fatalf("apply returned a hard error: %v", err)
	}
	if res.Failed == 0 {
		t.Errorf("apply must refuse with Failed > 0 when a conflict exists")
	}
	// The fake client must NOT have been called (the
	// conflict gate runs before any HTTP / fake call).
	calls := client.SetCalls()
	if len(calls) != 0 {
		t.Errorf("apply must not call SetHosts when a conflict exists; got %d call(s)", len(calls))
	}
}

// TestNamecheapProviderPreservesUnrelatedNonConflicting: when
// the unrelated records do NOT share (Name, Type) keys with
// the Orvix-managed set, the merged set preserves them all
// AND no conflict is raised. The test fixture here only has
// unrelated records under non-conflicting host names (www,
// blog, _acme-challenge) — no @ TXT unrelated record.
func TestNamecheapProviderPreservesUnrelatedNonConflicting(t *testing.T) {
	client := NewFakeNamecheapClient()
	client.SetLive("example", "com", []NamecheapHost{
		{Name: "www", Type: "A", Address: "203.0.113.50", TTL: "1800"},
		{Name: "blog", Type: "CNAME", Address: "www.example.com.", TTL: "1800"},
		{Name: "_acme-challenge", Type: "TXT", Address: "third-party", TTL: "60"},
	})
	p := NewNamecheapProvider(NamecheapConfig{
		APIUser:     "u",
		APIKey:      "k",
		Username:    "u",
		EnableApply: true,
	}, client)
	cp, _ := p.Plan(context.Background(), fixturePlanForNamecheap(t))
	for _, s := range cp.Steps {
		if s.Action == dnsops.ActionConflict {
			t.Errorf("plan must not surface a conflict when unrelated records do not share keys; got %s/%s", s.Record.Name, s.Record.Type)
		}
	}
	res, _ := p.Apply(context.Background(), cp, "apply-dns-changes")
	if res.Failed != 0 {
		t.Errorf("apply must succeed when no conflicts; got Failed=%d", res.Failed)
	}
	calls := client.SetCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SetHosts call; got %d", len(calls))
	}
	// The merged set must contain all 3 unrelated records.
	merged := calls[0].Hosts
	gotUnrelated := 0
	for _, h := range merged {
		if h.Name == "www" || h.Name == "blog" || h.Name == "_acme-challenge" {
			gotUnrelated++
		}
	}
	if gotUnrelated != 3 {
		t.Errorf("merged set must preserve all 3 unrelated records; got %d", gotUnrelated)
	}
}

// errFake is a small stand-in for the network / API errors
// the fake client surfaces in the unreadable-zone tests.
var errFake = stringError("namecheap api error")

// stringError is a tiny error implementation used by the
// fake client to surface seeded errors in tests.
type stringError string

func (s stringError) Error() string { return string(s) }

// fixturePlanForNamecheap returns a minimal dnsops.Plan for
// Namecheap-specific tests. The plan uses the canonical
// Orvix-managed record set so the read-merge-write logic
// has something concrete to classify.
func fixturePlanForNamecheap(t *testing.T) *dnsops.Plan {
	t.Helper()
	g := dnsops.NewGenerator()
	plan, err := g.Generate(dnsops.Inputs{
		Domain:     "example.com",
		MailHost:   "mail.example.com",
		ServerIPv4: "203.0.113.10",
		MTAMode:    "testing",
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return plan
}
