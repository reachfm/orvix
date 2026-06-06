package stalwart

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/firewall"
	"github.com/orvix/orvix/internal/guardian"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// EventProcessor handles Stalwart webhook events and dispatches to modules.
type EventProcessor struct {
	db       *gorm.DB
	logger   *zap.Logger
	firewall *firewall.Pipeline
	guardian *guardian.Agent
	security *auth.SecurityMonitor
}

// NewEventProcessor creates a new event processor.
func NewEventProcessor(db *gorm.DB, logger *zap.Logger, fw *firewall.Pipeline, gd *guardian.Agent, sec *auth.SecurityMonitor) *EventProcessor {
	return &EventProcessor{
		db:       db,
		logger:   logger,
		firewall: fw,
		guardian: gd,
		security: sec,
	}
}

// EmailReceivedPayload contains data from a received email webhook.
type EmailReceivedPayload struct {
	MessageID   string `json:"messageId"`
	SenderIP    string `json:"senderIp"`
	Sender      string `json:"sender"`
	Recipient   string `json:"recipient"`
	Subject     string `json:"subject"`
	Size        int64  `json:"size"`
	SPFResult   string `json:"spfResult"`
	DKIMResult  string `json:"dkimResult"`
	DMARCResult string `json:"dmarcResult"`
	Domain      string `json:"domain"`
}

// EmailSentPayload contains data from a sent email webhook.
type EmailSentPayload struct {
	MessageID string `json:"messageId"`
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Domain    string `json:"domain"`
	Size      int64  `json:"size"`
	Status    string `json:"status"`
}

// BouncePayload contains bounce event data.
type BouncePayload struct {
	MessageID string `json:"messageId"`
	Recipient string `json:"recipient"`
	Reason    string `json:"reason"`
	Status    string `json:"status"`
}

// AuthFailurePayload contains auth failure event data.
type AuthFailurePayload struct {
	Username  string `json:"username"`
	IP        string `json:"ip"`
	Protocol  string `json:"protocol"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// HandleEmailReceived processes an incoming email event.
func (ep *EventProcessor) HandleEmailReceived(payload EmailReceivedPayload) {
	ep.logger.Info("email received event",
		zap.String("message_id", payload.MessageID),
		zap.String("sender", payload.Sender),
		zap.String("recipient", payload.Recipient),
	)

	if ep.firewall != nil {
		emailCtx := &firewall.EmailContext{
			MessageID:    payload.MessageID,
			SenderIP:     payload.SenderIP,
			SenderDomain: extractDomain(payload.Sender),
			Recipient:    payload.Recipient,
			Subject:      payload.Subject,
			SPFResult:    payload.SPFResult,
			DKIMResult:   payload.DKIMResult,
			DMARCResult:  payload.DMARCResult,
			ReceivedAt:   time.Now(),
		}
		verdict, err := ep.firewall.Process(nil, emailCtx)
		if err == nil && verdict.Action != "pass" {
			ep.logger.Warn("firewall blocked email",
				zap.String("message_id", payload.MessageID),
				zap.String("action", verdict.Action),
			)

			ep.db.Create(&models.FirewallLog{
				IP:          payload.SenderIP,
				Domain:      payload.Domain,
				Sender:      payload.Sender,
				Recipient:   payload.Recipient,
				Action:      verdict.Action,
				Reason:      joinStrings(verdict.Reasons),
				ThreatScore: verdict.TotalScore,
			})

			if ep.guardian != nil {
				result, _ := ep.guardian.Analyze(nil, &guardian.AnalyzeRequest{
					EmailID:      payload.MessageID,
					SenderIP:     payload.SenderIP,
					SenderDomain: extractDomain(payload.Sender),
					Subject:      payload.Subject,
					SPFResult:    payload.SPFResult,
					DKIMResult:   payload.DKIMResult,
					DMARCResult:  payload.DMARCResult,
				})
				if result != nil {
					ep.db.Create(&guardian.GuardianLog{
						MessageID:   payload.MessageID,
						ThreatScore: result.ThreatScore,
						Verdict:     result.Verdict,
						Confidence:  result.Confidence,
						Reasons:     joinStrings(result.Reasons),
						Action:      result.Action,
					})
				}
			}
		}
	}

	ep.db.Create(&models.AuditLog{
		Action:   "email.received",
		Resource: "email:" + payload.MessageID,
		Details:  payload.Sender + " -> " + payload.Recipient,
	})
}

// HandleEmailSent processes an outgoing email event.
func (ep *EventProcessor) HandleEmailSent(payload EmailSentPayload) {
	ep.logger.Info("email sent event",
		zap.String("message_id", payload.MessageID),
		zap.String("sender", payload.Sender),
	)
	ep.db.Create(&models.AuditLog{
		Action:   "email.sent",
		Resource: "email:" + payload.MessageID,
		Details:  payload.Sender + " -> " + payload.Recipient,
	})
}

// HandleBounce processes a bounce event.
func (ep *EventProcessor) HandleBounce(payload BouncePayload) {
	ep.logger.Warn("bounce received",
		zap.String("message_id", payload.MessageID),
		zap.String("recipient", payload.Recipient),
		zap.String("reason", payload.Reason),
	)
	ep.db.Create(&models.AuditLog{
		Action:   "email.bounce",
		Resource: "email:" + payload.MessageID,
		Details:  payload.Recipient + ": " + payload.Reason,
	})
}

// HandleAuthFailure processes an authentication failure event.
func (ep *EventProcessor) HandleAuthFailure(payload AuthFailurePayload) {
	ep.logger.Warn("auth failure event",
		zap.String("username", payload.Username),
		zap.String("ip", payload.IP),
		zap.String("reason", payload.Reason),
	)
	if ep.security != nil {
		ep.security.RecordFailedLogin(nil, payload.IP, payload.Username)
	}
	ep.db.Create(&models.AuditLog{
		Action:   "auth.failure",
		Resource: "user:" + payload.Username,
		IP:       payload.IP,
		Details:  payload.Reason,
	})
}

// EmailWebhookHandler handles incoming webhook POST from Stalwart.
func (ep *EventProcessor) EmailWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	defer r.Body.Close()

	var event struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "EMAIL_RECEIVED":
		var p EmailReceivedPayload
		if json.Unmarshal(event.Payload, &p) == nil {
			ep.HandleEmailReceived(p)
		}
	case "EMAIL_SENT":
		var p EmailSentPayload
		if json.Unmarshal(event.Payload, &p) == nil {
			ep.HandleEmailSent(p)
		}
	case "BOUNCE_RECEIVED":
		var p BouncePayload
		if json.Unmarshal(event.Payload, &p) == nil {
			ep.HandleBounce(p)
		}
	case "AUTH_FAILURE":
		var p AuthFailurePayload
		if json.Unmarshal(event.Payload, &p) == nil {
			ep.HandleAuthFailure(p)
		}
	default:
		ep.logger.Warn("unknown webhook event type", zap.String("type", event.Type))
	}

	w.WriteHeader(http.StatusOK)
}

func extractDomain(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return email
}

func joinStrings(s []string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += "; "
		}
		result += v
	}
	return result
}
