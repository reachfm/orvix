package antispam

import (
	"fmt"
	"net"
)

// Engine evaluates anti-spam rules and produces a SpamAssessment.
type Engine struct {
	rules      []Rule
	thresholds Thresholds
	reputation ReputationProvider
}

// NewEngine creates an anti-spam engine with default rules.
func NewEngine(reputation ReputationProvider) *Engine {
	return &Engine{
		rules:      defaultRules(),
		thresholds: DefaultThresholds(),
		reputation: reputation,
	}
}

// NewEngineWithRules creates an anti-spam engine with custom rules.
func NewEngineWithRules(rules []Rule, reputation ReputationProvider) *Engine {
	return &Engine{
		rules:      rules,
		thresholds: DefaultThresholds(),
		reputation: reputation,
	}
}

func defaultRules() []Rule {
	return []Rule{
		newSPFFailRule(),
		newDMARCRejectRule(),
		newMissingReverseDNSRule(),
		newHELOMismatchRule(),
		newHELOSuspiciousRule(),
		newSenderNoMXRule(),
		newTooManyRecipientsRule(),
		newEmptyMailFromRule(),
		newBadIPRule(),
		newAllowedIPRule(),
	}
}

// Assess evaluates all rules against the given context and returns an assessment.
func (e *Engine) Assess(ctx *RuleContext) *SpamAssessment {
	if ctx == nil {
		return &SpamAssessment{Verdict: VerdictAccept}
	}
	if ctx.Reputation == nil && e.reputation != nil {
		ctx.Reputation = e.reputation
	}

	assessment := &SpamAssessment{
		RemoteIP:       ctx.RemoteIP,
		SenderDomain:   ctx.MailFromDomain,
		HELODomain:     ctx.HELODomain,
		SPFResult:      ctx.SPFResult,
		DKIMResult:     ctx.DKIMResult,
		DMARCResult:    ctx.DMARCResult,
		RecipientCount: ctx.RecipientCount,
		Verdict:        VerdictAccept,
	}

	var totalScore float64
	for _, rule := range e.rules {
		if !rule.Enabled() {
			continue
		}
		result := rule.Evaluate(ctx)
		if result.Match {
			assessment.MatchedRules = append(assessment.MatchedRules, *result)
			assessment.Reasons = append(assessment.Reasons, result.Reason)
			totalScore += result.Score
		}
	}

	assessment.Score = totalScore

	// Determine verdict based on thresholds.
	if totalScore >= e.thresholds.Reject {
		assessment.Verdict = VerdictReject
	} else if totalScore >= e.thresholds.Suspicious {
		assessment.Verdict = VerdictSuspicious
	}

	return assessment
}

// AssessFromContext builds assessment from a simpler set of inputs.
func (e *Engine) AssessFromContext(remoteIP net.IP, heloDomain, mailFromDomain, fromDomain,
	spfResult, dkimResult, dmarcResult, dmarcPolicy string,
	recipientCount int, hasReverseDNS bool) *SpamAssessment {
	ctx := &RuleContext{
		RemoteIP:       remoteIP,
		HELODomain:     heloDomain,
		MailFromDomain: mailFromDomain,
		FromDomain:     fromDomain,
		SPFResult:      spfResult,
		DKIMResult:     dkimResult,
		DMARCResult:    dmarcResult,
		DMARCPolicy:    dmarcPolicy,
		RecipientCount: recipientCount,
		HasReverseDNS:  hasReverseDNS,
		Reputation:     e.reputation,
	}
	return e.Assess(ctx)
}

// FormatSpamScoreHeader formats the X-Orvix-Spam-Score header value.
func FormatSpamScoreHeader(assessment *SpamAssessment) string {
	if assessment == nil {
		return ""
	}
	return fmt.Sprintf("%.1f", assessment.Score)
}

// FormatSpamVerdictHeader formats the X-Orvix-Spam-Verdict header value.
func FormatSpamVerdictHeader(assessment *SpamAssessment) string {
	if assessment == nil {
		return ""
	}
	return assessment.Verdict.String()
}

// FormatSpamReasons returns a semicolon-separated list of matched rule reasons.
func FormatSpamReasons(assessment *SpamAssessment) string {
	if assessment == nil || len(assessment.Reasons) == 0 {
		return ""
	}
	result := ""
	for i, r := range assessment.Reasons {
		if i > 0 {
			result += "; "
		}
		result += r
	}
	return result
}
