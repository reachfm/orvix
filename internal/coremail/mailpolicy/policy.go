// Package mailpolicy is the canonical mailbox-level mail-access
// policy for Orvix. It is the SINGLE place where the
// internal_only / internal_external semantics are decided; every real
// delivery path (SMTP inbound/outbound, webmail send, JMAP
// submission, forwarding/vacation, relay delivery, queue
// retry/redelivery) consults this package instead of scattering
// independent conditionals across handlers and protocols.
//
// Canonical semantics:
//
//   - internal_only mailbox: may send to local Orvix recipients; may
//     receive from trusted/local authenticated Orvix senders; must NOT
//     send to external recipients; must NOT receive from external or
//     otherwise untrusted delivery paths.
//   - internal_external mailbox: may send and receive both local and
//     external mail, subject to all existing security, relay,
//     suppression, quota, and abuse controls.
//   - Local-to-local delivery remains allowed unless another existing
//     policy denies it.
//
// Security rules:
//
//   - A sender is NEVER classified as internal merely because the
//     MAIL FROM domain looks local. Only an authenticated mailbox
//     identity (SMTP AUTH session, webmail session, JMAP account)
//     counts as a trusted local sender.
//   - An unauthenticated remote sender using a forged local address
//     remains external.
//   - A platform/admin API caller is not automatically a mail sender.
//   - Policy-lookup infrastructure failure fails closed BEFORE any
//     network delivery (deny or defer, never deliver).
//   - A denied operation never consumes delivery quota as a
//     successful delivery (denials happen before enqueue on the
//     interactive paths and fail the queue entry permanently on the
//     worker paths).
//   - Denials produce stable typed errors and auditable
//     policy-denied events. Audit records never expose message
//     bodies, passwords, tokens, or credential hashes.
package mailpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Mode is a mail-access mode value. The canonical persisted values
// are inherit, internal_only, internal_external.
type Mode string

const (
	ModeInherit          Mode = "inherit"
	ModeInternalOnly     Mode = "internal_only"
	ModeInternalExternal Mode = "internal_external"
)

// IsConcrete reports whether m is a directly enforceable mode.
func (m Mode) IsConcrete() bool {
	return m == ModeInternalOnly || m == ModeInternalExternal
}

// EffectiveMode is the RESOLVED policy state. Configured is what was
// persisted (possibly "inherit"); Effective is what delivery paths
// enforce. The two must never be confused.
type EffectiveMode struct {
	Configured Mode
	Effective  Mode
}

// ResolveEffectiveMode is the single canonical resolution function:
//
//  1. A concrete mailbox mode (internal_only / internal_external) is
//     used as-is.
//  2. A mailbox mode of "inherit" (or empty, the pre-column legacy
//     value) resolves through the domain.
//  3. A domain value that is one of the two concrete values is used.
//  4. An EMPTY domain value is the established pre-column default and
//     resolves to internal_external — this is what keeps every
//     pre-existing installation behaving exactly as before.
//  5. Any other domain value is CORRUPT and fails closed to
//     internal_only, with a safe observable security event.
//
// corruptEvent is a non-nil, human-readable reason when a corrupt
// value was encountered (never containing the raw value).
func ResolveEffectiveMode(mailboxMode, domainMode string) (EffectiveMode, string) {
	mbox := Mode(strings.TrimSpace(mailboxMode))
	switch mbox {
	case ModeInternalOnly, ModeInternalExternal:
		return EffectiveMode{Configured: mbox, Effective: mbox}, ""
	}
	// Empty mailbox value is the legacy "no per-mailbox policy" state.
	if mbox == "" {
		mbox = ModeInherit
	}

	dom := Mode(strings.TrimSpace(domainMode))
	switch dom {
	case ModeInternalOnly:
		return EffectiveMode{Configured: mbox, Effective: ModeInternalOnly}, ""
	case ModeInternalExternal:
		return EffectiveMode{Configured: mbox, Effective: ModeInternalExternal}, ""
	case "":
		// Pre-column domain rows: established default, unchanged.
		return EffectiveMode{Configured: mbox, Effective: ModeInternalExternal}, ""
	default:
		// Corrupt/unknown domain value: fail closed to internal_only.
		return EffectiveMode{Configured: mbox, Effective: ModeInternalOnly}, "corrupt domain mail_access_mode; failed closed to internal_only"
	}
}

// ── Stable typed errors ────────────────────────────────────────────

var (
	// ErrPolicyDenied is the stable denial contract. The message is a
	// safe, reason-carrying string with no message content.
	ErrPolicyDenied = errors.New("mail access policy denied")
	// ErrPolicyUnavailable is returned when the policy cannot be
	// evaluated (store failure). Callers MUST fail closed (defer or
	// deny) — never deliver on unknown policy state.
	ErrPolicyUnavailable = errors.New("mail access policy unavailable")
	// ErrSenderUnknown reports that the sender address is not a local
	// mailbox, so no sender policy can be established.
	ErrSenderUnknown = errors.New("sender is not a local mailbox")
)

// DeniedReason is the stable machine-readable denial category.
type DeniedReason string

const (
	ReasonExternalRecipient DeniedReason = "external_recipient_not_permitted"
	ReasonExternalSender    DeniedReason = "external_sender_not_permitted"
	ReasonUntrustedSender   DeniedReason = "untrusted_sender_not_permitted"
)

// Decision is the outcome of a policy check. Allowed=true means the
// operation may proceed. Denied carries a stable Reason. Unavailable
// means the policy could not be evaluated and the caller must fail
// closed.
type Decision struct {
	Allowed     bool
	Denied      bool
	Unavailable bool
	Reason      DeniedReason
	Detail      string // safe detail, never message content or secrets
}

func allow() Decision { return Decision{Allowed: true} }
func deny(reason DeniedReason, detail string) Decision {
	return Decision{Denied: true, Reason: reason, Detail: detail}
}
func unavailable(detail string) Decision {
	return Decision{Unavailable: true, Detail: detail}
}

// ── Store ──────────────────────────────────────────────────────────

// SenderIdentity is the resolved authenticated-sender context.
type SenderIdentity struct {
	MailboxID      uint
	TenantID       uint
	DomainID       uint
	MailboxEmail   string
	EffectiveMode  Mode
	ConfiguredMode Mode
}

// Store resolves the policy inputs. Implementations must be safe for
// concurrent use (the SMTP hot path calls them once per RCPT TO).
type Store interface {
	// SenderIdentity resolves an authenticated sender mailbox and its
	// effective mode. ErrSenderUnknown when the address is not a local
	// mailbox. Any other error is an infrastructure failure and the
	// caller must fail closed.
	SenderIdentity(ctx context.Context, mailboxEmail string) (SenderIdentity, error)
	// RecipientEffectiveMode resolves the effective mode of the FINAL
	// local recipient target(s) for an address (mailbox, forwarder,
	// alias, catchall). ErrRecipientUnknown when the address has no
	// local mailbox target. Any other error is an infrastructure
	// failure and the caller must fail closed.
	RecipientEffectiveMode(ctx context.Context, address string) (EffectiveMode, error)
	// RecipientIsLocal reports whether an address resolves to a
	// deliverable local recipient (mailbox, forwarder, alias,
	// catchall). Errors are infrastructure failures; callers fail
	// closed.
	RecipientIsLocal(ctx context.Context, address string) (bool, error)
	// IsLocalDomain reports whether the domain is hosted on this
	// platform. Errors are infrastructure failures; callers fail
	// closed.
	IsLocalDomain(ctx context.Context, domain string) (bool, error)
}

// ErrRecipientUnknown reports that an address has no local mailbox
// target.
var ErrRecipientUnknown = errors.New("recipient is not a local mailbox")

// ── Event sink ─────────────────────────────────────────────────────

// EventSink receives safe, observable policy events. Implementations
// (zap logger + observability history in the runtime, a recorder in
// tests) must NEVER receive message bodies, passwords, tokens, or
// hashes — the policy layer itself only passes addresses and reasons.
type EventSink interface {
	// PolicyDenied records a denial. kind is the delivery path
	// ("smtp_inbound", "smtp_outbound", "webmail", "jmap",
	// "forwarding", "vacation", "delivery_worker", "relay").
	// sender/recipient are bare addresses (never message content).
	PolicyDenied(ctx context.Context, kind, sender, recipient string, reason DeniedReason, detail string)
	// PolicyCorrupt records a fail-closed resolution on corrupt
	// persisted data. It never carries the raw corrupt value.
	PolicyCorrupt(ctx context.Context, kind string, detail string)
	// PolicyUnavailable records a fail-closed infrastructure failure.
	PolicyUnavailable(ctx context.Context, kind, detail string)
}

// NopSink discards events (the safe default for isolated consumers).
type NopSink struct{}

func (NopSink) PolicyDenied(context.Context, string, string, string, DeniedReason, string) {}
func (NopSink) PolicyCorrupt(context.Context, string, string)                              {}
func (NopSink) PolicyUnavailable(context.Context, string, string)                          {}

// ── Policy ─────────────────────────────────────────────────────────

// Policy is the canonical mail-access policy engine. It is stateless
// apart from its Store and EventSink and is safe for concurrent use.
type Policy struct {
	store Store
	sink  EventSink
	clock func() time.Time
}

func New(store Store, sink EventSink) *Policy {
	if sink == nil {
		sink = NopSink{}
	}
	return &Policy{store: store, sink: sink, clock: func() time.Time { return time.Now().UTC() }}
}

// CheckOutbound decides whether an authenticated sender mailbox may
// send to the given recipients. It is used by SMTP submission, webmail
// send, JMAP Submission/set, forwarding/vacation, and the delivery
// worker (relay/retry).
//
//   - internal_external: allow (subject to the caller's other
//     controls).
//   - internal_only: every recipient must be a local recipient; one
//     external recipient denies the whole send.
//   - Sender identity unknown: deny (an authenticated session whose
//     mailbox vanished cannot be granted external-send rights).
//   - Store failure: unavailable — the caller defers/fails closed and
//     never delivers.
func (p *Policy) CheckOutbound(ctx context.Context, kind, senderMailbox string, recipients []string) Decision {
	if len(recipients) == 0 {
		return allow()
	}
	ident, err := p.store.SenderIdentity(ctx, senderMailbox)
	if err != nil {
		if errors.Is(err, ErrSenderUnknown) {
			p.sink.PolicyDenied(ctx, kind, senderMailbox, strings.Join(recipients, ","), ReasonUntrustedSender, "sender identity not established")
			return deny(ReasonUntrustedSender, "sender identity not established")
		}
		p.sink.PolicyUnavailable(ctx, kind, "sender identity lookup failed")
		return unavailable("sender identity lookup failed")
	}

	if ident.EffectiveMode == ModeInternalOnly {
		for _, rcpt := range recipients {
			rcpt = strings.TrimSpace(rcpt)
			if rcpt == "" {
				continue
			}
			isLocal, lerr := p.store.RecipientIsLocal(ctx, rcpt)
			if lerr != nil {
				p.sink.PolicyUnavailable(ctx, kind, "recipient locality lookup failed")
				return unavailable("recipient locality lookup failed")
			}
			if !isLocal {
				p.sink.PolicyDenied(ctx, kind, senderMailbox, rcpt, ReasonExternalRecipient, "internal-only sender may not send to external recipients")
				return deny(ReasonExternalRecipient, "internal-only sender may not send to external recipients")
			}
		}
	}
	return allow()
}

// Sender describes the sender side of an inbound delivery. Only an
// authenticated mailbox identity counts as a trusted local sender.
type Sender struct {
	// Authenticated reports whether the delivery path authenticated
	// the sender (SMTP AUTH session, webmail session, JMAP account).
	Authenticated bool
	// MailboxEmail is the authenticated mailbox address. It is
	// ignored (and must be empty) when Authenticated is false.
	MailboxEmail string
}

// CheckInboundRecipient decides whether a message may be delivered to
// a local recipient. It is used by the SMTP inbound RCPT path.
//
//   - Recipient effective internal_external: allow (external senders
//     may deliver, subject to the caller's other controls).
//   - Recipient effective internal_only: allow ONLY when the sender is
//     a trusted local authenticated mailbox. A remote unauthenticated
//     sender with a forged local MAIL FROM remains external and is
//     denied.
//   - Recipient absent (no local mailbox target): allow — the
//     caller's own recipient validation rejects unknown users with
//     the established 5.1.1 contract. This preserves pre-existing
//     behavior for nonexistent local addresses.
//   - Store failure: unavailable — the caller fails closed with a
//     temporary failure.
func (p *Policy) CheckInboundRecipient(ctx context.Context, kind string, sender Sender, recipient string) Decision {
	eff, err := p.store.RecipientEffectiveMode(ctx, recipient)
	if err != nil {
		if errors.Is(err, ErrRecipientUnknown) {
			// Not a local mailbox target: no mailbox-level policy
			// applies; the caller's recipient validation owns this
			// address.
			return allow()
		}
		p.sink.PolicyUnavailable(ctx, kind, "recipient policy lookup failed")
		return unavailable("recipient policy lookup failed")
	}

	if eff.Effective == ModeInternalOnly {
		if !sender.Authenticated || strings.TrimSpace(sender.MailboxEmail) == "" {
			p.sink.PolicyDenied(ctx, kind, sender.MailboxEmail, recipient, ReasonExternalSender, "external sender may not deliver to an internal-only recipient")
			return deny(ReasonExternalSender, "external sender may not deliver to an internal-only recipient")
		}
		// The sender must actually BE a local mailbox — never trust
		// the MAIL FROM domain.
		ident, serr := p.store.SenderIdentity(ctx, sender.MailboxEmail)
		if serr != nil {
			if errors.Is(serr, ErrSenderUnknown) {
				p.sink.PolicyDenied(ctx, kind, sender.MailboxEmail, recipient, ReasonUntrustedSender, "authenticated sender is not a local mailbox")
				return deny(ReasonUntrustedSender, "authenticated sender is not a local mailbox")
			}
			p.sink.PolicyUnavailable(ctx, kind, "sender identity lookup failed")
			return unavailable("sender identity lookup failed")
		}
		_ = ident
	}
	return allow()
}

// fmt is kept for error wrapping helpers below.
var _ = fmt.Sprintf
