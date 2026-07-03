// Package rulertypes contains the shared contract between
// internal/coremail/smtp (the receiver / command handler)
// and internal/ruler (the rule engine). Pulling this
// interface into a tiny neutral package breaks the
// would-be import cycle so the SMTP package never has
// to import internal/ruler and vice versa.
//
// The interface is deliberately minimal: a RuleEngine is
// anything that evaluates an envelope / message and
// returns (matched, action, reason). MarkEnforced flips
// the engine's runtime_enforced flag so the admin status
// endpoint can surface it.
package rulertypes

import "context"

// Action is the rule decision returned to the SMTP
// receiver. The string is what the SMTP layer
// understands; the underlying engine may use whatever
// typed constant it likes internally.
type Action string

const (
	ActionAccept     Action = "accept"
	ActionReject     Action = "reject"
	ActionQuarantine Action = "quarantine"
	ActionTag        Action = "tag"
)

// Query carries the envelope + message context the
// rule engines consume. Keeping this narrow means
// engines can be tested independently of the SMTP
// machinery.
type Query struct {
	Sender    string
	Recipient string
	SourceIP  string
	Subject   string
	TenantID  uint
	Domain    string
	Headers   map[string]string
	MessageID string
}

// RuleEngine is the contract smtp.RuleEvaluator needs to
// implement (renamed here to avoid shadowing). It mirrors
// the smtp.RuleEvaluator surface so swapping the type
// alias is a single line per package.
type RuleEngine interface {
	Evaluate(ctx context.Context, q Query) (matched bool, action string, reason string)
	MarkEnforced()
}

// SetEnforcedFunc is the MarkEnforced compatible function
// type an engine exposes if it does not implement
// RuleEngine directly. Used by tests.
type SetEnforcedFunc func()
