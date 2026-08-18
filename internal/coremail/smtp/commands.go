package smtp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/observability"
)

// RecipientValidator checks whether a recipient address is valid for local delivery.
type RecipientValidator func(ctx context.Context, address string) (bool, error)

// SenderValidator checks whether an authenticated identity is allowed to send as the given MAIL FROM.
type SenderValidator func(ctx context.Context, identity *AuthIdentity, fromAddress string) (bool, error)

// CommandHandler handles all SMTP commands for a session.
type CommandHandler struct {
	cfg            Config
	auth           *Authenticator
	session        *Session
	authStep       int
	authUser       string
	authPass       string
	validateRcpt   RecipientValidator
	validateSender SenderValidator
	isLocalDomain  func(ctx context.Context, domain string) (bool, error)
	// mailAccessMode returns the domain's policy ("internal_only" or
	// "internal_external"); nil means "policy checking disabled" and
	// handleRCPT skips the check entirely (matching isLocalDomain's
	// nil-means-disabled convention). An empty/unknown return value is
	// treated as internal_external (safe default), never as a reason
	// to reject — only an explicit internal_only blocks external mail.
	//
	// DEPRECATED (MAILBOX-ACCESS-MODE-PHASE1): production wiring uses
	// mailAccessPolicy below. This legacy domain-level checker is kept
	// for backward-compatible test wiring and as the fallback when no
	// canonical policy checker is installed.
	mailAccessMode func(ctx context.Context, domain string) (string, error)
	// mailAccessPolicy is the CANONICAL mailbox-level mail-access
	// policy decision hook (internal/coremail/mailpolicy). When set it
	// REPLACES the legacy domain checker: it classifies the sender by
	// authenticated mailbox identity and the recipient by its
	// effective mailbox mode, so a forged local MAIL FROM from a
	// remote path is never treated as internal. A non-nil error means
	// the policy could not be evaluated and the caller must fail
	// closed with a temporary failure.
	mailAccessPolicy func(ctx context.Context, session *Session, rcptAddr string, rcptIsLocal bool) (allow bool, reason string, err error)
	onAuthEvent      func(eventType string, identity string, detail string)
	// acceptanceEngine applies admin acceptance rules at
	// MAIL FROM + RCPT TO. nil means "no rules configured"
	// and the handler falls back to legacy direct-accept
	// behaviour. The runtime module wires the engine in
	// during initCore so the operator's admin PATCHes
	// take effect on the very next SMTP command.
	acceptanceEngine RuleEvaluator
	Observability    *observability.Observability
	sendEnforceFn    func(ctx context.Context, tenantID, mailboxID uint, count int) error
	authIdentity     *AuthIdentity
}

// NewCommandHandler creates a command handler bound to a session.
// Default isLocalDomain checker returns false (fail-closed: no external relay).
func NewCommandHandler(cfg Config, auth *Authenticator, session *Session) *CommandHandler {
	return &CommandHandler{
		cfg:     cfg,
		auth:    auth,
		session: session,
		isLocalDomain: func(ctx context.Context, domain string) (bool, error) {
			return false, nil
		},
	}
}

// SetAcceptanceEngine wires the optional acceptance &
// routing rule engine. nil clears it.
func (h *CommandHandler) SetAcceptanceEngine(e RuleEvaluator) {
	h.acceptanceEngine = e
}

// SetRecipientValidator sets a function to validate RCPT TO addresses.
func (h *CommandHandler) SetRecipientValidator(v RecipientValidator) {
	h.validateRcpt = v
}

// SetSenderValidator sets a function to validate MAIL FROM against the authenticated identity.
func (h *CommandHandler) SetSenderValidator(v SenderValidator) {
	h.validateSender = v
}

// SetSendEnforcer wires a shared send enforcement callback. Called by
// the runtime module to connect the billing/abuse enforcement service
// to the SMTP command handler. nil disables enforcement.
func (h *CommandHandler) SetSendEnforcer(fn func(ctx context.Context, tenantID, mailboxID uint, count int) error) {
	h.sendEnforceFn = fn
}

// SetLocalDomainChecker sets a function to check if a domain is hosted locally.
func (h *CommandHandler) SetLocalDomainChecker(fn func(ctx context.Context, domain string) (bool, error)) {
	h.isLocalDomain = fn
}

// SetMailAccessModeChecker wires the legacy domain mail-access-mode
// lookup used to enforce internal_only vs internal_external at RCPT
// TO. nil disables the check (no policy restriction beyond existing
// relay protection).
//
// DEPRECATED: production wiring uses SetMailAccessPolicy, which
// enforces the canonical mailbox-level policy. This setter is retained
// for backward-compatible test wiring; when both are set, the policy
// checker takes precedence.
func (h *CommandHandler) SetMailAccessModeChecker(fn func(ctx context.Context, domain string) (string, error)) {
	h.mailAccessMode = fn
}

// SetMailAccessPolicy wires the canonical mailbox-level mail-access
// policy decision hook. When set, it replaces the legacy domain
// checker in handleRCPT. The callback returns (allow, reason, err):
// allow=false with a non-empty reason is a stable policy denial;
// err != nil means the policy could not be evaluated and handleRCPT
// fails closed with a temporary failure.
func (h *CommandHandler) SetMailAccessPolicy(fn func(ctx context.Context, session *Session, rcptAddr string, rcptIsLocal bool) (allow bool, reason string, err error)) {
	h.mailAccessPolicy = fn
}

// SetAuthEventHandler sets a callback for authentication events.
func (h *CommandHandler) SetAuthEventHandler(fn func(eventType string, identity string, detail string)) {
	h.onAuthEvent = fn
}

// Handle processes a parsed SMTP command and returns the response.
func (h *CommandHandler) Handle(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State == StateClosed {
		return ResponseBye
	}

	switch cmd.Verb {
	case "EHLO":
		return h.handleEHLO(ctx, cmd)
	case "HELO":
		return h.handleHELO(ctx, cmd)
	case "MAIL":
		return h.handleMAIL(ctx, cmd)
	case "RCPT":
		return h.handleRCPT(ctx, cmd)
	case "DATA":
		return h.handleDATA(ctx, cmd)
	case "RSET":
		return h.handleRSET(ctx, cmd)
	case "NOOP":
		return h.handleNOOP(ctx, cmd)
	case "QUIT":
		return h.handleQUIT(ctx, cmd)
	case "AUTH":
		return h.handleAUTH(ctx, cmd)
	case "STARTTLS":
		return h.handleSTARTTLS(ctx, cmd)
	default:
		return ResponseCmdUnknown
	}
}

func (h *CommandHandler) handleEHLO(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State != StateNew {
		return ResponseBadSequence
	}
	if cmd.Args != "" {
		h.session.HeloDomain = cmd.Args
	} else {
		h.session.HeloDomain = h.cfg.Hostname
	}

	h.session.State = StateGreeted

	lines := []string{
		h.cfg.Hostname,
	}
	lines = append(lines, h.session.Extensions...)

	return Response{
		Code:    StatusOK,
		Message: strings.Join(lines, "\r\n"),
	}
}

func (h *CommandHandler) handleHELO(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State != StateNew {
		return ResponseBadSequence
	}
	h.session.State = StateGreeted
	if cmd.Args != "" {
		h.session.HeloDomain = cmd.Args
	}
	return Response{StatusOK, h.cfg.Hostname}
}

func (h *CommandHandler) handleMAIL(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State != StateGreeted && h.session.State != StateAuthenticated {
		return ResponseBadSequence
	}
	// TLS policy: reject submission before TLS when required.
	if h.cfg.RequireTLSForSubmission && !h.session.TLSActive {
		return responsef(StatusTLSNotAvailable, "5.7.0 Must issue STARTTLS first")
	}
	if h.session.State == StateGreeted && h.cfg.RequireAuthForSubmission && !h.session.Authenticated {
		return ResponseAuthReq
	}

	address, size, err := ParseMailFrom(cmd.Args)
	if err != nil {
		return responsef(StatusBadArgs, "5.5.2 %s", err.Error())
	}
	if address == "" {
		return responsef(StatusBadArgs, "5.5.2 MAIL FROM address required")
	}
	if size > h.cfg.MaxMessageSizeBytes && h.cfg.MaxMessageSizeBytes > 0 {
		return ResponseSizeExceeded
	}

	// Authorized sender enforcement: authenticated users can only send as themselves.
	if h.session.Authenticated && h.validateSender != nil && h.session.AuthIdentity != nil {
		allowed, err := h.validateSender(ctx, h.session.AuthIdentity, address)
		if err != nil || !allowed {
			if h.onAuthEvent != nil {
				h.onAuthEvent("sender_rejected", h.session.AuthUser, fmt.Sprintf("attempted to send as %s", address))
			}
			return responsef(StatusMailboxNotFound, "5.7.1 Sender not authorized for %s", address)
		}
	}

	h.session.MailFrom = address

	// ── Acceptance rules at MAIL FROM ──────────────────
	// The runtime wires the acceptance engine when
	// coremail_acceptance_rules rows exist. We call it
	// AFTER parsing but BEFORE accepting the sender,
	// so a tenant-scoped "reject"* rule short-circuits
	// the rest of the SMTP transaction.
	if h.acceptanceEngine != nil {
		ok, action, reason := h.acceptanceEngine.Evaluate(ctx, RuleQuery{
			Sender:    address,
			SourceIP:  h.session.RemoteAddr,
			MessageID: h.session.ID,
		})
		if ok {
			switch action {
			case "reject":
				if h.Observability != nil {
					h.Observability.Metrics.IncSMTPRejected()
					h.Observability.EventHistory.Record(observability.EventAcceptanceRuleRejected, map[string]string{
						"sender":    address,
						"recipient": "",
						"reason":    reason,
					})
				}
				h.session.ResetTransaction()
				return responsef(StatusMailboxNotFound, "5.7.1 acceptance rule rejected sender: %s", reason)
			}
		}
	}

	h.session.State = StateMail
	return ResponseOK
}

func (h *CommandHandler) handleRCPT(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State != StateMail && h.session.State != StateRcpt && h.session.State != StateAuthenticated {
		return ResponseBadSequence
	}
	if h.session.MailFrom == "" {
		return ResponseNoFrom
	}
	if len(h.session.Recipients) >= h.cfg.MaxRecipientsPerMessage {
		return responsef(StatusMailboxNotFound, "5.5.3 Too many recipients")
	}

	address, err := ParseRcptTo(cmd.Args)
	if err != nil {
		return responsef(StatusBadArgs, "5.5.2 %s", err.Error())
	}
	if address == "" {
		return ResponseSyntaxErr
	}

	rcptDomain := ExtractDomain(address)
	var rcptIsLocal bool
	if rcptDomain != "" && h.isLocalDomain != nil {
		rcptIsLocal, _ = h.isLocalDomain(ctx, rcptDomain) // errors fail closed (treated as non-local) in every caller below
	}

	// Relay protection: unauthenticated users cannot send to external domains.
	// Fail-closed: if the local domain checker errors, relay is denied.
	if !h.session.Authenticated {
		if rcptDomain != "" {
			isLocal, err := h.isLocalDomain(ctx, rcptDomain)
			if err != nil || !isLocal {
				if h.onAuthEvent != nil {
					h.onAuthEvent("relay_denied", "", fmt.Sprintf("unauthenticated relay to %s", rcptDomain))
				}
				return ResponseNoRelay
			}
		}
	}

	// mail_access_mode enforcement — real delivery-path policy, not
	// just a CRUD flag.
	//
	// CANONICAL PATH (MAILBOX-ACCESS-MODE-PHASE1): when the
	// mailbox-level policy checker is wired it REPLACES the legacy
	// domain checker below. It classifies the sender by authenticated
	// mailbox identity (never by MAIL FROM domain alone), evaluates
	// the recipient's EFFECTIVE mailbox mode, and fails closed with a
	// temporary failure when the policy cannot be evaluated.
	if h.mailAccessPolicy != nil {
		allow, reason, perr := h.mailAccessPolicy(ctx, h.session, address, rcptIsLocal)
		if perr != nil {
			if h.onAuthEvent != nil {
				h.onAuthEvent("policy_unavailable", h.session.AuthUser,
					fmt.Sprintf("mail access policy unavailable for %s", rcptDomain))
			}
			return Response{StatusServiceNotAvailable, "4.7.0 Mail access policy temporarily unavailable"}
		}
		if !allow {
			if h.onAuthEvent != nil {
				h.onAuthEvent("policy_denied", h.session.AuthUser,
					fmt.Sprintf("mail access policy denied %s -> %s (%s)", h.session.MailFrom, address, reason))
			}
			return Response{StatusMailboxNotFound, "5.7.1 " + reason}
		}
	} else if h.mailAccessMode != nil && h.isLocalDomain != nil {
		// LEGACY DOMAIN-LEVEL CHECK (deprecated, backward compatible).
		senderDomain := ExtractDomain(h.session.MailFrom)
		if rcptDomain != "" && rcptIsLocal {
			mode, merr := h.mailAccessMode(ctx, rcptDomain)
			if merr == nil && mode == string(coremail.MailAccessInternalOnly) {
				senderIsLocal := false
				if senderDomain != "" {
					senderIsLocal, _ = h.isLocalDomain(ctx, senderDomain)
				}
				if !senderIsLocal {
					if h.onAuthEvent != nil {
						h.onAuthEvent("policy_denied", h.session.AuthUser,
							fmt.Sprintf("external inbound to internal-only domain %s blocked (sender %s)", rcptDomain, h.session.MailFrom))
					}
					return ResponseExternalInboundBlocked
				}
			}
		} else if senderDomain != "" {
			senderIsLocal, lerr := h.isLocalDomain(ctx, senderDomain)
			if lerr == nil && senderIsLocal {
				mode, merr := h.mailAccessMode(ctx, senderDomain)
				if merr == nil && mode == string(coremail.MailAccessInternalOnly) {
					if h.onAuthEvent != nil {
						h.onAuthEvent("policy_denied", h.session.AuthUser,
							fmt.Sprintf("external outbound from internal-only domain %s blocked (recipient %s)", senderDomain, address))
					}
					return ResponseExternalOutboundBlocked
				}
			}
		}
	}

	// Validate recipient if a validator is configured and session is not
	// authenticated (authenticated submission allows external recipients).
	if h.validateRcpt != nil && !h.session.Authenticated {
		valid, err := h.validateRcpt(ctx, address)
		if err != nil || !valid {
			if err != nil {
				return responsef(StatusMailboxNotFound, "5.1.1 %s: %v", address, err)
			}
			return responsef(StatusMailboxNotFound, "5.1.1 %s: User unknown", address)
		}
	}

	// ── Acceptance rules at RCPT TO ─────────────────────
	// Run the acceptance engine against each RCPT TO so
	// per-recipient rules can reject without aborting the
	// whole transaction. The Match engine returns
	// "accept" / "reject" / "quarantine" — only reject
	// and quarantine short-circuit; accept allows the
	// RCPT TO to succeed.
	if h.acceptanceEngine != nil {
		ok, action, reason := h.acceptanceEngine.Evaluate(ctx, RuleQuery{
			Sender:    h.session.MailFrom,
			Recipient: address,
			SourceIP:  h.session.RemoteAddr,
			MessageID: h.session.ID,
		})
		if ok && (action == "reject" || action == "quarantine") {
			if h.Observability != nil {
				h.Observability.Metrics.IncSMTPRejected()
				h.Observability.EventHistory.Record(observability.EventAcceptanceRuleRejected, map[string]string{
					"sender":    h.session.MailFrom,
					"recipient": address,
					"reason":    reason,
				})
			}
			return responsef(StatusMailboxNotFound, "5.7.1 acceptance rule rejected recipient: %s", reason)
		}
	}

	h.session.Recipients = append(h.session.Recipients, address)
	h.session.State = StateRcpt

	return ResponseOK
}

func (h *CommandHandler) handleDATA(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.State != StateRcpt && h.session.State != StateMail && h.session.State != StateAuthenticated {
		return ResponseBadSequence
	}
	if h.session.MailFrom == "" {
		return ResponseNoFrom
	}
	if len(h.session.Recipients) == 0 {
		return ResponseNoRecipient
	}

	if h.sendEnforceFn != nil && h.session.AuthIdentity != nil {
		if err := h.sendEnforceFn(ctx, h.session.AuthIdentity.TenantID, h.session.AuthIdentity.MailboxID, len(h.session.Recipients)); err != nil {
			return Response{StatusServiceNotAvailable, "4.7.0 " + err.Error()}
		}
	}

	h.session.State = StateData
	return ResponseStartData
}

func (h *CommandHandler) handleRSET(ctx context.Context, cmd *ParsedCommand) Response {
	h.session.ResetTransaction()
	return ResponseOK
}

func (h *CommandHandler) handleNOOP(ctx context.Context, cmd *ParsedCommand) Response {
	return ResponseOK
}

func (h *CommandHandler) handleQUIT(ctx context.Context, cmd *ParsedCommand) Response {
	h.session.State = StateClosed
	return ResponseBye
}

func (h *CommandHandler) handleAUTH(ctx context.Context, cmd *ParsedCommand) Response {
	// Auth disabled on this listener (e.g. inbound port 25).
	if h.cfg.DisableAuth {
		return ResponseCmdUnknown
	}
	// TLS policy: reject AUTH before TLS when required.
	if h.cfg.RequireTLSForAuth && !h.session.TLSActive {
		return responsef(StatusTLSNotAvailable, "5.7.0 Must issue STARTTLS first")
	}
	if !h.cfg.AllowPlainAuthWithoutTLS && !h.session.TLSActive && !h.cfg.RequireTLSForAuth {
		return responsef(StatusTLSNotAvailable, "5.7.0 Must issue STARTTLS first")
	}

	args := strings.ToUpper(cmd.Args)

	if strings.HasPrefix(args, "PLAIN") {
		encoded := strings.TrimSpace(cmd.Args[5:])
		var result AuthResult

		if encoded != "" {
			result = h.auth.HandleAuthPlain(ctx, encoded)
		}
		if !result.Success {
			if h.onAuthEvent != nil {
				h.onAuthEvent("auth_failed", cmd.Args, "invalid credentials")
			}
			if h.Observability != nil {
				h.Observability.Metrics.IncAuthFailure()
				h.Observability.EventHistory.Record(observability.EventSMTPAuthFailure, map[string]string{
					"method": "PLAIN", "detail": "invalid credentials",
				})
			}
			return ResponseAuthFail
		}
		h.session.AuthUser = result.Username
		h.session.AuthIdentity = result.Identity
		h.session.Authenticated = true
		h.session.State = StateAuthenticated
		if h.onAuthEvent != nil {
			h.onAuthEvent("auth_success", result.Username, "PLAIN")
		}
		if h.Observability != nil {
			h.Observability.Metrics.IncAuthSuccess()
			h.Observability.EventHistory.Record(observability.EventSMTPAuthSuccess, map[string]string{
				"identity": result.Username, "method": "PLAIN",
			})
		}
		return ResponseAuthSuccess
	}

	// AUTH LOGIN
	if strings.HasPrefix(args, "LOGIN") {
		h.authStep = 1
		_, challenge, _ := h.auth.HandleAuthLogin(ctx, 0, "")
		return Response{StatusAuthChallenge, challenge}
	}

	return responsef(StatusParamNotImplemented, "5.5.4 Unsupported authentication mechanism")
}

// HandleAuthLoginStep handles the next step of AUTH LOGIN.
func (h *CommandHandler) HandleAuthLoginStep(ctx context.Context, data string) Response {
	if h.cfg.DisableAuth {
		h.authStep = 0
		return ResponseCmdUnknown
	}
	switch h.authStep {
	case 1:
		// Received username, ask for password.
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			h.authStep = 0
			return ResponseAuthFail
		}
		h.authUser = string(decoded)
		h.authStep = 2
		challenge := base64.StdEncoding.EncodeToString([]byte("Password:"))
		return Response{StatusAuthChallenge, challenge}
	case 2:
		// Received password, verify.
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			h.authStep = 0
			return ResponseAuthFail
		}
		h.authPass = string(decoded)
		h.authStep = 0

		result := h.auth.HandleAuthPlain(ctx, CreateAuthPlainResponse(h.authUser, h.authPass))
		if !result.Success {
			if h.onAuthEvent != nil {
				h.onAuthEvent("auth_failed", h.authUser, "invalid credentials")
			}
			if h.Observability != nil {
				h.Observability.Metrics.IncAuthFailure()
				h.Observability.EventHistory.Record(observability.EventSMTPAuthFailure, map[string]string{
					"method": "LOGIN", "identity": h.authUser,
				})
			}
			return ResponseAuthFail
		}
		h.session.AuthUser = result.Username
		h.session.AuthIdentity = result.Identity
		h.session.Authenticated = true
		h.session.State = StateAuthenticated
		if h.onAuthEvent != nil {
			h.onAuthEvent("auth_success", result.Username, "LOGIN")
		}
		if h.Observability != nil {
			h.Observability.Metrics.IncAuthSuccess()
			h.Observability.EventHistory.Record(observability.EventSMTPAuthSuccess, map[string]string{
				"identity": result.Username, "method": "LOGIN",
			})
		}
		return ResponseAuthSuccess
	default:
		h.authStep = 0
		return ResponseBadSequence
	}
}

func (h *CommandHandler) handleSTARTTLS(ctx context.Context, cmd *ParsedCommand) Response {
	if h.session.TLSActive {
		return ResponseBadSequence
	}
	if h.cfg.ImplicitTLS {
		return responsef(StatusTLSNotAvailable, "5.7.0 TLS already active")
	}
	if h.session.TLSConfig == nil {
		return responsef(StatusTLSNotAvailable, "5.7.0 TLS not available")
	}

	// The server layer must handle the TLS handshake after this response.
	return Response{StatusReady, "2.0.0 Ready to start TLS"}
}
