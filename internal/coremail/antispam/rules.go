package antispam

import (
	"fmt"
	"net"
	"strings"
)

// Built-in rule names.
const (
	RuleSPFFail         = "SPF_FAIL"
	RuleDMARCReject     = "DMARC_REJECT"
	RuleMissingReverseDNS = "MISSING_REVERSE_DNS"
	RuleHELOMismatch    = "HELO_MISMATCH"
	RuleHELOSuspicious  = "HELO_SUSPICIOUS"
	RuleSenderNoMX      = "SENDER_NO_MX"
	RuleTooManyRecipients = "TOO_MANY_RECIPIENTS"
	RuleEmptyMailFrom   = "EMPTY_MAIL_FROM"
	RuleBadIP           = "BAD_IP"
	RuleAllowedIP       = "ALLOWED_IP"
)

// ruleBase provides common Rule fields.
type ruleBase struct {
	name    string
	weight  float64
	enabled bool
}

func (r *ruleBase) Name() string     { return r.name }
func (r *ruleBase) Weight() float64  { return r.weight }
func (r *ruleBase) Enabled() bool    { return r.enabled }

// ── SPF Fail Rule ───────────────────────────────────────

type spfFailRule struct{ ruleBase }

func newSPFFailRule() Rule {
	return &spfFailRule{ruleBase{name: RuleSPFFail, weight: 5.0, enabled: true}}
}

func (r *spfFailRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.SPFResult == "fail" {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("SPF fail for domain %s", ctx.MailFromDomain),
		}
	}
	if ctx.SPFResult == "softfail" {
		return &RuleResult{
			Name: r.name, Score: r.weight * 0.5, Match: true,
			Reason: fmt.Sprintf("SPF softfail for domain %s", ctx.MailFromDomain),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── DMARC Reject Rule ───────────────────────────────────

type dmarcRejectRule struct{ ruleBase }

func newDMARCRejectRule() Rule {
	return &dmarcRejectRule{ruleBase{name: RuleDMARCReject, weight: 6.0, enabled: true}}
}

func (r *dmarcRejectRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.DMARCResult == "fail" && ctx.DMARCPolicy == "reject" {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: "DMARC fail with reject policy",
		}
	}
	if ctx.DMARCResult == "fail" {
		return &RuleResult{
			Name: r.name, Score: r.weight * 0.5, Match: true,
			Reason: "DMARC fail",
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Missing Reverse DNS Rule ────────────────────────────

type missingReverseDNSRule struct{ ruleBase }

func newMissingReverseDNSRule() Rule {
	return &missingReverseDNSRule{ruleBase{name: RuleMissingReverseDNS, weight: 1.0, enabled: true}}
}

func (r *missingReverseDNSRule) Evaluate(ctx *RuleContext) *RuleResult {
	if !ctx.HasReverseDNS {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("no reverse DNS for %s", ctx.RemoteIP),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── HELO Domain Mismatch Rule ───────────────────────────

type heloMismatchRule struct{ ruleBase }

func newHELOMismatchRule() Rule {
	return &heloMismatchRule{ruleBase{name: RuleHELOMismatch, weight: 2.0, enabled: true}}
}

func (r *heloMismatchRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.HELODomain == "" || ctx.MailFromDomain == "" {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	// The HELO domain should match or be a subdomain of the MAIL FROM domain.
	// If they don't share an organizational domain, flag it.
	if !strings.EqualFold(ctx.HELODomain, ctx.MailFromDomain) &&
		!strings.HasSuffix(ctx.HELODomain, "."+ctx.MailFromDomain) &&
		!strings.HasSuffix(ctx.MailFromDomain, "."+ctx.HELODomain) {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("HELO domain %s does not match sender domain %s",
				ctx.HELODomain, ctx.MailFromDomain),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Suspicious HELO Literal Rule ────────────────────────

type heloSuspiciousRule struct{ ruleBase }

func newHELOSuspiciousRule() Rule {
	return &heloSuspiciousRule{ruleBase{name: RuleHELOSuspicious, weight: 4.0, enabled: true}}
}

func (r *heloSuspiciousRule) Evaluate(ctx *RuleContext) *RuleResult {
	helo := ctx.HELODomain
	if helo == "" {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	// Suspicious: HELO is an IP literal in brackets.
	if strings.HasPrefix(helo, "[") && strings.HasSuffix(helo, "]") {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("HELO is an IP literal: %s", helo),
		}
	}
	// Suspicious: HELO is a bare IP address (no brackets).
	if net.ParseIP(helo) != nil {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("HELO is a bare IP: %s", helo),
		}
	}
	// Suspicious: HELO is localhost or similar.
	lower := strings.ToLower(helo)
	if lower == "localhost" || lower == "localhost.localdomain" || strings.HasPrefix(lower, "localhost") {
		return &RuleResult{
			Name: r.name, Score: r.weight * 0.5, Match: true,
			Reason: fmt.Sprintf("HELO is localhost: %s", helo),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Sender Domain Missing MX Rule ───────────────────────

type senderNoMXRule struct{ ruleBase }

func newSenderNoMXRule() Rule {
	return &senderNoMXRule{ruleBase{name: RuleSenderNoMX, weight: 2.0, enabled: true}}
}

func (r *senderNoMXRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.MailFromDomain == "" {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	if ctx.Reputation == nil {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	rep := ctx.Reputation.SenderDomainReputation(ctx.MailFromDomain)
	if rep.KnownGood {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	if !rep.HasMX && rep.Confidence > 0 {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("sender domain %s has no MX records", ctx.MailFromDomain),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Too Many Recipients Rule ────────────────────────────

type tooManyRecipientsRule struct{ ruleBase }

func newTooManyRecipientsRule() Rule {
	return &tooManyRecipientsRule{ruleBase{name: RuleTooManyRecipients, weight: 2.0, enabled: true}}
}

func (r *tooManyRecipientsRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.RecipientCount > 10 {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("%d recipients exceeds threshold", ctx.RecipientCount),
		}
	}
	if ctx.RecipientCount > 5 {
		return &RuleResult{
			Name: r.name, Score: r.weight * 0.5, Match: true,
			Reason: fmt.Sprintf("elevated recipient count: %d", ctx.RecipientCount),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Empty MAIL FROM Rule ────────────────────────────────

type emptyMailFromRule struct{ ruleBase }

func newEmptyMailFromRule() Rule {
	return &emptyMailFromRule{ruleBase{name: RuleEmptyMailFrom, weight: 1.0, enabled: true}}
}

func (r *emptyMailFromRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.MailFromDomain == "" && ctx.FromDomain == "" {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: "empty MAIL FROM with no From header domain",
		}
	}
	if ctx.MailFromDomain == "" {
		return &RuleResult{
			Name: r.name, Score: r.weight * 0.5, Match: true,
			Reason: "empty MAIL FROM (bounce/DSN)",
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Bad IP Rule ─────────────────────────────────────────

type badIPRule struct{ ruleBase }

func newBadIPRule() Rule {
	return &badIPRule{ruleBase{name: RuleBadIP, weight: 8.0, enabled: true}}
}

func (r *badIPRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.Reputation == nil {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	if ctx.Reputation.IsBadIP(ctx.RemoteIP) {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("known bad IP: %s", ctx.RemoteIP),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}

// ── Allowed IP Rule (negative weight = score decrease) ──

type allowedIPRule struct{ ruleBase }

func newAllowedIPRule() Rule {
	return &allowedIPRule{ruleBase{name: RuleAllowedIP, weight: -5.0, enabled: true}}
}

func (r *allowedIPRule) Evaluate(ctx *RuleContext) *RuleResult {
	if ctx.Reputation == nil {
		return &RuleResult{Name: r.name, Score: 0, Match: false}
	}
	if ctx.Reputation.IsAllowedIP(ctx.RemoteIP) {
		return &RuleResult{
			Name: r.name, Score: r.weight, Match: true,
			Reason: fmt.Sprintf("known allowed IP: %s", ctx.RemoteIP),
		}
	}
	return &RuleResult{Name: r.name, Score: 0, Match: false}
}
