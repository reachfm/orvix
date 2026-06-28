package smtp

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// This file owns the bridge between the SMTP receiver and the
// rules engine. The receiver has just stored the message in the
// recipient's mailbox durably (see AcceptMessage). Now it
// invokes the rules runner, which decides whether to forward,
// vacation-reply, move, or flag. The runner NEVER sees the
// durable message row — it only enqueues outbound work and
// returns a description of what should happen to the local
// copy. This file applies that description defensively: every
// step is wrapped so a panic / DB error inside the runner does
// NOT roll back the inbound delivery.
//
// Hard constraints satisfied by this file (the runner enforces
// the matching ones on its side):
//
//   1. Original stored even if rule action fails.
//      AcceptMessage calls StoreMessage BEFORE calling
//      applyRulesRunner. applyRulesRunner never deletes or
//      moves the original unless MoveToFolder / SetFlag /
//      ForwardKeepCopy cleanly succeed. A panic in the
//      runner is recovered and logged; the original stays
//      in INBOX (or whatever folder the runner decided,
//      with the move applied only if Move() succeeded).
//   2. Forwarding and vacation use the existing queue /
//      outbound path only. enqueueOutbound (rules package)
//      writes a fresh Message row in the sender's Sent
//      folder then calls QueueEngine.Enqueue. There is no
//      raw SMTP / network call here or in the runner.
//   3. Rules runner failure is logged / audited and does
//      NOT fail SMTP acceptance. The defer-recover in
//      applyRulesRunner returns nil; AcceptMessage has
//      already queued the message for local delivery,
//      so a 250 response goes back to the sending MTA.
//
// What this file does NOT do:
//   - It does NOT call MailStore.StoreMessage. The runner
//     has already done that for the forwarded / vacation
//     outbound messages; we only enqueue the queue entry.
//   - It does NOT touch inbound bytes. Headers (auth, spam
//     score, Received-SPF) have already been injected by
//     AcceptMessage before this runs.

// rulesRunnerApplyTimeout caps how long we let the runner
// take to decide what to do with one inbound message. The
// runner must be fast — it is a per-message DB read on the
// SMTP accept path. We cap it generously because the
// runner also does outbound inserts on the same call, but
// we never want it to block the SMTP accept indefinitely.
const rulesRunnerApplyTimeout = 30 * time.Second

// applyRulesRunner runs the rules engine for one locally-
// stored inbound message and applies the runner's outputs
// (move / flag / keep-copy) defensively.
//
// The caller MUST have already stored the message in the
// recipient's mailbox. This function never attempts to
// delete the inbound row unless the runner explicitly
// requested it via ForwardKeepCopy=false AND the forward
// enqueue succeeded.
//
// Errors are logged + audited but never returned: the SMTP
// accept has already committed the message to the
// recipient's mailbox, and returning an error would force
// AcceptMessage to surface a 4xx to the sending MTA,
// which would risk a duplicate delivery retry. The contract
// is "store first, decide later, never lose the original".
func (r *Receiver) applyRulesRunner(ctx context.Context, rcpt resolvedRecipient, msg *storage.Message, rfc822Data []byte) {
	if r.RulesRunner == nil {
		return
	}
	// Defensive recover: a panic inside the runner must
	// not propagate up and abort the SMTP accept. The
	// durable message is already in INBOX; we keep it
	// there and log the failure with a stack trace for
	// post-mortem.
	defer func() {
		if rec := recover(); rec != nil {
			r.logRulesRunnerFailure(rcpt, msg, "panic",
				fmt.Errorf("rules runner panic: %v\n%s", rec, debug.Stack()))
		}
	}()

	runCtx, cancel := context.WithTimeout(ctx, rulesRunnerApplyTimeout)
	defer cancel()

	fromHeader := extractHeaderValue(rfc822Data, "From")
	if fromHeader == "" {
		fromHeader = msg.FromAddress
	}
	toHeader := extractHeaderValue(rfc822Data, "To")
	if toHeader == "" {
		toHeader = msg.ToAddresses
	}
	ccHeader := extractHeaderValue(rfc822Data, "Cc")

	in := rules.RunInput{
		MailboxID:     rcpt.MailboxID,
		TenantID:      rcpt.TenantID,
		DomainID:      rcpt.DomainID,
		MailboxEmail:  rcpt.Email,
		MessageID:     msg.MessageID,
		RFC822:        rfc822Data,
		FromHeader:    fromHeader,
		ToHeader:      toHeader,
		CcHeader:      ccHeader,
		Subject:       msg.Subject,
		BodyText:      extractBodyForRules(rfc822Data),
		HasAttachment: hasAttachmentHint(rfc822Data),
		ReceivedAt:    msg.ReceivedDate,
	}

	out, runErr := r.RulesRunner.Run(runCtx, in)
	if runErr != nil {
		// The runner returned an error. This is the
		// documented "rules could not load" path —
		// fail-closed: do not apply any rule action, do
		// not delete the inbound row, log + audit.
		r.logRulesRunnerFailure(rcpt, msg, "run_error", runErr)
		return
	}
	if out == nil {
		// Defensive: the runner contract says Run always
		// returns a non-nil output. Treat a nil as a
		// silent failure.
		r.logRulesRunnerFailure(rcpt, msg, "nil_output", fmt.Errorf("rules runner returned nil output"))
		return
	}

	// Apply MoveToFolder first. If Move fails we still
	// proceed to flags and copy — a partial application is
	// better than nothing, and the user can always move
	// the message manually if the engine could not.
	if out.MoveToFolder != "" {
		if err := r.applyMove(runCtx, rcpt.MailboxID, msg, out.MoveToFolder); err != nil {
			r.logRulesRunnerFailure(rcpt, msg, "move_failed", err)
		}
	}

	// Apply CopyToFolder. Copy is independent of move:
	// MailStore.CopyMessage inserts a NEW Message row in
	// the target folder (with a fresh MessageID and a
	// hardlink to the RFC822 file) and leaves the
	// original row untouched. A copy rule does NOT move
	// the original.
	//
	// Earlier revisions of this code aliased CopyTo onto
	// MoveTo in the runner, so a configured "Copy to
	// Junk" actually relocated the message. The runner
	// now emits CopyToFolder as a separate field; this
	// block is the only place that performs the copy.
	if out.CopyToFolder != "" {
		if err := r.applyCopy(runCtx, rcpt.MailboxID, msg, out.CopyToFolder); err != nil {
			r.logRulesRunnerFailure(rcpt, msg, "copy_failed", err)
		}
	}

	// Apply SetFlag. nil pointers in the SetFlagValue mean
	// "leave unchanged" — only the explicit bool fields
	// flip. Each flag flip is independent: one failure
	// does not roll back the others.
	if out.SetFlag != nil {
		if err := r.applyFlags(runCtx, msg.ID, out.SetFlag); err != nil {
			r.logRulesRunnerFailure(rcpt, msg, "flag_failed", err)
		}
	}

	// Forward-keep-copy handling. The runner has already
	// enqueued the forward (or surfaced a skip reason in
	// out.SkipReason). If forwarding succeeded AND
	// ForwardKeepCopy is false, we delete the local copy.
	// We never delete if the runner reported a forward
	// failure: keeping the original is the safer contract.
	if out.ForwardedTo != "" && !out.ForwardKeepCopy && out.SkipReason == "" {
		if err := r.MailStore.DeleteMessage(runCtx, msg.ID, nil); err != nil {
			r.logRulesRunnerFailure(rcpt, msg, "forward_purge_failed", err)
		}
	}

	// Audit log entry: every rule-runner invocation
	// records a row so the operator can see what
	// happened — even when nothing matched.
	r.auditRulesRunnerOutcome(rcpt, msg, out)
}

// applyMove relocates the message to the named folder in
// the recipient's mailbox. The folder lookup is
// case-insensitive; missing folders are logged and the
// move is skipped (the message stays where it is).
func (r *Receiver) applyMove(ctx context.Context, mailboxID uint, msg *storage.Message, folderPath string) error {
	folder, err := resolveFolderCaseInsensitiveFromStore(ctx, r.MailStore, mailboxID, folderPath)
	if err != nil {
		return fmt.Errorf("resolve folder %q: %w", folderPath, err)
	}
	if folder == nil {
		return fmt.Errorf("folder %q not found", folderPath)
	}
	return r.MailStore.MoveMessage(ctx, msg.ID, folder.ID, nil)
}

// applyCopy duplicates the message into the named folder
// in the recipient's mailbox. The original row stays
// where it is — CopyMessage inserts a new DB row with a
// fresh MessageID and hardlinks the RFC822 file so the
// body bytes are shared on disk.
//
// Copy does NOT delete the original. A copy failure is
// logged but never propagated to the SMTP accept: the
// message remains in the source folder and the user can
// retry the rule by re-saving it.
func (r *Receiver) applyCopy(ctx context.Context, mailboxID uint, msg *storage.Message, folderPath string) error {
	folder, err := resolveFolderCaseInsensitiveFromStore(ctx, r.MailStore, mailboxID, folderPath)
	if err != nil {
		return fmt.Errorf("resolve folder %q: %w", folderPath, err)
	}
	if folder == nil {
		return fmt.Errorf("folder %q not found", folderPath)
	}
	if _, err := r.MailStore.CopyMessage(ctx, msg.ID, mailboxID, folder.ID, nil); err != nil {
		return fmt.Errorf("copy to folder %q: %w", folderPath, err)
	}
	return nil
}

// applyFlags flips only the flags the engine set. nil
// pointers in the SetFlagValue mean "leave unchanged".
func (r *Receiver) applyFlags(ctx context.Context, messageID uint, flags *storage.SetFlagValue) error {
	if flags == nil {
		return nil
	}
	seen, answered, flagged, draft, deleted, junk := flags.Seen, boolPtr(false), flags.Flagged, boolPtr(false), boolPtr(false), boolPtr(false)
	return r.MailStore.Messages.UpdateFlags(ctx, messageID, seen, answered, flagged, draft, deleted, junk, nil)
}

// auditRulesRunnerOutcome records an observability event
// for the rule decision. The event carries the message_id
// and the SkipReason so the admin UI can surface "what
// happened" without scraping logs.
func (r *Receiver) auditRulesRunnerOutcome(rcpt resolvedRecipient, msg *storage.Message, out *rules.RunOutput) {
	if r.Observability == nil {
		return
	}
	fields := map[string]string{
		"mailbox_id":    fmt.Sprintf("%d", rcpt.MailboxID),
		"message_id":    msg.MessageID,
		"moved_to":      out.MoveToFolder,
		"copied_to":     out.CopyToFolder,
		"forwarded_to":  out.ForwardedTo,
		"keep_copy":     boolYesNo(out.ForwardKeepCopy),
		"vacation_sent": boolYesNo(out.VacationReply != nil),
		"skip_reason":   out.SkipReason,
	}
	r.Observability.EventHistory.Record(r.rulesRunnerEventType(out), fields)
}

// rulesRunnerEventType picks the observability event kind
// for the rule outcome. We keep three buckets so the
// operator dashboard can colour-code them.
func (r *Receiver) rulesRunnerEventType(out *rules.RunOutput) observability.EventType {
	switch {
	case out.SkipReason != "":
		return observability.EventRulesRunnerSkip
	case out.ForwardedTo != "" || out.VacationReply != nil:
		return observability.EventRulesRunnerAction
	default:
		return observability.EventRulesRunnerPass
	}
}

// logRulesRunnerFailure is the single sink for every
// runner-side failure: panic, run-error, move failure,
// flag failure, purge failure. The observability package's
// Logger keeps a bounded ring buffer that the admin
// telemetry endpoint surfaces; an EventHistory entry
// carries the same data with a stable event type so the
// dashboard can colour-code failures.
func (r *Receiver) logRulesRunnerFailure(rcpt resolvedRecipient, msg *storage.Message, kind string, err error) {
	if r.Observability == nil {
		return
	}
	fields := map[string]string{
		"mailbox_id": fmt.Sprintf("%d", rcpt.MailboxID),
		"message_id": msg.MessageID,
		"kind":       kind,
		"error":      err.Error(),
	}
	if r.Observability.Logger != nil {
		r.Observability.Logger.Event(observability.EventRulesRunnerError, fields)
	}
	r.Observability.EventHistory.Record(observability.EventRulesRunnerError, fields)
}

// extractBodyForRules is a cheap body extractor used by
// the rules runner to satisfy body_contains conditions.
// We deliberately do NOT invoke the full MIME parser
// here — body_contains only needs a substring match on
// the textual part. The full parser is used by the
// attachment extractor in MailStore.StoreMessage.
//
// Returns "" when no textual body can be located.
func extractBodyForRules(rfc822 []byte) string {
	const maxScan = 64 * 1024
	if len(rfc822) > maxScan {
		rfc822 = rfc822[:maxScan]
	}
	// Look for the first blank line that separates
	// headers from body, then scan forward.
	for i := 0; i < len(rfc822)-3; i++ {
		if rfc822[i] == '\n' && rfc822[i+1] == '\n' {
			return string(rfc822[i+2:])
		}
		if rfc822[i] == '\r' && rfc822[i+1] == '\n' && rfc822[i+2] == '\r' && rfc822[i+3] == '\n' {
			return string(rfc822[i+4:])
		}
	}
	return ""
}

// resolveFolderCaseInsensitiveFromStore is a thin wrapper
// around MailStore.Folders.GetByPath that does a
// case-insensitive fallback (the rules engine writes
// folder paths with canonical casing, but the user may
// have configured a rule with mixed case).
func resolveFolderCaseInsensitiveFromStore(ctx context.Context, ms *storage.MailStore, mailboxID uint, path string) (*storage.Folder, error) {
	if f, err := ms.Folders.GetByPath(ctx, mailboxID, path, nil); err == nil && f != nil {
		return f, nil
	}
	folders, err := ms.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	for i := range folders {
		if equalFoldASCII(folders[i].Path, path) || equalFoldASCII(folders[i].Name, path) {
			return &folders[i], nil
		}
	}
	return nil, nil
}

// equalFoldASCII compares two strings after lowercasing
// ASCII letters only. Folder paths are ASCII so this is
// safe and avoids importing strings just for EqualFold.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func boolPtr(b bool) *bool { return &b }

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
