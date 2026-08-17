package api

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"go.uber.org/zap"
)

type smtpMailSender struct {
	host     string
	port     int
	username string
	password string
	from     string
	logger   *zap.Logger
}

func newSMTPMailSender(host string, port int, username, password, from string, logger *zap.Logger) handlers.MailSender {
	return &smtpMailSender{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		logger:   logger,
	}
}

func (s *smtpMailSender) Send(to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from, to, subject, body)

	if s.username != "" && s.password != "" {
		// Authenticated mode — require TLS.
		client, err := s.dialAuthenticated()
		if err != nil {
			return fmt.Errorf("SMTP dial: %w", err)
		}
		defer client.Close()

		if err := client.Mail(s.from); err != nil {
			return fmt.Errorf("SMTP MAIL FROM: %w", err)
		}
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("SMTP RCPT TO: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP DATA: %w", err)
		}
		if _, err := w.Write([]byte(msg)); err != nil {
			return fmt.Errorf("SMTP write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("SMTP close: %w", err)
		}
		return client.Quit()
	}

	// Unauthenticated mode — use standard smtp.SendMail which supports STARTTLS.
	return smtp.SendMail(addr, nil, s.from, []string{to}, []byte(msg))
}

// dialAuthenticated establishes a TLS-secured SMTP connection and authenticates
// via the shared auth.DialSMTPWithTLS helper.
func (s *smtpMailSender) dialAuthenticated() (*smtp.Client, error) {
	return auth.DialSMTPWithTLS(s.host, s.port, s.username, s.password)
}

type noopMailSender struct{}

func newNoopMailSender() handlers.MailSender {
	return &noopMailSender{}
}

func (n *noopMailSender) Send(to, subject, body string) error {
	return nil
}

// initTransactionalMailSender wires the shared first-party transactional
// mail pipeline (signup OTP, password reset, support requests).
//
// Anonymous SMTP against the inbound MX port (cfgSMTPHost:cfgSMTPPort) is
// correctly rejected by CoreMail's anti-relay policy for any recipient
// outside the server's own domains — that rejection is the relay policy
// working as designed, not a bug. First-party transactional mail to
// external recipients therefore requires authenticating as a real local
// account on the TLS submission port, which is what happens here whenever
// submission credentials are configured; otherwise this falls back to the
// legacy unauthenticated MX-port path for backward compatibility (which
// will still be relay-rejected for external recipients — see
// customer_signup_otp.go's Part 1 fix for why that no longer produces a
// false "sent" response).
func initTransactionalMailSender(cfgSMTPHost string, cfgSMTPPort int, cfgSubmissionHost string, cfgSubmissionPort int, cfgUsername, cfgPassword, cfgHostname string, logger *zap.Logger) handlers.MailSender {
	from := fmt.Sprintf("noreply@%s", resolveHostname(cfgHostname))

	if cfgUsername != "" && cfgPassword != "" {
		host := cfgSubmissionHost
		if host == "" {
			host = cfgSMTPHost
		}
		port := cfgSubmissionPort
		if port == 0 {
			port = 587
		}
		if host != "" {
			sender := newSMTPMailSender(host, port, cfgUsername, cfgPassword, from, logger)
			logger.Info("transactional mail sender wired via authenticated SMTP submission", zap.String("host", host), zap.Int("port", port))
			return sender
		}
	}

	host := cfgSMTPHost
	port := cfgSMTPPort
	if host == "" || port == 0 {
		logger.Info("transactional mail sender: no SMTP configured — emails logged only")
		return newNoopMailSender()
	}
	logger.Warn("transactional mail sender wired via UNAUTHENTICATED SMTP — external delivery will be rejected by the anti-relay policy; configure transactional submission credentials to enable real delivery", zap.String("host", host), zap.Int("port", port))
	sender := newSMTPMailSender(host, port, "", "", from, logger)
	return sender
}

func resolveHostname(h string) string {
	if h == "" {
		return "localhost"
	}
	if strings.Contains(h, ":") {
		h = strings.Split(h, ":")[0]
	}
	return h
}
