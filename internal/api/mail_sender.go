package api

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/orvix/orvix/internal/api/handlers"
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
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		return smtp.SendMail(addr, auth, s.from, []string{to}, []byte(msg))
	}
	return smtp.SendMail(addr, nil, s.from, []string{to}, []byte(msg))
}

type noopMailSender struct{}

func newNoopMailSender() handlers.MailSender {
	return &noopMailSender{}
}

func (n *noopMailSender) Send(to, subject, body string) error {
	return nil
}

func initTransactionalMailSender(cfgSMTPHost string, cfgSMTPPort int, cfgHostname string, logger *zap.Logger) handlers.MailSender {
	host := cfgSMTPHost
	port := cfgSMTPPort
	if host == "" || port == 0 {
		logger.Info("transactional mail sender: no SMTP configured — emails logged only")
		return newNoopMailSender()
	}
	from := fmt.Sprintf("noreply@%s", resolveHostname(cfgHostname))
	sender := newSMTPMailSender(host, port, "", "", from, logger)
	logger.Info("transactional mail sender wired via SMTP", zap.String("host", host), zap.Int("port", port))
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
