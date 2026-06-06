package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AlertSender sends security alerts via SMTP and/or webhook.
type AlertSender struct {
	db     *gorm.DB
	logger *zap.Logger
}

// NewAlertSender creates a new alert sender.
func NewAlertSender(db *gorm.DB, logger *zap.Logger) *AlertSender {
	return &AlertSender{db: db, logger: logger}
}

// AlertEvent represents a security alert event.
type AlertEvent struct {
	Type      string `json:"type"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	IP        string `json:"ip"`
	Email     string `json:"email"`
	Timestamp int64  `json:"timestamp"`
}

// SendAlert delivers an alert via configured channels for the tenant.
func (as *AlertSender) SendAlert(ctx context.Context, tenantID uint, event AlertEvent) {
	event.Timestamp = time.Now().Unix()

	var cfg struct {
		SMTPEnabled  bool
		SMTPServer   string
		SMTPPort     int
		SMTPUsername string
		SMTPPassword string
		SMTPFrom     string
		WebhookEnabled bool
		WebhookURL   string
	}
	as.db.Table("alert_configs").Where("tenant_id = ?", tenantID).First(&cfg)

	if cfg.SMTPEnabled && cfg.SMTPServer != "" {
		as.sendSMTP(cfg, event)
	}
	if cfg.WebhookEnabled && cfg.WebhookURL != "" {
		as.sendWebhook(ctx, cfg.WebhookURL, event)
	}

	as.logger.Warn("alert sent",
		zap.String("type", event.Type),
		zap.String("severity", event.Severity),
		zap.Uint("tenant_id", tenantID),
	)
}

func (as *AlertSender) sendSMTP(cfg struct {
	SMTPEnabled  bool
	SMTPServer   string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	WebhookEnabled bool
	WebhookURL   string
}, event AlertEvent) {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPServer, cfg.SMTPPort)
	subject := fmt.Sprintf("[Orvix Alert] %s - %s", event.Severity, event.Type)
	body := fmt.Sprintf("Subject: %s\r\n\r\nAlert: %s\r\nSeverity: %s\r\nIP: %s\r\nEmail: %s\r\nTime: %d",
		subject, event.Message, event.Severity, event.IP, event.Email, event.Timestamp)

	auth := smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPServer)
	err := smtp.SendMail(addr, auth, cfg.SMTPFrom, nil, []byte(body))
	if err != nil {
		as.logger.Error("smtp alert failed", zap.Error(err))
	}
}

func (as *AlertSender) sendWebhook(ctx context.Context, url string, event AlertEvent) {
	data, _ := json.Marshal(event)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		as.logger.Error("webhook alert failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
}
