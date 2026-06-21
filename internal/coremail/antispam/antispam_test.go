package antispam

import (
	"net"
	"strings"
	"testing"
)

func testReporter() *MemoryReporter {
	r := NewMemoryReporter()
	r.AddBadIP(net.ParseIP("10.0.0.1"))
	r.AddAllowedIP(net.ParseIP("192.168.1.1"))
	r.AddAllowedCIDR("10.0.0.0/8")
	r.SetDomainReputation("good.com", DomainReputation{KnownGood: true, HasMX: true, Confidence: 1.0})
	r.SetDomainReputation("nomx.com", DomainReputation{KnownGood: false, HasMX: false, Confidence: 0.8})
	r.SetDomainReputation("mx.com", DomainReputation{KnownGood: true, HasMX: true, Confidence: 0.9})
	return r
}

func testEngine() *Engine {
	return NewEngine(testReporter())
}

func testCtx() *RuleContext {
	return &RuleContext{
		RemoteIP:       net.ParseIP("203.0.113.1"),
		HELODomain:     "mail.test.com",
		MailFromDomain: "test.com",
		SPFResult:      "pass",
		DKIMResult:     "pass",
		DMARCResult:    "pass",
		RecipientCount: 1,
		HasReverseDNS:  true,
		Reputation:     testReporter(),
	}
}

func assertScore(t *testing.T, assessment *SpamAssessment, expected float64) {
	t.Helper()
	if assessment.Score != expected {
		t.Fatalf("expected score %.1f, got %.1f", expected, assessment.Score)
	}
}

func assertVerdict(t *testing.T, assessment *SpamAssessment, expected Verdict) {
	t.Helper()
	if assessment.Verdict != expected {
		t.Fatalf("expected verdict %s, got %s", expected, assessment.Verdict)
	}
}

// ── SPF Fail ───────────────────────────────────────────────

func TestSPFFailIncreasesScore(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.SPFResult = "fail"
	a := e.Assess(ctx)
	if a.Score <= 0 {
		t.Fatal("expected SPF fail to increase score")
	}
	if !ruleMatched(a, RuleSPFFail) {
		t.Fatal("expected SPF_FAIL rule to match")
	}
}

// ── DMARC Reject ───────────────────────────────────────────

func TestDMARCRejectIncreasesScore(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.DMARCResult = "fail"
	ctx.DMARCPolicy = "reject"
	a := e.Assess(ctx)
	if a.Score <= 0 {
		t.Fatal("expected DMARC reject to increase score")
	}
	if !ruleMatched(a, RuleDMARCReject) {
		t.Fatal("expected DMARC_REJECT rule to match")
	}
}

// ── Missing Reverse DNS ────────────────────────────────────

func TestMissingReverseDNS(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HasReverseDNS = false
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleMissingReverseDNS) {
		t.Fatal("expected MISSING_REVERSE_DNS rule to match")
	}
}

// ── HELO Mismatch ──────────────────────────────────────────

func TestHELOMismatchRule(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HELODomain = "attacker.com"
	ctx.MailFromDomain = "victim.com"
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleHELOMismatch) {
		t.Fatal("expected HELO_MISMATCH rule to match")
	}
}

func TestHELOMatchDoesNotFire(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HELODomain = "mail.test.com"
	ctx.MailFromDomain = "test.com"
	a := e.Assess(ctx)
	if ruleMatched(a, RuleHELOMismatch) {
		t.Fatal("expected HELO_MISMATCH not to fire when HELO is subdomain")
	}
}

// ── Suspicious HELO Literal ────────────────────────────────

func TestSuspiciousHELOLiteral(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HELODomain = "[10.0.0.1]"
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleHELOSuspicious) {
		t.Fatal("expected HELO_SUSPICIOUS for IP literal")
	}
}

func TestSuspiciousHELOBareIP(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HELODomain = "10.0.0.1"
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleHELOSuspicious) {
		t.Fatal("expected HELO_SUSPICIOUS for bare IP")
	}
}

func TestSuspiciousHELOLocalhost(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.HELODomain = "localhost"
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleHELOSuspicious) {
		t.Fatal("expected HELO_SUSPICIOUS for localhost")
	}
}

// ── Sender Domain Missing MX ───────────────────────────────

func TestSenderDomainNoMX(t *testing.T) {
	e := NewEngine(NewMemoryReporter())
	reporter := testReporter()
	ctx := testCtx()
	ctx.MailFromDomain = "nomx.com"
	ctx.Reputation = reporter
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleSenderNoMX) {
		t.Fatal("expected SENDER_NO_MX for domain without MX")
	}
}

func TestSenderDomainWithMXNoPenalty(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.MailFromDomain = "mx.com"
	a := e.Assess(ctx)
	if ruleMatched(a, RuleSenderNoMX) {
		t.Fatal("expected no SENDER_NO_MX for domain with MX")
	}
}

// ── Too Many Recipients ────────────────────────────────────

func TestTooManyRecipientsRule(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.RecipientCount = 11
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleTooManyRecipients) {
		t.Fatal("expected TOO_MANY_RECIPIENTS for 11 recipients")
	}
}

// ── Bad IP Rule ────────────────────────────────────────────

func TestBadIPRule(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.RemoteIP = net.ParseIP("10.0.0.1")
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleBadIP) {
		t.Fatal("expected BAD_IP for known bad IP")
	}
}

// ── Allowed IP Lowers Score ────────────────────────────────

func TestAllowedIPLowersScore(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.RemoteIP = net.ParseIP("192.168.1.1")
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleAllowedIP) {
		t.Fatal("expected ALLOWED_IP rule to match")
	}
}

// ── Assessment Headers ─────────────────────────────────────

func TestAssessmentHeaders(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.SPFResult = "fail"
	ctx.DMARCResult = "fail"
	ctx.DMARCPolicy = "reject"
	a := e.Assess(ctx)

	scoreHeader := FormatSpamScoreHeader(a)
	if scoreHeader == "" {
		t.Fatal("expected non-empty score header")
	}

	verdictHeader := FormatSpamVerdictHeader(a)
	if verdictHeader == "" {
		t.Fatal("expected non-empty verdict header")
	}
}

// ── Clean Message Remains Accept ──────────────────────────

func TestCleanMessageRemainsAccept(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	a := e.Assess(ctx)
	assertVerdict(t, a, VerdictAccept)
	if a.Score != 0 {
		t.Fatalf("expected score 0 for clean message, got %.1f", a.Score)
	}
}

// ── Reject Verdict at Threshold ────────────────────────────

func TestRejectVerdictAtThreshold(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.SPFResult = "fail"
	ctx.DMARCResult = "fail"
	ctx.DMARCPolicy = "reject"
	ctx.RemoteIP = net.ParseIP("10.0.0.1") // known bad
	ctx.HasReverseDNS = false
	ctx.RecipientCount = 15
	a := e.Assess(ctx)
	if a.Verdict != VerdictReject {
		t.Fatalf("expected reject verdict, got %s (score=%.1f)", a.Verdict, a.Score)
	}
}

// ── Nil Context ────────────────────────────────────────────

func TestNilContext(t *testing.T) {
	e := testEngine()
	a := e.Assess(nil)
	if a.Verdict != VerdictAccept {
		t.Fatal("expected accept for nil context")
	}
}

// ── Empty MAIL FROM ────────────────────────────────────────

func TestEmptyMailFromRule(t *testing.T) {
	e := testEngine()
	ctx := testCtx()
	ctx.MailFromDomain = ""
	ctx.FromDomain = ""
	a := e.Assess(ctx)
	if !ruleMatched(a, RuleEmptyMailFrom) {
		t.Fatal("expected EMPTY_MAIL_FROM to match")
	}
}

// ── Reputation Provider ────────────────────────────────────

func TestMemoryReporterBadIP(t *testing.T) {
	r := NewMemoryReporter()
	r.AddBadIP(net.ParseIP("1.2.3.4"))
	if !r.IsBadIP(net.ParseIP("1.2.3.4")) {
		t.Fatal("expected bad IP to be detected")
	}
	if r.IsBadIP(net.ParseIP("5.6.7.8")) {
		t.Fatal("expected non-bad IP to not match")
	}
}

func TestMemoryReporterAllowedIP(t *testing.T) {
	r := NewMemoryReporter()
	r.AddAllowedIP(net.ParseIP("10.0.0.1"))
	if !r.IsAllowedIP(net.ParseIP("10.0.0.1")) {
		t.Fatal("expected allowed IP to be detected")
	}
}

func TestMemoryReporterDomainRep(t *testing.T) {
	r := NewMemoryReporter()
	r.SetDomainReputation("known.com", DomainReputation{KnownGood: true, HasMX: true, Confidence: 1.0})
	rep := r.SenderDomainReputation("known.com")
	if !rep.KnownGood {
		t.Fatal("expected known.com to be good")
	}
	rep2 := r.SenderDomainReputation("unknown.com")
	if rep2.Confidence != 0 {
		t.Fatal("expected unknown domain to have zero confidence")
	}
}

// ── HELPERS ────────────────────────────────────────────────

func ruleMatched(a *SpamAssessment, name string) bool {
	for _, r := range a.MatchedRules {
		if r.Name == name {
			return true
		}
	}
	return false
}

var _ = strings.Contains

// ── INBOUND-RECEIVE-3A Regression Tests ───────────────────

func TestEngineAssessWithNilReputationNoPanic(t *testing.T) {
	// Engine created with nil ReputationProvider, matching
	// production NewEngine(nil) in the CoreMail runtime module.
	// The nil reputation must never cause a panic during
	// assessment — it must be safely handled.
	e := NewEngine(nil)
	ctx := &RuleContext{
		RemoteIP:       net.ParseIP("203.0.113.1"),
		HELODomain:     "mail.gmail.com",
		MailFromDomain: "gmail.com",
		SPFResult:      "pass",
		DKIMResult:     "pass",
		DMARCResult:    "pass",
		RecipientCount: 1,
		HasReverseDNS:  true,
		Reputation:     nil, // nil reputation — must not panic
	}
	a := e.Assess(ctx)
	if a.Verdict != VerdictAccept {
		t.Fatalf("expected accept with nil reputation, got %s (score=%.1f)", a.Verdict, a.Score)
	}
}

func TestSenderNoMXRuleNilReputationNoPanic(t *testing.T) {
	rule := &senderNoMXRule{ruleBase{name: RuleSenderNoMX, weight: 2.0, enabled: true}}
	ctx := &RuleContext{
		MailFromDomain: "gmail.com",
		Reputation:     nil, // nil — must not panic
	}
	result := rule.Evaluate(ctx)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Match {
		t.Fatal("expected no match when reputation is nil")
	}
	if result.Score != 0 {
		t.Fatal("expected score 0 when reputation is nil")
	}
}

func TestBadIPRuleNilReputationNoPanic(t *testing.T) {
	rule := &badIPRule{ruleBase{name: RuleBadIP, weight: 8.0, enabled: true}}
	ctx := &RuleContext{
		RemoteIP:   net.ParseIP("10.0.0.1"),
		Reputation: nil, // nil — must not panic
	}
	result := rule.Evaluate(ctx)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Match {
		t.Fatal("expected no match when reputation is nil")
	}
}

func TestAllowedIPRuleNilReputationNoPanic(t *testing.T) {
	rule := &allowedIPRule{ruleBase{name: RuleAllowedIP, weight: -5.0, enabled: true}}
	ctx := &RuleContext{
		RemoteIP:   net.ParseIP("192.168.1.1"),
		Reputation: nil, // nil — must not panic
	}
	result := rule.Evaluate(ctx)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Match {
		t.Fatal("expected no match when reputation is nil")
	}
}
