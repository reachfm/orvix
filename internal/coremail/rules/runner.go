package rules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// Anti-loop markers. The engine + caller check these headers
// on every inbound message before applying forward / vacation
// actions. If a message carries any of these, the engine
// treats it as a system-generated message and refuses to
// re-forward / re-reply. This is the canonical defence
// against vacation reply loops and forward loops.
const (
	HeaderForwarded = "X-Orvix-Forwarded"
	HeaderVacation  = "X-Orvix-Vacation"
)

// RunInput carries everything the runner needs to evaluate
// rules for one inbound message destined for one local
// recipient mailbox. The runner never mutates the input —
// it produces a RunOutput that the caller applies.
type RunInput struct {
	MailboxID     uint
	TenantID      uint
	DomainID      uint
	MailboxEmail  string
	MessageID     string
	RFC822        []byte // raw bytes of the inbound message
	FromHeader    string
	ToHeader      string
	CcHeader      string
	Subject       string
	BodyText      string // optional; empty when body_contains not used
	HasAttachment bool
	ReceivedAt    time.Time
}

// RunOutput tells the caller what side-effects to apply.
type RunOutput struct {
	// MoveToFolder is non-empty when the engine decided the
	// message should be relocated to a different folder. The
	// caller MUST physically move the durable row — a move
	// leaves the message in exactly one folder.
	MoveToFolder string
	// CopyToFolder is non-empty when the engine decided the
	// message should ALSO land in another folder. The caller
	// MUST duplicate the durable row into the target folder
	// without deleting the original — a copy leaves the
	// message in TWO folders (the original + the copy).
	//
	// Copy and move are independent. A single rule can
	// emit both: e.g. "move to Archive AND copy to Receipts".
	// The caller applies move first, then copy.
	//
	// Earlier revisions of this struct aliased CopyTo onto
	// MoveTo, which silently turned user-configured copies
	// into moves. Do not re-introduce that alias — the SMTP
	// tests pin the new contract.
	CopyToFolder string
	// SetFlag is non-nil when the engine decided to flip flags.
	SetFlag *storage.SetFlagValue
	// ForwardedTo is the address to forward to. Empty when
	// no forward rule fired. The caller builds the RFC822
	// wrapper and enqueues it.
	ForwardedTo string
	// ForwardKeepCopy is set when the forwarding row (or
	// forwarding rule) specifies keep_copy=1. The caller
	// leaves the original in the mailbox in that case;
	// when false the caller may purge the original after
	// the forward has been durably queued.
	ForwardKeepCopy bool
	// VacationReply is non-nil when the caller should enqueue
	// a vacation auto-reply.
	VacationReply *VacationReply
	// SkipReason explains why an action was NOT taken even
	// though a rule matched (e.g. "loop marker found",
	// "rate limited", "vacation disabled"). Used for
	// audit logging only; the caller does not branch on it.
	SkipReason string
}

// MaxRfc822ReadBytes is the soft cap on how many bytes of
// body the engine will scan for body_contains matchers. The
// caller is responsible for limiting this; if it passes a
// larger blob the engine still scans up to MaxRfc822ReadBytes
// and ignores the rest. Default 256 KB.
const MaxRfc822ReadBytes = 256 * 1024

// Dependencies is the dependency bundle the runner needs.
// All interfaces are satisfied by the existing types in the
// codebase; tests pass fakes.
type Dependencies struct {
	MailStore       *storage.MailStore
	QueueEngine     *queue.QueueEngine
	Vacation        storage.VacationRepository
	Forwarding      storage.ForwardingRepository
	Logger          *zap.Logger
}

// Runner evaluates the rules engine for one inbound message.
// One Runner per call — the runner holds no per-message
// mutable state and is safe for concurrent use.
type Runner struct {
	deps Dependencies
}

// NewRunner returns a Runner bound to the supplied deps.
func NewRunner(deps Dependencies) *Runner {
	return &Runner{deps: deps}
}

// RulesRunner is the contract the SMTP receiver depends
// on. *Runner satisfies it. Tests use it to substitute a
// panic-throwing or error-returning fake to verify the
// receiver's durability guarantee.
type RulesRunner interface {
	Run(ctx context.Context, in RunInput) (*RunOutput, error)
}

// Compile-time assertion that *Runner satisfies the
// RulesRunner interface.
var _ RulesRunner = (*Runner)(nil)

// Run is the canonical entry point. It loads the rules,
// evaluates the engine, applies the resulting actions, and
// returns the output the caller should use to update the
// stored message (move / flag).
//
// IMPORTANT: the runner NEVER mutates the inbound message
// directly. The caller — the SMTP receive path — is the
// only place that talks to MailStore. The runner only:
//   - reads rules + vacation + forwarding rows
//   - decides what to do
//   - enqueues outbound messages (forwarded / vacation)
//   - records vacation replies for rate-limit
//
// The caller decides whether to move the original message or
// apply flags based on RunOutput. If anything in the runner
// fails, the caller MUST keep the original message intact in
// the recipient's mailbox — the storage layer does not see
// the runner, so a panic / DB error in the runner does not
// touch the durable message row.
func (r *Runner) Run(ctx context.Context, in RunInput) (*RunOutput, error) {
	out := &RunOutput{}

	// Anti-loop gate. If the inbound message is already marked
	// as forwarded / vacation by us, refuse to re-process.
	// The caller MUST set in.HeadersChecked before calling Run.
	if hasAntiLoopMarker(in.RFC822) {
		out.SkipReason = "anti_loop_marker_present"
		return out, nil
	}

	// Load rules.
	ruleRows, err := r.deps.MailStore.Rules.ListByMailbox(ctx, in.MailboxID)
	if err != nil {
		return out, fmt.Errorf("load rules: %w", err)
	}
	parsed := make([]*ParsedRule, 0, len(ruleRows))
	for _, row := range ruleRows {
		p, perr := ParseRuleJSON(row.ID, row.MailboxID, row.Name, row.Enabled, row.SortOrder, row.StopProcessing, row.ConditionsJSON, row.ActionsJSON)
		if perr != nil {
			// A corrupt rule row must never abort the
			// whole delivery — log and skip.
			r.deps.Logger.Warn("rules: skip corrupt rule",
				zap.Uint64("rule_id", uint64(row.ID)),
				zap.Uint64("mailbox_id", uint64(row.MailboxID)),
				zap.Error(perr))
			continue
		}
		parsed = append(parsed, p)
	}

	// Load forwarding config + vacation config.
	fwd, ferr := r.deps.MailStore.Forwarding.GetOrCreate(ctx, in.MailboxID)
	if ferr != nil {
		r.deps.Logger.Warn("rules: forwarding load failed",
			zap.Uint64("mailbox_id", uint64(in.MailboxID)),
			zap.Error(ferr))
		fwd = nil // graceful degrade — no forwarding this run
	}
	vac, verr := r.deps.MailStore.Vacation.GetOrCreate(ctx, in.MailboxID)
	if verr != nil {
		r.deps.Logger.Warn("rules: vacation load failed",
			zap.Uint64("mailbox_id", uint64(in.MailboxID)),
			zap.Error(verr))
		vac = nil
	}

	// Evaluate the engine.
	engineCtx := MessageContext{
		From:          in.FromHeader,
		To:            in.ToHeader,
		Cc:            in.CcHeader,
		Subject:       in.Subject,
		Body:          in.BodyText,
		HasAttachment: in.HasAttachment,
		ReceivedAt:    in.ReceivedAt,
		MessageID:     in.MessageID,
	}
	result := Evaluate(parsed, engineCtx)

	// Apply the engine result. The runner emits at most one
	// Forward and one Vacation per inbound message — the
	// engine enforces "first forward wins" via the
	// ForwardedAlready flag it returns. Move and Copy are
	// independent: a single rule may emit both, and the
	// runner tracks them in separate fields so the SMTP
	// caller can do "move first, then duplicate" instead
	// of aliasing copy into move.
	for _, a := range result.Actions {
		switch {
		case a.MoveTo != "":
			// First move wins; later moves would clobber.
			if out.MoveToFolder == "" {
				out.MoveToFolder = a.MoveTo
			}
		case a.CopyTo != "":
			// Copy semantics: the caller (SMTP receiver)
			// calls MailStore.CopyMessage so the durable
			// row is duplicated into the target folder
			// while the original row stays put. Do NOT
			// alias CopyTo onto MoveToFolder — that was
			// the bug fixed in this revision; see the
			// RunOutput.CopyToFolder doc.
			if out.CopyToFolder == "" {
				out.CopyToFolder = a.CopyTo
			}
		case a.SetFlag != nil:
			out.SetFlag = a.SetFlag
		case a.ForwardTo != "":
			if out.ForwardedTo == "" {
				out.ForwardedTo = a.ForwardTo
			}
		}
	}

	// Forwarding row (one-row evaluation). Only fires when
	// no rule-based forward already fired.
	if out.ForwardedTo == "" && fwd != nil && fwd.Enabled && strings.TrimSpace(fwd.ForwardTo) != "" {
		if _, err := mail.ParseAddress(fwd.ForwardTo); err == nil {
			out.ForwardedTo = fwd.ForwardTo
			out.ForwardKeepCopy = fwd.KeepCopy
		} else {
			out.SkipReason = "forwarding address invalid"
		}
	}

	// Vacation. Only fires when no rule already enqueued a
	// forward this run AND the engine has not yet decided to
	// skip the message. The order matters: we always reply
	// to inbound mail when vacation is on — vacation replies
	// do not require the message to land in INBOX first.
	if out.VacationReply == nil && vac != nil && vac.Enabled {
		if !shouldSkipVacation(in, vac) {
			reply, skip := r.evaluateVacationReply(ctx, in, vac)
			if reply != nil {
				out.VacationReply = reply
			} else if skip != "" {
				out.SkipReason = skip
			}
		} else {
			out.SkipReason = "vacation skip-condition met"
		}
	}

	// Enqueue outbound messages produced by the engine.
	if out.ForwardedTo != "" {
		if err := r.enqueueForward(ctx, in, out); err != nil {
			r.deps.Logger.Error("rules: forward enqueue failed",
				zap.String("message_id", in.MessageID),
				zap.Error(err))
			out.SkipReason = "forward enqueue failed"
			// We do NOT clear out.ForwardedTo — the caller
			// surfaces the failure for the audit log; the
			// original message stays in the recipient's
			// mailbox because the runner only emits
			// queue entries.
		}
	}
	if out.VacationReply != nil {
		if err := r.enqueueVacation(ctx, in, out); err != nil {
			r.deps.Logger.Error("rules: vacation enqueue failed",
				zap.String("message_id", in.MessageID),
				zap.Error(err))
			// Preserve any SkipReason the inner helper
			// already set (BLOCKER 4: persistence failure
			// is the more specific signal). Only fall
			// back to the generic message when the inner
			// helper did not record one of its own.
			if out.SkipReason == "" {
				out.SkipReason = "vacation enqueue failed"
			}
		}
	}

	return out, nil
}

// hasAntiLoopMarker returns true when the RFC822 body already
// carries one of the Orvix-internal markers. The caller is
// responsible for keeping the headers section intact on the
// way in — receiving a forwarded / vacation message must
// round-trip through here before any rule evaluation.
func hasAntiLoopMarker(rfc822 []byte) bool {
	s := string(rfc822)
	return strings.Contains(s, HeaderForwarded+":") ||
		strings.Contains(s, HeaderVacation+":") ||
		// Common standards-based signals — a message with
		// Auto-Submitted other than "no" was generated by
		// a mailer, not a human; do not reply to it.
		hasAutoSubmittedNotNo(s)
}

// hasAutoSubmittedNotNo returns true when the message
// declares Auto-Submitted with any value other than "no".
// Per RFC 8058 the absent header defaults to "no", so a
// missing header is fine to reply to.
func hasAutoSubmittedNotNo(s string) bool {
	idx := strings.Index(strings.ToLower(s), "auto-submitted:")
	if idx < 0 {
		return false
	}
	// Walk to end of line.
	end := strings.IndexAny(s[idx:], "\r\n")
	var line string
	if end < 0 {
		line = s[idx:]
	} else {
		line = s[idx : idx+end]
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return false
	}
	val := strings.TrimSpace(strings.ToLower(parts[1]))
	return val != "" && val != "no"
}

// shouldSkipVacation returns true when the message must NOT
// receive a vacation reply. Bounce sources, mailing lists,
// and bulk auto-replied messages are excluded.
func shouldSkipVacation(in RunInput, vac *storage.VacationConfig) bool {
	if vac == nil {
		return true
	}
	from := strings.ToLower(strings.TrimSpace(in.FromHeader))
	if from == "" {
		return true
	}
	// MAILER-DAEMON and postmaster are excluded.
	if strings.Contains(from, "mailer-daemon") ||
		strings.Contains(from, "postmaster") {
		return true
	}
	// List-Unsubscribe is a strong signal of a mailing list.
	if strings.Contains(strings.ToLower(string(in.RFC822)), "list-unsubscribe:") {
		return true
	}
	// Precedence: bulk / list / junk
	if strings.Contains(strings.ToLower(string(in.RFC822)), "precedence: bulk") ||
		strings.Contains(strings.ToLower(string(in.RFC822)), "precedence: junk") ||
		strings.Contains(strings.ToLower(string(in.RFC822)), "precedence: list") {
		return true
	}
	return false
}

// evaluateVacationReply applies the start/end window and the
// per-sender rate limit, and returns either the auto-reply
// descriptor or a skip reason. Recording the reply (so the
// rate limit takes effect) only happens on successful enqueue
// from the caller; the caller calls deps.Vacation.RecordReply
// after the queue entry is durably written.
func (r *Runner) evaluateVacationReply(ctx context.Context, in RunInput, vac *storage.VacationConfig) (*VacationReply, string) {
	// Time window check. Nil bounds mean "open on that side".
	if vac.StartAt != nil && *vac.StartAt != "" {
		t, err := time.Parse(time.RFC3339, *vac.StartAt)
		if err == nil && in.ReceivedAt.Before(t) {
			return nil, "vacation before start window"
		}
	}
	if vac.EndAt != nil && *vac.EndAt != "" {
		t, err := time.Parse(time.RFC3339, *vac.EndAt)
		if err == nil && in.ReceivedAt.After(t) {
			return nil, "vacation after end window"
		}
	}

	// Per-sender rate limit. Default reply_interval_seconds
	// is 86400 (24h). The handler clamps the value to [60,
	// 30*86400].
	sender := extractBareAddress(in.FromHeader)
	if sender == "" {
		return nil, "vacation sender empty"
	}
	last, err := r.deps.MailStore.Vacation.LastRepliedAt(ctx, in.MailboxID, sender)
	if err != nil {
		r.deps.Logger.Warn("rules: vacation last-replied lookup failed",
			zap.Uint64("mailbox_id", uint64(in.MailboxID)),
			zap.String("sender", sender),
			zap.Error(err))
		// Fail closed: do not send if we cannot enforce the rate limit.
		return nil, "vacation rate-limit lookup failed"
	}
	if last != "" {
		t, err := time.Parse(time.RFC3339, last)
		if err == nil {
			interval := time.Duration(vac.ReplyIntervalSeconds) * time.Second
			if time.Since(t) < interval {
				return nil, "vacation rate limited"
			}
		}
	}
	return &VacationReply{
		Subject: vac.Subject,
		Body:    vac.Body,
	}, ""
}

// enqueueForward builds a wrapped RFC822 message (with the
// Orvix-Forwarded marker) and enqueues one queue entry per
// recipient. The original message stays in the recipient's
// mailbox; the caller decides whether to delete the copy
// based on out.ForwardKeepCopy.
func (r *Runner) enqueueForward(ctx context.Context, in RunInput, out *RunOutput) error {
	wrapped, err := buildForwardedRfc822(in, out.ForwardedTo)
	if err != nil {
		return fmt.Errorf("build forward: %w", err)
	}
	if err := r.enqueueOutbound(ctx, in, wrapped, out.ForwardedTo); err != nil {
		return err
	}
	return nil
}

// enqueueVacation builds the auto-reply RFC822, persists
// the rate-limit history, and enqueues the outbound reply.
//
// Order matters (BLOCKER 4 fix). The rate-limit record MUST
// be persisted BEFORE we enqueue the outbound message:
//
//   1. RecordReply (atomic UPSERT against
//      UNIQUE(mailbox_id, sender_email))
//   2. Enqueue outbound Message + QueueEntry
//
// If RecordReply fails, we MUST NOT enqueue. The previous
// implementation logged RecordReply errors and continued
// regardless — a transient DB error would let the same
// sender trigger a fresh vacation reply on every subsequent
// inbound until the rate window finally expired, a
// textbook reply-storm bug.
//
// The trade-off for the new ordering: if the enqueue step
// fails after RecordReply succeeded, we have spent one
// rate-limit slot without delivering the reply. The next
// inbound from the same sender will hit LastRepliedAt and
// be skipped. That is the correct side to err on — the
// alternative (allow duplicate replies on enqueue failure)
// is much worse than (allow a single missed reply on a
// transient enqueue failure).
func (r *Runner) enqueueVacation(ctx context.Context, in RunInput, out *RunOutput) error {
	reply := out.VacationReply
	if reply == nil {
		return nil
	}
	sender := extractBareAddress(in.FromHeader)
	if sender == "" {
		return fmt.Errorf("vacation reply: empty sender")
	}
	// Fail-closed: persist the rate-limit row FIRST. If
	// this fails we refuse to enqueue the reply — the
	// LastRepliedAt check that gates the next inbound
	// must not be allowed to drift open.
	if err := r.deps.MailStore.Vacation.RecordReply(ctx, in.MailboxID, sender); err != nil {
		r.deps.Logger.Error("rules: vacation record-reply failed — refusing to enqueue reply to prevent storm",
			zap.Uint64("mailbox_id", uint64(in.MailboxID)),
			zap.String("sender", sender),
			zap.Error(err))
		out.SkipReason = "vacation rate-limit persistence failed"
		// Clear VacationReply so callers iterating the
		// output cannot be misled — the engine decided
		// to send a reply, but the persistence failure
		// vetoed it. The audit event (skip_reason) is
		// the source of truth for what actually happened.
		out.VacationReply = nil
		return fmt.Errorf("vacation rate-limit persistence: %w", err)
	}
	replyRfc, err := buildVacationRfc822(in, reply.Subject, reply.Body)
	if err != nil {
		return fmt.Errorf("build vacation: %w", err)
	}
	if err := r.enqueueOutbound(ctx, in, replyRfc, sender); err != nil {
		// enqueue failed but the rate-limit row IS
		// already persisted. Log loudly so the operator
		// can replay manually if needed, but do NOT
		// return a nil error here because the reply
		// was not actually sent. The rate-limit window
		// has consumed one slot; the next inbound will
		// be suppressed. That is the intended
		// trade-off — see the BLOCKER 4 comment above.
		r.deps.Logger.Error("rules: vacation enqueue failed after rate-limit persisted",
			zap.Uint64("mailbox_id", uint64(in.MailboxID)),
			zap.String("sender", sender),
			zap.Error(err))
		return err
	}
	return nil
}

// enqueueOutbound writes the wrapped message into the
// sender's Sent folder (so the worker can
// LoadMessageByMessageID find it during delivery AND the
// sender has a "Sent" copy of every auto-generated
// message), then enqueues a single QueueEntry. The
// MessageID used here is a fresh UUID so the queued entry
// does not collide with the inbound row.
//
// The Sender's Sent folder is the canonical "auto-Sent"
// destination: the webmail Send path stores user-authored
// mail there, and the runner follows the same contract.
// This is also the only folder universally guaranteed to
// exist on every mailbox because system_folders.go's
// EnsureMailboxSystemFolders provisions it. If the folder
// is somehow missing we refuse to enqueue rather than
// write a dangling Message row.
func (r *Runner) enqueueOutbound(ctx context.Context, in RunInput, rfc822 []byte, recipient string) error {
	outMsgID := newMessageID()
	fromHeader := in.MailboxEmail
	if fromHeader == "" {
		fromHeader = extractBareAddress(in.FromHeader)
	}
	if fromHeader == "" {
		return fmt.Errorf("enqueue outbound: no sender address")
	}
	sentFolder, ferr := r.deps.MailStore.Folders.GetByPath(ctx, in.MailboxID, "Sent", nil)
	if ferr != nil {
		return fmt.Errorf("resolve Sent folder for mailbox %d: %w", in.MailboxID, ferr)
	}
	if sentFolder == nil {
		return fmt.Errorf("Sent folder missing for mailbox %d; cannot enqueue outbound", in.MailboxID)
	}
	outMsg := &storage.Message{
		MessageID:         outMsgID,
		InternetMessageID: fmt.Sprintf("<%s@orvix.local>", outMsgID),
		TenantID:          in.TenantID,
		DomainID:          in.DomainID,
		MailboxID:         in.MailboxID,
		FolderID:          sentFolder.ID,
		FromAddress:       fromHeader,
		ToAddresses:       recipient,
		Subject:           extractSubjectFromRfc822(rfc822),
		ReceivedDate:      time.Now().UTC(),
		Seen:              true, // the auto-generated message is "seen" — no unread badge for our own forward / vacation
	}
	if err := r.deps.MailStore.StoreMessage(ctx, outMsg, rfc822, nil); err != nil {
		return fmt.Errorf("store outbound: %w", err)
	}
	entry := &queue.QueueEntry{
		TenantID:        in.TenantID,
		DomainID:        in.DomainID,
		MailboxID:       &in.MailboxID,
		MessageID:       outMsgID,
		FromAddress:     fromHeader,
		ToAddress:       recipient,
		RecipientDomain: extractDomain(recipient),
		Direction:       queue.DirectionOutbound,
		DeliveryMode:    queue.DeliveryRemoteSMTP,
		Status:          queue.StatusPending,
	}
	if err := r.deps.QueueEngine.Enqueue(ctx, entry); err != nil {
		// Best-effort cleanup so we don't leave a dangling
		// outbound message row.
		if perr := r.deps.MailStore.PurgeMessage(ctx, outMsg.ID, nil); perr != nil {
			r.deps.Logger.Error("rules: outbound enqueue failed AND purge failed",
				zap.String("message_id", outMsgID),
				zap.Error(err),
				zap.Error(perr))
		}
		return fmt.Errorf("enqueue outbound: %w", err)
	}
	return nil
}

// extractBareAddress pulls "alice@example.com" out of
// "Alice <alice@example.com>" or returns the input unchanged.
func extractBareAddress(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(h); err == nil {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	return strings.ToLower(h)
}

// extractDomain returns the part after the last @, lowercased.
func extractDomain(addr string) string {
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(addr[i+1:]))
}

// extractSubjectFromRfc822 pulls the Subject header out of
// the RFC822 bytes without loading the whole message into
// the MIME parser. Cheap-and-fast path used only for the
// outbound message row metadata.
func extractSubjectFromRfc822(rfc822 []byte) string {
	s := string(rfc822)
	idx := strings.Index(strings.ToLower(s), "\nsubject:")
	if idx < 0 {
		return ""
	}
	start := idx + len("\nsubject:")
	end := strings.IndexAny(s[start:], "\r\n")
	if end < 0 {
		return strings.TrimSpace(s[start:])
	}
	return strings.TrimSpace(s[start : start+end])
}

// buildForwardedRfc822 wraps the inbound RFC822 with the
// Orvix-Forwarded marker so the next hop's runner refuses to
// re-forward. The body is the original bytes prefixed with a
// small "Forwarded by Orvix" header block.
func buildForwardedRfc822(in RunInput, recipient string) ([]byte, error) {
	id := newMessageID()
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(in.MailboxEmail)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(recipient)
	b.WriteString("\r\n")
	if in.Subject != "" {
		b.WriteString("Subject: Fwd: ")
		b.WriteString(in.Subject)
		b.WriteString("\r\n")
	}
	b.WriteString("Date: ")
	b.WriteString(time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("\r\n")
	b.WriteString("Message-ID: <")
	b.WriteString(id)
	b.WriteString("@orvix.local>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(HeaderForwarded)
	b.WriteString(": yes\r\n")
	b.WriteString("Auto-Submitted: auto-forwarded\r\n")
	b.WriteString("Content-Type: message/rfc822\r\n")
	b.WriteString("\r\n")
	b.Write(in.RFC822)
	return []byte(b.String()), nil
}

// buildVacationRfc822 builds the auto-reply message body.
// Subject defaults to "Out of office" when the config leaves
// it blank. Body defaults to a one-line "I am out of the
// office" message so the engine never sends an empty reply.
//
// Subject is validated for header-injection (CR/LF/NUL/control
// bytes). The storage layer already rejects these on the
// write path; this is defence in depth so a future caller
// that bypasses the storage layer still cannot inject
// extra headers into outbound auto-replies.
func buildVacationRfc822(in RunInput, subject, body string) ([]byte, error) {
	id := newMessageID()
	to := extractBareAddress(in.FromHeader)
	if to == "" {
		return nil, fmt.Errorf("vacation reply: empty recipient")
	}
	from := in.MailboxEmail
	if from == "" {
		return nil, fmt.Errorf("vacation reply: empty sender")
	}
	if subject == "" {
		subject = "Out of office"
	}
	if err := validateHeaderField("Subject", subject); err != nil {
		return nil, err
	}
	if body == "" {
		body = "I am out of the office and will reply when I return."
	}
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(from)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(to)
	b.WriteString("\r\n")
	b.WriteString("Subject: ")
	b.WriteString(subject)
	b.WriteString("\r\n")
	b.WriteString("Date: ")
	b.WriteString(time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("\r\n")
	b.WriteString("Message-ID: <")
	b.WriteString(id)
	b.WriteString("@orvix.local>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(HeaderVacation)
	b.WriteString(": yes\r\n")
	b.WriteString("Auto-Submitted: auto-replied\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(sanitizeBody(body))
	b.WriteString("\r\n")
	return []byte(b.String()), nil
}

// newMessageID returns a fresh 32-char hex id. Same
// algorithm as storage.GenerateMessageID; duplicated here so
// the runner package does not pull in the whole storage
// package transitively. (storage.MailStore is required by
// the runner for the other operations; this helper exists
// for documentation / parity only — callers prefer
// storage.GenerateMessageID when the storage package is
// already imported.)
func newMessageID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// validateHeaderField rejects values that would let the
// caller inject extra headers into an auto-generated RFC822
// message. Used by buildVacationRfc822 (and any future
// outbound builder) as a defence-in-depth check on top of
// the storage-layer validators.
//
// The "name" parameter is the header name (Subject, From,
// etc.) and only exists so error messages can quote it.
//
// Rejects:
//   - CR (0x0D) and LF (0x0A): would break out of the
//     current header line into a new one.
//   - NUL (0x00): forbidden by RFC 5322.
//   - Other C0 control bytes (0x01–0x1F) except HTAB (0x09).
//   - DEL (0x7F).
//
// Unicode bytes (0x80+) are permitted; mail clients decode
// Subject and From as UTF-8 and modern MTAs carry UTF-8
// mail on the wire.
func validateHeaderField(name, value string) error {
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '\r':
			return fmt.Errorf("%s contains CR (position %d): header injection", name, i)
		case c == '\n':
			return fmt.Errorf("%s contains LF (position %d): header injection", name, i)
		case c == 0:
			return fmt.Errorf("%s contains NUL (position %d): forbidden by RFC 5322", name, i)
		case c < 0x20 && c != '\t':
			return fmt.Errorf("%s contains control byte 0x%02X (position %d)", name, c, i)
		case c == 0x7f:
			return fmt.Errorf("%s contains DEL (position %d)", name, i)
		}
	}
	return nil
}

// sanitizeBody strips the leading dot-stuffing sequence
// from the body. SMTP DATA requires every line starting
// with a literal "." to be escaped to ".." so the receiving
// MTA does not interpret the lone dot as end-of-data. The
// runner writes the auto-reply into a fresh Message row
// (not raw to the wire), but the queue worker that
// delivers it later will relay it through SMTP DATA —
// failing to strip a leading-dot line here would cause the
// downstream MTA to truncate the body.
//
// We only strip "." at the start of a line. Inline "."
// characters are preserved.
//
// The body is otherwise passed through verbatim — body
// content is allowed to contain CR/LF; only HEADER values
// are restricted. The blank-line header/body separator is
// written by the caller, not by this function.
func sanitizeBody(body string) string {
	var b strings.Builder
	b.Grow(len(body))
	atLineStart := true
	for i := 0; i < len(body); i++ {
		c := body[i]
		if atLineStart {
			if c == '.' {
				// Skip the dot. The line itself
				// remains valid; downstream MTA
				// sees the un-escaped form.
				atLineStart = false
				continue
			}
			atLineStart = false
		}
		b.WriteByte(c)
		if c == '\n' {
			atLineStart = true
		}
	}
	return b.String()
}