package antispam

import "net"

// Verdict represents the final spam assessment outcome.
type Verdict int

const (
	VerdictAccept  Verdict = iota // Clean message, deliver normally
	VerdictSuspicious             // Suspicious but deliver (adds headers)
	VerdictReject                 // Reject the message
)

func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "accept"
	case VerdictSuspicious:
		return "suspicious"
	case VerdictReject:
		return "reject"
	default:
		return "accept"
	}
}

// RuleResult is the outcome of a single rule evaluation.
type RuleResult struct {
	Name   string
	Score  float64
	Match  bool
	Reason string
}

// SpamAssessment is the unified output of the anti-spam engine.
type SpamAssessment struct {
	Score         float64
	Verdict       Verdict
	Reasons       []string
	MatchedRules  []RuleResult
	RemoteIP      net.IP
	SenderDomain  string
	HELODomain    string
	SPFResult     string
	DKIMResult    string
	DMARCResult   string
	RecipientCount int
}

// Rule defines a single spam evaluation rule.
type Rule interface {
	Name() string
	Weight() float64
	Enabled() bool
	Evaluate(ctx *RuleContext) *RuleResult
}

// RuleContext carries evaluation inputs for a single rule evaluation.
type RuleContext struct {
	RemoteIP        net.IP
	HELODomain      string
	MailFromDomain  string
	FromDomain      string
	SPFResult       string
	DKIMResult      string
	DMARCResult     string
	DMARCPolicy     string
	RecipientCount  int
	Reputation      ReputationProvider
	HasReverseDNS   bool
	ReverseDNSName  string
}

// ReputationProvider provides IP and domain reputation data.
type ReputationProvider interface {
	IsBadIP(ip net.IP) bool
	IsAllowedIP(ip net.IP) bool
	SenderDomainReputation(domain string) DomainReputation
}

// DomainReputation represents known information about a sender domain.
type DomainReputation struct {
	KnownGood   bool
	KnownBad    bool
	HasMX       bool
	MXHosts     []string
	Confidence  float64 // 0.0 to 1.0
}

// Thresholds define the score boundaries for verdicts.
type Thresholds struct {
	Suspicious float64 // score >= this -> suspicious
	Reject     float64 // score >= this -> reject
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		Suspicious: 3.0,
		Reject:     8.0,
	}
}
