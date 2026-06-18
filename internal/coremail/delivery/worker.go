package delivery

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// DeliveryWorker processes queue entries and delivers messages through the
// fully integrated pipeline: policy → loop detection → transport → history → audit → metrics.
type DeliveryWorker struct {
	Queue       *queue.QueueEngine
	MailStore   *storage.MailStore
	Resolver    Resolver
	Transport   *SMTPTransport
	HeloHost    string
	WorkerID    string
	LocalDomain string

	// Reliability integrations.
	RetryPolicy  RetryPolicy
	History      AttemptHistoryRepository
	Audit        *AuditLogger
	Policy       *PolicyEnforcer
	LoopDetector *LoopDetector
	Metrics      *ReliabilityMetrics
	Recovery     *WorkerCrashRecovery
	Shutdown     *ShutdownManager

	// DKIM signing integration (optional).
	DKIMSigner  *dkim.Signer
	DKIMConfigs dkim.Repository

	// Observability (optional).
	Observability *observability.Observability
}

// NewDeliveryWorker creates a delivery worker with optional reliability integrations.
// Pass nil for any integration to disable it.
func NewDeliveryWorker(qe *queue.QueueEngine, ms *storage.MailStore, resolver Resolver, transport *SMTPTransport, localDomain, workerID string) *DeliveryWorker {
	w := &DeliveryWorker{
		Queue:       qe,
		MailStore:   ms,
		Resolver:    resolver,
		Transport:   transport,
		HeloHost:    localDomain,
		WorkerID:    workerID,
		LocalDomain: localDomain,
	}
	w.setDefaults()
	return w
}

func (w *DeliveryWorker) setDefaults() {
	if w.RetryPolicy.MaxAttempts == 0 {
		w.RetryPolicy = DefaultRetryPolicy()
	}
	if w.Policy == nil {
		w.Policy = NewPolicyEnforcer(DefaultDeliveryPolicy())
	}
	if w.Metrics == nil {
		w.Metrics = &ReliabilityMetrics{}
	}
	if w.Recovery == nil && w.Queue != nil {
		w.Recovery = NewWorkerCrashRecovery(w.Queue, w.WorkerID, 300)
	}
	if w.Shutdown == nil {
		w.Shutdown = NewShutdownManager()
	}
}

// ProcessOnce integrates the full reliability pipeline:
// policy → loop detection → delivery → history → audit → metrics.
func (w *DeliveryWorker) ProcessOnce(ctx context.Context) (bool, error) {
	if !w.Shutdown.BeginJob() {
		return false, nil
	}
	defer w.Shutdown.EndJob()

	entry, err := w.Queue.LeaseNext(ctx, w.WorkerID)
	if err != nil {
		return false, fmt.Errorf("lease: %w", err)
	}
	if entry == nil {
		return false, nil
	}

	// Audit: leased.
	if w.Audit != nil {
		w.Audit.RecordEvent(ctx, BuildEvent(entry.ID, entry.MessageID, entry.FromAddress, entry.ToAddress, w.WorkerID, string(entry.Direction), EventLeased))
	}

	return true, w.deliver(ctx, entry)
}

// ProcessAll processes all available queue entries.
func (w *DeliveryWorker) ProcessAll(ctx context.Context) (int, error) {
	if w.Shutdown.IsShutdown() {
		return 0, nil
	}
	count := 0
	for {
		worked, err := w.ProcessOnce(ctx)
		if err != nil {
			return count, err
		}
		if !worked {
			break
		}
		count++
	}
	return count, nil
}

func (w *DeliveryWorker) deliver(ctx context.Context, entry *queue.QueueEntry) error {
	attemptNumber := entry.AttemptCount + 1

	// 1. Policy check.
	if !w.checkPolicy(ctx, entry, attemptNumber) {
		return nil // policy rejection already handled (audit + queue update)
	}

	// 2. Loop detection.
	if !w.checkLoops(ctx, entry, attemptNumber) {
		return nil // loop already handled
	}

	// 3. Load message for size check and delivery.
	msg, data, err := w.MailStore.LoadMessageByMessageID(ctx, entry.MessageID)
	if err != nil {
		return w.failPermanent(ctx, entry, attemptNumber, "load message", err.Error())
	}
	if msg == nil {
		return w.failPermanent(ctx, entry, attemptNumber, "message not found", "")
	}

	// 4. Size policy check.
	if pr := w.Policy.CheckMessageSize(int64(len(data))); !pr.Allowed {
		return w.failPolicy(ctx, entry, attemptNumber, pr.Reason, pr.Code)
	}

	// 5. Recipient count policy check.
	rcpts := strings.Split(entry.ToAddress, ",")
	if pr := w.Policy.CheckRecipients(len(rcpts)); !pr.Allowed {
		return w.failPolicy(ctx, entry, attemptNumber, pr.Reason, pr.Code)
	}

	// 6. Attempt delivery.
	startTime := time.Now()
	isLocal := strings.EqualFold(entry.RecipientDomain, w.LocalDomain) ||
		entry.DeliveryMode == queue.DeliveryLocal

	var result *DeliveryResult
	if isLocal {
		result = w.deliverLocal(ctx, entry)
	} else {
		result = w.deliverRemote(ctx, entry)
	}
	result.DurationMs = time.Since(startTime).Milliseconds()

	// 7. Record attempt history.
	w.recordAttempt(ctx, entry, attemptNumber, result)

	// 8. Classify result using retry policy (with entry-level max attempts).
	decisionPolicy := w.RetryPolicy
	if entry.MaxAttempts > 0 && entry.MaxAttempts < decisionPolicy.MaxAttempts {
		decisionPolicy.MaxAttempts = entry.MaxAttempts
	}
	decision, nextAttempt, err := decisionPolicy.ClassifyResult(result, attemptNumber)
	if err != nil {
		return fmt.Errorf("classify: %w", err)
	}

	switch decision {
	case DecisionDelivered:
		w.emitAudit(ctx, entry, EventDelivered, result)
		w.Metrics.RecordDelivery(result.DurationMs)
		w.recordDeliveryEvent(ctx, entry, observability.EventQueueDelivered, result)
		return w.Queue.AckDelivered(ctx, entry.ID)

	case DecisionRetry:
		w.emitAudit(ctx, entry, EventDeferred, result)
		w.Metrics.RecordDeferral()
		w.Metrics.RecordRetry(attemptNumber)
		w.recordDeliveryEvent(ctx, entry, observability.EventQueueDeferred, result)
		return w.Queue.Repo.Defer(ctx, entry.ID, nextAttempt, result.StatusMsg, nil)

	case DecisionDeadLetter:
		maxAllowed := w.RetryPolicy.MaxAttempts
		if maxAllowed <= 0 || entry.MaxAttempts < maxAllowed {
			maxAllowed = entry.MaxAttempts
		}
		if !result.TempFail && attemptNumber < maxAllowed {
			w.emitAudit(ctx, entry, EventBounced, result)
			w.Metrics.RecordBounce()
			w.recordDeliveryEvent(ctx, entry, observability.EventQueueBounced, result)
			return w.Queue.Repo.Bounce(ctx, entry.ID, result.StatusMsg, nil)
		}
		w.emitAudit(ctx, entry, EventDeadLetter, result)
		w.Metrics.RecordDeadLetter()
		w.recordDeliveryEvent(ctx, entry, observability.EventQueueDeadLetter, result)
		return w.Queue.Repo.DeadLetter(ctx, entry.ID, result.StatusMsg, nil)
	}

	return nil
}

// checkPolicy runs all policy checks. On failure, it audits, records history, and updates the queue.
func (w *DeliveryWorker) checkPolicy(ctx context.Context, entry *queue.QueueEntry, attemptNumber int) bool {
	pr := w.Policy.CheckSender(ctx, entry.FromAddress, &entry.DomainID, entry.MailboxID, &entry.TenantID, 0)
	if !pr.Allowed {
		w.recordAttempt(ctx, entry, attemptNumber, &DeliveryResult{StatusCode: pr.Code, StatusMsg: pr.Reason, TempFail: false})
		w.emitAudit(ctx, entry, EventPolicyRejected, &DeliveryResult{StatusMsg: pr.Reason, StatusCode: pr.Code})
		w.Metrics.RecordBounce()
		w.Queue.Repo.Bounce(ctx, entry.ID, pr.Reason, nil)
		return false
	}

	pr2 := w.Policy.CheckDomain(ctx, entry.RecipientDomain, 0, 0)
	if !pr2.Allowed {
		w.recordAttempt(ctx, entry, attemptNumber, &DeliveryResult{StatusCode: pr2.Code, StatusMsg: pr2.Reason, TempFail: false})
		w.emitAudit(ctx, entry, EventPolicyRejected, &DeliveryResult{StatusMsg: pr2.Reason, StatusCode: pr2.Code})
		w.Metrics.RecordBounce()
		w.Queue.Repo.Bounce(ctx, entry.ID, pr2.Reason, nil)
		return false
	}

	return true
}

// checkLoops runs all loop detection checks.
func (w *DeliveryWorker) checkLoops(ctx context.Context, entry *queue.QueueEntry, attemptNumber int) bool {
	if w.LoopDetector == nil {
		return true
	}
	// Check deferral loop.
	if lr := w.LoopDetector.CheckDeferralLoop(attemptNumber, entry.MaxAttempts); lr.IsLoop {
		return w.handleLoop(ctx, entry, attemptNumber, lr.Reason)
	}
	// Check self-delivery.
	if lr := w.LoopDetector.CheckSelfDelivery(entry.RecipientDomain); lr.IsLoop {
		return w.handleLoop(ctx, entry, attemptNumber, lr.Reason)
	}
	return true
}

func (w *DeliveryWorker) handleLoop(ctx context.Context, entry *queue.QueueEntry, attemptNumber int, reason string) bool {
	w.recordAttempt(ctx, entry, attemptNumber, &DeliveryResult{StatusMsg: reason, StatusCode: 550, TempFail: false})
	w.emitAudit(ctx, entry, EventLoopDetected, &DeliveryResult{StatusMsg: reason, StatusCode: 550})
	w.Metrics.RecordBounce()
	w.Queue.Repo.Bounce(ctx, entry.ID, reason, nil)
	return false
}

func (w *DeliveryWorker) failPermanent(ctx context.Context, entry *queue.QueueEntry, attemptNumber int, tag, detail string) error {
	msg := detail
	if msg == "" {
		msg = tag
	}
	w.recordAttempt(ctx, entry, attemptNumber, &DeliveryResult{StatusMsg: msg, TempFail: false})
	w.emitAudit(ctx, entry, EventBounced, &DeliveryResult{StatusMsg: msg})
	w.Metrics.RecordBounce()
	return w.Queue.Repo.Bounce(ctx, entry.ID, msg, nil)
}

func (w *DeliveryWorker) failPolicy(ctx context.Context, entry *queue.QueueEntry, attemptNumber int, reason string, code int) error {
	w.recordAttempt(ctx, entry, attemptNumber, &DeliveryResult{StatusCode: code, StatusMsg: reason, TempFail: false})
	w.emitAudit(ctx, entry, EventPolicyRejected, &DeliveryResult{StatusCode: code, StatusMsg: reason})
	w.Metrics.RecordBounce()
	return w.Queue.Repo.Bounce(ctx, entry.ID, reason, nil)
}

// recordAttempt persists a delivery attempt to the history repository.
func (w *DeliveryWorker) recordAttempt(ctx context.Context, entry *queue.QueueEntry, attemptNumber int, result *DeliveryResult) {
	if w.History == nil {
		return
	}
	a := &DeliveryAttempt{
		QueueEntryID:  entry.ID,
		AttemptNumber: attemptNumber,
		Status:        outcomeString(result),
		RemoteHost:    result.RemoteHost,
		RemoteIP:      result.RemoteIP,
		StatusCode:    result.StatusCode,
		StatusMsg:     result.StatusMsg,
		EnhancedCode:  result.EnhancedCode,
		DurationMs:    result.DurationMs,
		TLSUsed:       result.TLSUsed,
		WorkerID:      w.WorkerID,
	}
	w.History.RecordAttempt(ctx, a, nil)
}

// emitAudit records a delivery audit event.
func (w *DeliveryWorker) emitAudit(ctx context.Context, entry *queue.QueueEntry, eventType DeliveryEventType, result *DeliveryResult) {
	if w.Audit == nil {
		return
	}
	e := BuildEvent(entry.ID, entry.MessageID, entry.FromAddress, entry.ToAddress, w.WorkerID, string(entry.Direction), eventType)
	if result != nil {
		e.StatusCode = result.StatusCode
		e.StatusMsg = result.StatusMsg
		e.EnhancedCode = result.EnhancedCode
		e.RemoteHost = result.RemoteHost
		e.RemoteIP = result.RemoteIP
	}
	w.Audit.RecordEvent(ctx, e)
}

func outcomeString(res *DeliveryResult) string {
	if res == nil {
		return "unknown"
	}
	if res.Success {
		return "delivered"
	}
	if res.TempFail {
		return "deferred"
	}
	return "bounced"
}

// signWithDKIM signs the message with DKIM if the sender domain has a configured
// and enabled DKIM key. Returns the data with DKIM-Signature header prepended,
// or the original data unchanged if signing is not possible or already present.
func (w *DeliveryWorker) signWithDKIM(ctx context.Context, data []byte, entry *queue.QueueEntry) []byte {
	if w.DKIMSigner == nil || w.DKIMConfigs == nil {
		w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignSkipped, "signer or repo not configured")
		return data
	}

	senderDomain := extractDomainFromAddress(entry.FromAddress)
	if senderDomain == "" {
		w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignSkipped, "no sender domain")
		return data
	}

	if bytes.HasPrefix(data, []byte("DKIM-Signature:")) || bytes.Contains(data, []byte("\nDKIM-Signature:")) {
		w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignSkipped, "already signed")
		return data
	}

	cfg, err := w.DKIMConfigs.GetByDomain(ctx, senderDomain, nil)
	if err != nil || cfg == nil || !cfg.Enabled {
		w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignSkipped, "no enabled config for domain")
		return data
	}

	hs := dkim.HeaderSet{
		Domain:        cfg.Domain,
		Selector:      cfg.Selector,
		PrivateKeyPEM: cfg.PrivateKeyPEM,
		SignedHeaders: dkim.DefaultHeaders,
		HeaderCanon:   dkim.CanonRelaxed,
		BodyCanon:     dkim.CanonRelaxed,
	}

	result, err := w.DKIMSigner.Sign(data, hs)
	if err != nil || result == nil || result.Signature == "" {
		w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignFailure,
			fmt.Sprintf("sign failed: %v", err))
		return data
	}

	w.recordDKIMEvent(ctx, entry, observability.EventDKIMSignSuccess, cfg.Domain)

	sigHeader := []byte("DKIM-Signature: " + result.Signature + "\r\n")
	return append(sigHeader, data...)
}

func (w *DeliveryWorker) recordDeliveryEvent(ctx context.Context, entry *queue.QueueEntry, typ observability.EventType, result *DeliveryResult) {
	if w.Observability == nil {
		return
	}
	w.Observability.Metrics.IncQueueDelivered()
	_ = typ
	fields := map[string]string{
		"queue_id":   fmt.Sprintf("%d", entry.ID),
		"message_id": entry.MessageID,
		"sender":     entry.FromAddress,
		"recipient":  entry.ToAddress,
		"domain":     entry.RecipientDomain,
	}
	if result != nil {
		fields["status"] = result.StatusMsg
	}
	w.Observability.EventHistory.Record(typ, fields)
}

func (w *DeliveryWorker) recordDKIMEvent(ctx context.Context, entry *queue.QueueEntry, typ observability.EventType, detail string) {
	if w.Observability == nil {
		return
	}
	switch typ {
	case observability.EventDKIMSignSuccess:
		w.Observability.Metrics.IncDKIMSigned()
	case observability.EventDKIMSignSkipped:
		w.Observability.Metrics.IncDKIMSkipped()
	case observability.EventDKIMSignFailure:
		w.Observability.Metrics.IncDKIMFailed()
	}
	w.Observability.EventHistory.Record(typ, map[string]string{
		"queue_id":   fmt.Sprintf("%d", entry.ID),
		"message_id": entry.MessageID,
		"sender":     entry.FromAddress,
		"detail":     detail,
	})
}

func extractDomainFromAddress(addr string) string {
	idx := strings.LastIndexByte(addr, '@')
	if idx < 0 {
		return ""
	}
	return addr[idx+1:]
}

// ── Delivery implementations ─────────────────────────────────

func (w *DeliveryWorker) deliverLocal(ctx context.Context, entry *queue.QueueEntry) *DeliveryResult {
	mboxID := entry.MailboxID
	if mboxID == nil {
		return &DeliveryResult{StatusMsg: "no mailbox id", TempFail: false}
	}
	folder, err := w.MailStore.Folders.GetByPath(ctx, *mboxID, "INBOX", nil)
	if err != nil || folder == nil {
		return &DeliveryResult{StatusMsg: fmt.Sprintf("inbox lookup: %v", err), TempFail: true}
	}
	msg, data, err := w.MailStore.LoadMessageByMessageID(ctx, entry.MessageID)
	if err != nil {
		return &DeliveryResult{StatusMsg: fmt.Sprintf("load message: %v", err), TempFail: false}
	}
	if msg == nil {
		return &DeliveryResult{StatusMsg: "message not found", TempFail: false}
	}
	newMsg := *msg
	newMsg.ID = 0
	newMsg.MessageID = storage.GenerateMessageID()
	newMsg.MailboxID = *mboxID
	newMsg.FolderID = folder.ID
	newMsg.RFC822Path = ""
	newMsg.Seen = false
	if err := w.MailStore.StoreMessage(ctx, &newMsg, data, nil); err != nil {
		return &DeliveryResult{StatusMsg: fmt.Sprintf("store for local: %v", err), TempFail: true}
	}
	return &DeliveryResult{Success: true}
}

func (w *DeliveryWorker) deliverRemote(ctx context.Context, entry *queue.QueueEntry) *DeliveryResult {
	domain := entry.RecipientDomain
	mxRecords, err := w.Resolver.LookupMX(ctx, domain)
	if err != nil {
		return &DeliveryResult{StatusMsg: fmt.Sprintf("mx lookup: %v", err), TempFail: true}
	}
	msg, data, err := w.MailStore.LoadMessageByMessageID(ctx, entry.MessageID)
	if err != nil {
		return &DeliveryResult{StatusMsg: fmt.Sprintf("load message: %v", err), TempFail: false}
	}
	if msg == nil {
		return &DeliveryResult{StatusMsg: "message not found", TempFail: false}
	}

	// ── DKIM Signing (if configured) ──────────────────────
	data = w.signWithDKIM(ctx, data, entry)

	var lastTemp *DeliveryResult
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")
		if hasExplicitPort(host) {
			result := w.Transport.Deliver(ctx, host, false, entry.FromAddress, []string{entry.ToAddress}, data, w.HeloHost)
			result.RemoteHost = host
			result.RemoteIP = host
			if result.Success || !result.TempFail {
				return result
			}
			lastTemp = result
			continue
		}
		addrs, err := w.Resolver.LookupHost(ctx, host)
		if err != nil {
			lastTemp = &DeliveryResult{
				StatusMsg:  fmt.Sprintf("host lookup %s: %v", host, err),
				TempFail:   true,
				RemoteHost: host,
			}
			continue
		}
		for _, ip := range addrs {
			addr := smtpDialAddress(ip, "25")
			result := w.Transport.Deliver(ctx, addr, false, entry.FromAddress, []string{entry.ToAddress}, data, w.HeloHost)
			result.RemoteHost = host
			result.RemoteIP = ip
			if result.Success || !result.TempFail {
				return result
			}
			lastTemp = result
		}
	}
	if lastTemp != nil {
		return lastTemp
	}
	return &DeliveryResult{StatusMsg: fmt.Sprintf("all mx hosts failed for %s", domain), TempFail: true}
}

func smtpDialAddress(host, port string) string {
	if hasExplicitPort(host) {
		return host
	}
	return net.JoinHostPort(host, port)
}

func hasExplicitPort(host string) bool {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return true
	}
	colon := strings.LastIndex(host, ":")
	if colon <= 0 || strings.Count(host, ":") != 1 || colon == len(host)-1 {
		return false
	}
	_, err := strconv.Atoi(host[colon+1:])
	return err == nil
}
