package smtp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/antispam"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/dmarc"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/spf"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// Receiver handles message acceptance, MailStore persistence, and Queue enqueue.
// If SPFEvaluator and DMARCEvaluator are non-nil, SPF and DMARC evaluation
// are performed during message acceptance and auth headers are injected.
type Receiver struct {
	Engine          *coremail.Engine
	MailStore       *storage.MailStore
	QueueEngine     *queue.QueueEngine
	Config          Config
	SPFEvaluator    *spf.Evaluator
	DMARCEvaluator  *dmarc.Evaluator
	AntiSpamEngine  *antispam.Engine
	DKIMVerifier    *dkim.Verifier
	Observability   *observability.Observability
}

// NewReceiver creates a message receiver.
func NewReceiver(eng *coremail.Engine, ms *storage.MailStore, qe *queue.QueueEngine, cfg Config) *Receiver {
	return &Receiver{
		Engine:      eng,
		MailStore:   ms,
		QueueEngine: qe,
		Config:      cfg,
	}
}

// AcceptMessage processes a completed DATA transfer.
// Steps:
// 1. Evaluate SPF (if configured)
// 2. Evaluate DKIM (if configured)
// 3. Evaluate DMARC (if configured)
// 4. Anti-spam assessment
// 5. Enforce spam policy (reject if in enforcement mode)
// 6. Inject Received-SPF, Authentication-Results, X-Orvix-Spam headers
// 7. Parse the destination domain(s)
// 8. Validate recipients exist locally
// 9. Store message in each recipient's MailStore
// 10. Enqueue for local delivery
func (r *Receiver) AcceptMessage(ctx context.Context, session *Session) error {
	rfc822Data := session.DataBuffer

	if len(rfc822Data) == 0 {
		return fmt.Errorf("empty message body")
	}

	if int64(len(rfc822Data)) > r.Config.MaxMessageSizeBytes {
		return fmt.Errorf("message too large: %d > %d", len(rfc822Data), r.Config.MaxMessageSizeBytes)
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
			ConnectingIP:    connectingIP,
			HeloDomain:      heloDomain,
			MailFromDomain:  mailFromDomain,
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

	// ── Recipient resolution ───────────────────────────────
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
			if purgeErr := r.MailStore.PurgeMessage(ctx, msg.ID, nil); purgeErr != nil {
				return fmt.Errorf("store succeeded but queue failed (%v) AND purge failed (%v): %v",
					err, purgeErr, err)
			}
			return fmt.Errorf("enqueue for %s: %w", rcpt.Email, err)
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
