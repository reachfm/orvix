package smtp

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/antispam"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/dmarc"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/spf"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/rulertypes"
)

// Receiver handles message acceptance, MailStore persistence, and Queue enqueue.
// If SPFEvaluator and DMARCEvaluator are non-nil, SPF and DMARC evaluation
// are performed during message acceptance and auth headers are injected.
type Receiver struct {
	Engine         *coremail.Engine
	MailStore      *storage.MailStore
	QueueEngine    *queue.QueueEngine
	Config         Config
	SPFEvaluator   *spf.Evaluator
	DMARCEvaluator *dmarc.Evaluator
	AntiSpamEngine *antispam.Engine
	DKIMVerifier   *dkim.Verifier
	Observability  *observability.Observability
	// RulesRunner is the optional rules engine runner. When
	// non-nil, every local-delivery message is fed through it
	// AFTER the message is durably stored in the recipient's
	// mailbox. The runner never mutates the inbound message
	// itself; the receiver applies the runner's MoveTo /
	// SetFlag outputs to MailStore.Move + MailStore.UpdateFlags.
	// If the runner is nil the receiver falls back to the
	// legacy direct-to-INBOX behaviour, which keeps the
	// existing tests in the smtp package green even before
	// the operator enables the rules engine.
	//
	// Typed as an interface so tests can substitute a
	// panic-throwing runner to verify the "rules runner
	// failure does not lose the original" guarantee. The
	// production code always assigns a *rules.Runner.
	RulesRunner rules.RulesRunner
	// AntivirusEngine, when non-nil, scans every accepted
	// message body BEFORE durable storage / queue enqueue.
	// Wired by the runtime module during initCore so the
	// admin panel can claim "antivirus active" only when
	// the runtime has actually called MarkEnforced on the
	// engine. nil means the runtime did not configure
	// antivirus; AcceptMessage skips the scan with no
	// side effects (this is the test-friendly default).
	AntivirusEngine *antivirus.Engine
	// AcceptanceEngine applies admin-scoped acceptance &
	// routing rules at MAIL FROM, RCPT TO, and DATA.
	// nil means the runtime did not configure acceptance
	// rules; AcceptMessage and the command handler skip
	// the engine with no side effects. The runtime
	// package owns the wiring.
	AcceptanceEngine rulertypes.RuleEngine
	// IncomingRuleEngine applies admin-scoped incoming
	// message rules AFTER authentication + BEFORE final
	// storage. nil means "no admin rules configured" and
	// the receive path skips with no side effects.
	IncomingRuleEngine rulertypes.RuleEngine
	// DB is supplied by the runtime so the antivirus and
	// acceptance engines can persist quarantine rows.
	// DB is supplied by the runtime so the antivirus
	// engine can persist quarantine rows. nil means
	// quarantine persistence is disabled (the engine
	// still decides reject / tag / quarantine, but
	// quarantine rows are not durable).
	DB *sql.DB
}

// RuleEvaluator is the minimal interface any rule engine
// must expose to be wired into the SMTP receiver. The
// internal/ruler package satisfies it. Tests may use a
// stub implementation to verify the receiver's branching
// without standing up the full Ruler.
//
// The alias is kept so existing receiver code reads
// naturally; the canonical definition lives in
// internal/rulertypes.
type RuleEvaluator = rulertypes.RuleEngine

// RuleQuery is the local alias for rulertypes.Query.
// Keeping the alias makes the receiver code read in
// terms of "what the SMTP layer sees", not "what
// internal/ruler passes in". The two are bit-identical.
type RuleQuery = rulertypes.Query

// The MarkEnforced contract is satisfied by the same
// interface — kept here as an explicit type for clarity
// rather than for dispatch.
type MarkEnforcedFunc = rulertypes.SetEnforcedFunc

// NewReceiver creates a message receiver.
func NewReceiver(eng *coremail.Engine, ms *storage.MailStore, qe *queue.QueueEngine, cfg Config) *Receiver {
	return &Receiver{
		Engine:      eng,
		MailStore:   ms,
		QueueEngine: qe,
		Config:      cfg,
	}
}

type resolvedRecipient struct {
	Email     string
	MailboxID uint
	DomainID  uint
	TenantID  uint
	Domain    string
}

type externalRecipient struct {
	Email  string
	Domain string
}

// AcceptMessage processes a completed DATA transfer.
// Steps:
//  1. Evaluate SPF (if configured)
//  2. Evaluate DKIM (if configured)
//  3. Evaluate DMARC (if configured)
//  4. Anti-spam assessment
//  5. Enforce spam policy (reject if in enforcement mode)
//  6. Inject Received-SPF, Authentication-Results, X-Orvix-Spam headers
//  7. Parse the destination domain(s)
//  8. Validate recipients exist locally
//  9. Store message in each recipient's MailStore
//  10. Enqueue for local delivery (BEFORE any rule side effects)
//  11. Apply rules engine runner (forward / vacation / move /
//     copy / flag). The runner may enqueue outbound work but
//     ONLY AFTER local delivery is durably enqueued. If the
//     local enqueue in step 10 fails the runner NEVER runs
//     for that recipient — no forwarding, no vacation reply,
//     no auto-copy can leak into the queue ahead of the
//     original local delivery being accepted.
func (r *Receiver) AcceptMessage(ctx context.Context, session *Session) error {
	rfc822Data := session.DataBuffer

	if len(rfc822Data) == 0 {
		return fmt.Errorf("empty message body")
	}

	if int64(len(rfc822Data)) > r.Config.MaxMessageSizeBytes {
		return fmt.Errorf("message too large: %d > %d", len(rfc822Data), r.Config.MaxMessageSizeBytes)
	}

	// ── Acceptance & routing rules at DATA level ────────
	// The engine was already applied at MAIL FROM and
	// RCPT TO (in commands.go) for sender / recipient /
	// IP scopes. The DATA-stage evaluation also checks
	// subject-based patterns the command handler has no
	// access to.
	if r.AcceptanceEngine != nil {
		var firstLocal string
		for _, rcpt := range session.Recipients {
			dom := ExtractDomain(rcpt)
			if dom != "" {
				firstLocal = rcpt
				break
			}
		}
		ok, action, reason := r.AcceptanceEngine.Evaluate(ctx, RuleQuery{
			Sender:    session.MailFrom,
			Recipient: firstLocal,
			SourceIP:  session.RemoteAddr,
			Subject:   extractSubject(rfc822Data),
			MessageID: session.ID,
			Headers: map[string]string{
				"From":    session.MailFrom,
				"To":      strings.Join(session.Recipients, ","),
				"Subject": extractSubject(rfc822Data),
			},
		})
		if ok {
			switch action {
			case "reject":
				if r.Observability != nil {
					r.Observability.Metrics.IncSMTPRejected()
					r.Observability.EventHistory.Record(observability.EventAcceptanceRuleRejected, map[string]string{
						"sender":    session.MailFrom,
						"recipient": strings.Join(session.Recipients, ","),
						"reason":    reason,
					})
				}
				return fmt.Errorf("5.7.1 message rejected by acceptance rule: %s", reason)
			case "quarantine":
				// The acceptance-rule quarantine flow is
				// routed through the same StoreMessage +
				// quarantine_index path the antivirus
				// quarantine uses, but for acceptance
				// quarantine there is no antivirus
				// signature — the row is marked
				// reason="acceptance_rule:<note>".
				return r.acceptanceQuarantine(ctx, session, rfc822Data, reason)
			}
			// accept: continue to antivirus scan below.
		}
	}

	// ── Antivirus scan ───────────────────────────────────
	// The engine, when wired, scans every accepted message
	// body. The decision branch — accept / reject /
	// quarantine / tag — is configured by the operator
	// via the admin antivirus page and applies per the
	// engine.Policy() configured at boot. A nil engine
	// skips the scan entirely (no-op), which is the
	// test-friendly default.
	if r.AntivirusEngine != nil {
		dec := r.AntivirusEngine.Scan(ctx, rfc822Data, session.ID)
		switch dec.Action {
		case antivirus.ActionAccept:
			// Note: a tag decision still resolves to
			// ActionTag, not ActionAccept. Tag is
			// applied below by injecting the header.
		case antivirus.ActionReject:
			if r.Observability != nil {
				r.Observability.Metrics.IncSMTPRejected()
			}
			return fmt.Errorf("5.7.1 message rejected by antivirus: %s", dec.Reason)
		case antivirus.ActionQuarantine:
			if r.DB != nil {
				if _, err := r.AntivirusEngine.Quarantine(ctx, r.DB, "", session.ID,
					session.MailFrom, strings.Join(session.Recipients, ","),
					extractSubject(rfc822Data), dec.Virus, rfc822Data); err != nil {
					if r.Observability != nil {
						r.Observability.Metrics.IncAntivirusFailClosed()
					}
					return fmt.Errorf("5.7.1 antivirus quarantine failed: %v", err)
				}
			}
			// Return a temp-failure so the sender retries.
			// The message was stored to disk for the
			// admin to review; do NOT deliver to the
			// recipient.
			if r.Observability != nil {
				r.Observability.Metrics.IncSMTPRejected()
			}
			return fmt.Errorf("4.7.1 message quarantined by antivirus: %s", dec.Reason)
		case antivirus.ActionTag:
			// Inject X-Orvix-AV-Verdict:infected so the
			// downstream message-store path tags the
			// message as suspicious. The verdict header
			// is appended ahead of the existing
			// auth / spam headers below.
			verdictHeader := []byte("X-Orvix-AV-Verdict: infected (" + dec.Virus + ")\r\n")
			rfc822Data = append(verdictHeader, rfc822Data...)
		}
	}

	// ── Authentication evaluation ──────────────────────────
	var spfResult *spf.EvaluationResult
	var dkimResult *dkim.VerifyResultInfo
	var dmarcResult *dmarc.EvaluationResult

	mailFromDomain := ExtractDomain(session.MailFrom)
	heloDomain := extractHeloDomain(session)
	connectingIP := extractRemoteIP(session.RemoteAddr)

	if r.SPFEvaluator != nil && connectingIP != nil && mailFromDomain != "" {
		var spfErr error
		spfResult, spfErr = r.SPFEvaluator.Evaluate(ctx, &spf.Context{
			ConnectingIP:   connectingIP,
			HeloDomain:     heloDomain,
			MailFromDomain: mailFromDomain,
		})
		if spfErr != nil {
			spfResult = &spf.EvaluationResult{
				Result:          spf.ResultTempError,
				Explanation:     spfErr.Error(),
				EvaluatedDomain: mailFromDomain,
			}
			r.recordAuthError(observability.EventSPFTempError, mailFromDomain, spfErr)
		} else if spfResult != nil && (spfResult.Result == spf.ResultTempError || spfResult.Result == spf.ResultPermError) {
			r.recordAuthError(observability.EventSPFTempError, mailFromDomain, fmt.Errorf("%s", spfResult.Explanation))
		}
	}

	if r.DKIMVerifier != nil {
		dkimResult = r.DKIMVerifier.VerifyMessage(ctx, rfc822Data)
	}

	dkimResultStr := "none"
	dkimSigningDomain := ""
	if dkimResult != nil {
		dkimResultStr = dkimResult.Result.String()
		dkimSigningDomain = dkimResult.Domain
	}

	if r.DMARCEvaluator != nil && mailFromDomain != "" {
		spfResultStr := "none"
		spfDomain := ""
		if spfResult != nil {
			spfResultStr = spfResult.Result.String()
			spfDomain = spfResult.EvaluatedDomain
		}
		var dmarcErr error
		dmarcResult, dmarcErr = r.DMARCEvaluator.Evaluate(&dmarc.EvaluationInput{
			FromDomain:        mailFromDomain,
			SPFResult:         spfResultStr,
			SPFAuthDomain:     spfDomain,
			DKIMResult:        dkimResultStr,
			DKIMSigningDomain: dkimSigningDomain,
		})
		if dmarcErr != nil {
			dmarcResult = &dmarc.EvaluationResult{
				Result:          dmarc.ResultTempError,
				Explanation:     dmarcErr.Error(),
				Policy:          dmarc.PolicyNone,
				EvaluatedDomain: mailFromDomain,
				SPFResult:       spfResultStr,
				DKIMResult:      dkimResultStr,
			}
			r.recordAuthError(observability.EventDMARCTempError, mailFromDomain, dmarcErr)
		} else if dmarcResult != nil && (dmarcResult.Result == dmarc.ResultTempError || dmarcResult.Result == dmarc.ResultPermError) {
			r.recordAuthError(observability.EventDMARCTempError, mailFromDomain, fmt.Errorf("%s", dmarcResult.Explanation))
		}
	}

	// ── Anti-spam assessment ───────────────────────────────
	var spamAssessment *antispam.SpamAssessment
	if r.AntiSpamEngine != nil && connectingIP != nil {
		spfResultStr := "none"
		dmarcResultStr := "none"
		dmarcPolicyStr := ""
		if spfResult != nil {
			spfResultStr = spfResult.Result.String()
		}
		if dmarcResult != nil {
			dmarcResultStr = dmarcResult.Result.String()
			dmarcPolicyStr = dmarcResult.Policy.String()
		}
		fromDomain := mailFromDomain
		if fromDomain == "" {
			fromDomain = heloDomain
		}
		spamAssessment = r.AntiSpamEngine.AssessFromContext(
			connectingIP, heloDomain, mailFromDomain, fromDomain,
			spfResultStr, dkimResultStr, dmarcResultStr, dmarcPolicyStr,
			len(session.Recipients), false,
		)
	}

	// ── Anti-spam enforcement ──────────────────────────────
	if spamAssessment != nil && r.Config.SpamMode == SpamModeEnforcement {
		if spamAssessment.Verdict == antispam.VerdictReject {
			if r.Observability != nil {
				r.Observability.Metrics.IncSpamRejected()
				r.Observability.EventHistory.Record(observability.EventSpamRejected, map[string]string{
					"score":     fmt.Sprintf("%.1f", spamAssessment.Score),
					"sender":    mailFromDomain,
					"remote_ip": connectingIP.String(),
				})
			}
			return fmt.Errorf("5.7.1 message rejected by anti-spam policy")
		}
	}

	// ── Observability: record auth/spam metrics ──────────
	if r.Observability != nil {
		if spfResult != nil {
			r.Observability.Metrics.RecordSPFResult(spfResult.Result.String())
		}
		if dkimResult != nil {
			if dkimResult.Result == dkim.VerifyPass {
				r.Observability.Metrics.IncDKIMSigned()
			} else if dkimResult.Result == dkim.VerifyFail {
				r.Observability.Metrics.IncDKIMFailed()
			}
		}
		if dmarcResult != nil {
			r.Observability.Metrics.RecordDMARCResult(dmarcResult.Result.String())
		}
		if spamAssessment != nil {
			r.Observability.Metrics.RecordSpamVerdict(spamAssessment.Verdict.String())
			r.Observability.EventHistory.Record(observability.EventSpamAccepted, map[string]string{
				"score":     fmt.Sprintf("%.1f", spamAssessment.Score),
				"verdict":   spamAssessment.Verdict.String(),
				"sender":    mailFromDomain,
				"remote_ip": connectingIP.String(),
			})
		}
	}

	// ── Inject authentication + anti-spam headers ─────────
	var authHeaders []byte

	if spfResult != nil && connectingIP != nil {
		receivedSPF := spf.FormatReceivedSPF(spfResult, connectingIP, r.Config.Hostname)
		if receivedSPF != "" {
			authHeaders = append(authHeaders, []byte("Received-SPF: "+receivedSPF+"\r\n")...)
		}
	}

	// Build Authentication-Results with SPF, DKIM, and DMARC.
	var spfAuthResult *spf.AuthResult
	if spfResult != nil {
		spfAuthResult = spf.AuthResultFromSPF(spfResult)
	}
	var dkimAuthResult *dmarc.AuthResult
	if dkimResult != nil {
		dkimAuthResult = &dmarc.AuthResult{
			Method:      "dkim",
			Result:      dkimResult.Result.String(),
			Domain:      dkimResult.Domain,
			Explanation: dkimResult.Explanation,
		}
	}
	var dmarcAuthResult *dmarc.AuthResult
	if dmarcResult != nil {
		dmarcAuthResult = dmarc.AuthResultFromDMARC(dmarcResult)
	}

	authResults := &dmarc.AuthResultList{
		SPF:   convertSPFAuthResult(spfAuthResult),
		DKIM:  dkimAuthResult,
		DMARC: dmarcAuthResult,
	}
	authHeader := dmarc.FormatAuthResults(authResults, r.Config.Hostname, connectingIP)
	if authHeader != "" {
		authHeaders = append(authHeaders, []byte("Authentication-Results: "+authHeader+"\r\n")...)
	}

	// Anti-spam headers.
	if spamAssessment != nil {
		scoreHeader := antispam.FormatSpamScoreHeader(spamAssessment)
		if scoreHeader != "" {
			authHeaders = append(authHeaders, []byte("X-Orvix-Spam-Score: "+scoreHeader+"\r\n")...)
		}
		verdictHeader := antispam.FormatSpamVerdictHeader(spamAssessment)
		if verdictHeader != "" {
			authHeaders = append(authHeaders, []byte("X-Orvix-Spam-Verdict: "+verdictHeader+"\r\n")...)
		}
		reasons := antispam.FormatSpamReasons(spamAssessment)
		if reasons != "" {
			authHeaders = append(authHeaders, []byte("X-Orvix-Spam-Reasons: "+reasons+"\r\n")...)
		}
	}

	if len(authHeaders) > 0 {
		rfc822Data = append(authHeaders, rfc822Data...)
	}

	// ── Incoming message rules (admin-scoped) ────────────
	// Walks each enabled rule in priority order and
	// applies the action it returns. The engine is
	// permitted to add an X-Orvix-Incoming-Rule header
	// on accept decisions so the receiver path's
	// branches are observable.
	if r.IncomingRuleEngine != nil {
		var firstLocal string
		for _, rcpt := range session.Recipients {
			dom := ExtractDomain(rcpt)
			if dom != "" {
				firstLocal = rcpt
				break
			}
		}
		ok, action, reason := r.IncomingRuleEngine.Evaluate(ctx, RuleQuery{
			Sender:    session.MailFrom,
			Recipient: firstLocal,
			SourceIP:  session.RemoteAddr,
			Subject:   extractSubject(rfc822Data),
			MessageID: session.ID,
			Headers: map[string]string{
				"From":    session.MailFrom,
				"To":      strings.Join(session.Recipients, ","),
				"Subject": extractSubject(rfc822Data),
			},
		})
		if ok {
			switch action {
			case "reject":
				if r.Observability != nil {
					r.Observability.Metrics.IncSMTPRejected()
					r.Observability.EventHistory.Record(observability.EventIncomingRuleApplied, map[string]string{
						"sender":    session.MailFrom,
						"recipient": strings.Join(session.Recipients, ","),
						"reason":    "reject: " + reason,
					})
				}
				return fmt.Errorf("5.7.1 message rejected by incoming rule: %s", reason)
			case "quarantine":
				// Same path as acceptance-rule quarantine:
				// persist a coremail_quarantine_index row and
				// return a temp-failure.
				_, qerr := r.DB.ExecContext(ctx, `INSERT INTO coremail_quarantine_index
					(tenant_id, message_id, recipient, sender, subject, reason, severity, status, created_at)
					VALUES (?, ?, ?, ?, ?, ?, 'high', 'held', ?)`,
					0, session.ID,
					strings.Join(session.Recipients, ","),
					session.MailFrom, extractSubject(rfc822Data), "incoming_rule:"+reason,
					time.Now().UTC())
				if qerr != nil && r.Observability != nil {
					r.Observability.Metrics.IncSMTPRejected()
					return fmt.Errorf("4.7.1 incoming rule quarantine failed: %v", qerr)
				}
				if r.Observability != nil {
					r.Observability.EventHistory.Record(observability.EventQuarantineHeld, map[string]string{
						"sender":    session.MailFrom,
						"recipient": strings.Join(session.Recipients, ","),
						"reason":    "incoming_rule:" + reason,
					})
				}
				return fmt.Errorf("4.7.1 message held by incoming rule: %s", reason)
			case "tag":
				// Add a header documenting the rule that
				// fired. The downstream message-store path
				// keeps the header through delivery.
				tagHeader := []byte("X-Orvix-Incoming-Rule: " + reason + "\r\n")
				rfc822Data = append(tagHeader, rfc822Data...)
				if r.Observability != nil {
					r.Observability.EventHistory.Record(observability.EventIncomingRuleApplied, map[string]string{
						"sender":    session.MailFrom,
						"recipient": strings.Join(session.Recipients, ","),
						"reason":    "tag: " + reason,
					})
				}
			}
		}
	}

	// ── Recipient resolution ───────────────────────────────
	var recipients []resolvedRecipient
	var externalRecipients []externalRecipient
	for _, rcpt := range session.Recipients {
		domain := ExtractDomain(rcpt)
		if domain == "" {
			return fmt.Errorf("invalid recipient: %s", rcpt)
		}

		dom, err := r.Engine.Domains.GetByName(ctx, domain, nil)
		if err != nil {
			return fmt.Errorf("lookup domain %s: %w", domain, err)
		}
		if dom == nil || dom.Status != coremail.DomainActive {
			externalRecipients = append(externalRecipients, externalRecipient{
				Email:  rcpt,
				Domain: domain,
			})
			continue
		}

		targets, err := r.Engine.Auth.ResolveAddress(ctx, rcpt)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", rcpt, err)
		}

		for _, target := range targets {
			mbox, err := r.Engine.Mailboxes.GetByEmail(ctx, target, nil)
			if err != nil {
				return fmt.Errorf("lookup mailbox %s: %w", target, err)
			}
			if mbox == nil || mbox.Status != coremail.MailboxActive {
				return fmt.Errorf("mailbox not active: %s", target)
			}
			recipients = append(recipients, resolvedRecipient{
				Email:     target,
				MailboxID: mbox.ID,
				DomainID:  dom.ID,
				TenantID:  dom.TenantID,
				Domain:    domain,
			})
		}
	}

	// Deduplicate local recipients by MailboxID. If the same address appears
	// in multiple RCPT TO commands, or an alias resolves to the same mailbox,
	// deliver exactly one copy.
	recipients = dedupRecipients(recipients)

	if len(recipients) == 0 && len(externalRecipients) == 0 {
		return fmt.Errorf("no valid recipients")
	}

	// Store the message at least once even if only external recipients.
	var firstMessageID string
	var senderTenantID uint
	var senderDomainID uint
	if session.AuthIdentity != nil {
		senderTenantID = session.AuthIdentity.TenantID
		senderDomainID = session.AuthIdentity.DomainID
	}

	// Store message in each local recipient's mailbox.
	for _, rcpt := range recipients {
		inbox, err := r.MailStore.Folders.GetByPath(ctx, rcpt.MailboxID, "INBOX", nil)
		if err != nil {
			return fmt.Errorf("get inbox for %d: %w", rcpt.MailboxID, err)
		}
		if inbox == nil {
			return fmt.Errorf("inbox not found for mailbox %d", rcpt.MailboxID)
		}

		msg := &storage.Message{
			MessageID:    storage.GenerateMessageID(),
			TenantID:     rcpt.TenantID,
			DomainID:     rcpt.DomainID,
			MailboxID:    rcpt.MailboxID,
			FolderID:     inbox.ID,
			FromAddress:  session.MailFrom,
			ToAddresses:  strings.Join(session.Recipients, ","),
			Subject:      extractSubject(rfc822Data),
			ReceivedDate: time.Now().UTC(),
		}

		if err := r.MailStore.StoreMessage(ctx, msg, rfc822Data, nil); err != nil {
			return fmt.Errorf("store message for %s: %w", rcpt.Email, err)
		}
		if firstMessageID == "" {
			firstMessageID = msg.MessageID
		}

		// Enqueue the LOCAL delivery BEFORE running the rules
		// runner. The runner may emit forwarding / vacation
		// side effects (queue entries of its own); those are
		// allowed to land only once the original local copy
		// is durably on the queue. If this local enqueue
		// fails we MUST purge the stored row and return the
		// error WITHOUT invoking the runner — otherwise a
		// transient queue outage could leave the recipient
		// with no local copy while auto-forwarded / vacation
		// replies already went out to third parties.
		qEntry := &queue.QueueEntry{
			TenantID:        rcpt.TenantID,
			DomainID:        rcpt.DomainID,
			MailboxID:       &rcpt.MailboxID,
			MessageID:       msg.MessageID,
			FromAddress:     session.MailFrom,
			ToAddress:       rcpt.Email,
			RecipientDomain: rcpt.Domain,
			Direction:       queue.DirectionInbound,
			DeliveryMode:    queue.DeliveryLocal,
			Status:          queue.StatusPending,
		}

		if err := r.QueueEngine.Enqueue(ctx, qEntry); err != nil {
			// Local delivery failed. Purge the stored row
			// so we do not leak an orphan message, then
			// return the error WITHOUT invoking the rules
			// runner — no forward / vacation / copy can
			// leak ahead of a failed local accept.
			purgeErr := r.MailStore.PurgeMessage(ctx, msg.ID, nil)
			if purgeErr != nil {
				return fmt.Errorf("enqueue for %s: %w (and purge failed: %v)", rcpt.Email, err, purgeErr)
			}
			return fmt.Errorf("enqueue for %s: %w", rcpt.Email, err)
		}

		// Local delivery is now durably enqueued. Apply
		// the rules runner for forwarding / vacation /
		// move / copy / flag. The runner is best-effort:
		// a panic / DB error inside the runner must not
		// abort the SMTP accept because the original is
		// already on the queue (and therefore the local
		// inbox will be populated by the queue worker).
		// applyRulesRunner's own defer-recover ensures
		// that contract.
		if r.RulesRunner != nil {
			r.applyRulesRunner(ctx, rcpt, msg, rfc822Data)
		}
	}

	// ── External recipients (outbound relay) ────────────────
	for _, ext := range externalRecipients {
		msgID := firstMessageID
		if msgID == "" {
			msgID = storage.GenerateMessageID()
			firstMessageID = msgID
			extMsg := &storage.Message{
				MessageID:    msgID,
				TenantID:     senderTenantID,
				DomainID:     senderDomainID,
				FromAddress:  session.MailFrom,
				ToAddresses:  ext.Email,
				Subject:      extractSubject(rfc822Data),
				ReceivedDate: time.Now().UTC(),
			}
			if session.AuthIdentity != nil && session.AuthIdentity.MailboxID > 0 {
				extMsg.MailboxID = session.AuthIdentity.MailboxID
			}
			if err := r.MailStore.StoreMessage(ctx, extMsg, rfc822Data, nil); err != nil {
				return fmt.Errorf("store message for external %s: %w", ext.Email, err)
			}
		}

		qEntry := &queue.QueueEntry{
			TenantID:        senderTenantID,
			DomainID:        senderDomainID,
			MessageID:       msgID,
			FromAddress:     session.MailFrom,
			ToAddress:       ext.Email,
			RecipientDomain: ext.Domain,
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
			Status:          queue.StatusPending,
		}

		if err := r.QueueEngine.Enqueue(ctx, qEntry); err != nil {
			return fmt.Errorf("enqueue external %s: %w", ext.Email, err)
		}
	}

	// ── Observability: SMTP accepted. ───────────────────────
	if r.Observability != nil {
		r.Observability.Metrics.IncSMTPAccepted()
		r.Observability.EventHistory.Record(observability.EventSMTPAccepted, map[string]string{
			"sender":    session.MailFrom,
			"recipient": strings.Join(session.Recipients, ","),
			"remote_ip": connectingIP.String(),
		})
	}

	return nil
}

// acceptanceQuarantine handles the "quarantine" action
// returned by the acceptance-rule engine. The receiver
// persists the message to disk via the same path the
// antivirus quarantine uses, then returns a temp-failure
// to the sender so the message is retried later (after
// the operator inspects the quarantine).
func (r *Receiver) acceptanceQuarantine(ctx context.Context, session *Session, rfc822 []byte, reason string) error {
	if r.DB == nil {
		// Without a DB we cannot persist the quarantine
		// row. Fall back to reject so the message does
		// not flow through silently.
		if r.Observability != nil {
			r.Observability.Metrics.IncSMTPRejected()
		}
		return fmt.Errorf("5.7.1 acceptance rule action=quarantine requires DB; rejected: %s", reason)
	}
	if _, err := r.DB.ExecContext(ctx, `INSERT INTO coremail_quarantine_index
		(tenant_id, message_id, recipient, sender, subject, reason, severity, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'high', 'held', ?)`,
		0, session.ID,
		strings.Join(session.Recipients, ","),
		session.MailFrom, extractSubject(rfc822), "acceptance_rule:"+reason,
		time.Now().UTC()); err != nil {
		if r.Observability != nil {
			r.Observability.Metrics.IncSMTPRejected()
		}
		return fmt.Errorf("4.7.1 acceptance rule quarantine failed: %v", err)
	}
	if r.Observability != nil {
		r.Observability.Metrics.IncSMTPRejected()
		r.Observability.EventHistory.Record(observability.EventQuarantineHeld, map[string]string{
			"sender":    session.MailFrom,
			"recipient": strings.Join(session.Recipients, ","),
			"reason":    "acceptance_rule:" + reason,
		})
	}
	return fmt.Errorf("4.7.1 message held by acceptance rule: %s", reason)
}

func (r *Receiver) recordAuthError(event observability.EventType, domain string, err error) {
	if r.Observability == nil || err == nil {
		return
	}
	r.Observability.EventHistory.Record(event, map[string]string{
		"domain": domain,
		"error":  err.Error(),
	})
}

func extractSubject(rfc822 []byte) string {
	lines := bytes.Split(rfc822, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(bytes.ToUpper(trimmed), []byte("SUBJECT:")) {
			return string(bytes.TrimSpace(trimmed[8:]))
		}
	}
	return ""
}

// extractHeaderValue pulls the value of a single top-level
// RFC822 header (case-insensitive name). The function is
// header-section only — it does NOT walk into MIME parts. Used
// by the rules runner to pull From / To / Cc without paying
// for a full MIME parse.
func extractHeaderValue(rfc822 []byte, name string) string {
	prefix := []byte(strings.ToLower(name) + ":")
	lines := bytes.Split(rfc822, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(bytes.ToLower(trimmed), prefix) {
			return strings.TrimSpace(string(trimmed[len(prefix):]))
		}
	}
	return ""
}

// hasAttachmentHint inspects the MIME structure cheaply for
// any multipart/mixed or attachment-shaped part. The rules
// runner uses this to drive has_attachment conditions without
// invoking the MIME extractor (which would be overkill for a
// boolean predicate).
func hasAttachmentHint(rfc822 []byte) bool {
	s := string(rfc822)
	return strings.Contains(strings.ToLower(s), "content-disposition: attachment") ||
		strings.Contains(strings.ToLower(s), "content-type: multipart/mixed") ||
		strings.Contains(strings.ToLower(s), "content-type: multipart/related")
}

func extractHeloDomain(session *Session) string {
	if session.HeloDomain != "" {
		return session.HeloDomain
	}
	return ""
}

func extractRemoteIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		if ip := net.ParseIP(addr); ip != nil {
			return ip
		}
		return nil
	}
	return net.ParseIP(host)
}

func dedupRecipients(recipients []resolvedRecipient) []resolvedRecipient {
	seen := make(map[uint]bool)
	result := make([]resolvedRecipient, 0, len(recipients))
	for _, r := range recipients {
		if seen[r.MailboxID] {
			continue
		}
		seen[r.MailboxID] = true
		result = append(result, r)
	}
	return result
}

func convertSPFAuthResult(ar *spf.AuthResult) *dmarc.AuthResult {
	if ar == nil {
		return nil
	}
	return &dmarc.AuthResult{
		Method:      ar.Method,
		Result:      ar.Result,
		Domain:      ar.Domain,
		Explanation: ar.Explanation,
	}
}
