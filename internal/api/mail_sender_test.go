package api

import (
	"testing"

	"go.uber.org/zap"
)

// Regression test for the OTP-delivery live hotfix: the authenticated
// submission client must dial the server's real mail hostname (the one
// the TLS certificate was issued for), never a LISTEN bind address like
// "0.0.0.0" — auth.DialSMTPWithTLS uses the same host value for both the
// TCP dial target and the TLS ServerName, so a bind address there means
// certificate verification fails even though the TCP connection itself
// succeeds (observed live: "cannot validate certificate for 0.0.0.0").
//
// It must also send AS the authenticated account itself (cfgUsername,
// e.g. noreply@orvix.email), not noreply@<cfgHostname>
// (noreply@mail.orvix.email): cfgHostname is the MTA hostname, which is
// neither the mailbox's own domain nor DKIM-configured — the relay
// correctly rejects a mismatched MAIL FROM with "550 5.7.1 Sender not
// authorized" (also observed live).
func TestInitTransactionalMailSender_AuthenticatedUsesHostnameNotBindAddress(t *testing.T) {
	logger := zap.NewNop()

	sender := initTransactionalMailSender(
		"0.0.0.0", 25, // cfgSMTPHost, cfgSMTPPort — bind addresses, must never be dialed
		"0.0.0.0", 0, // cfgSubmissionHost, cfgSubmissionPort — bind address, must never be dialed
		"noreply@orvix.email", "svc-pass", // credentials present -> authenticated path
		"mail.orvix.email", // cfgHostname — the real, cert-matching hostname
		logger,
	)

	smtpSender, ok := sender.(*smtpMailSender)
	if !ok {
		t.Fatalf("want *smtpMailSender, got %T", sender)
	}
	if smtpSender.host != "mail.orvix.email" {
		t.Fatalf("want host=mail.orvix.email (matches the TLS cert), got %q — this would fail cert verification exactly like the live bug", smtpSender.host)
	}
	if smtpSender.port != 587 {
		t.Fatalf("want default submission port 587, got %d", smtpSender.port)
	}
	if smtpSender.username != "noreply@orvix.email" || smtpSender.password != "svc-pass" {
		t.Fatalf("credentials not wired through")
	}
	if smtpSender.from != "noreply@orvix.email" {
		t.Fatalf("want from=noreply@orvix.email (the authenticated account's own address), got %q", smtpSender.from)
	}
}

func TestInitTransactionalMailSender_AuthenticatedRespectsExplicitSubmissionPort(t *testing.T) {
	logger := zap.NewNop()
	sender := initTransactionalMailSender("0.0.0.0", 25, "0.0.0.0", 2525, "u", "p", "mail.orvix.email", logger)
	smtpSender, ok := sender.(*smtpMailSender)
	if !ok {
		t.Fatalf("want *smtpMailSender, got %T", sender)
	}
	if smtpSender.port != 2525 {
		t.Fatalf("want explicit submission port 2525, got %d", smtpSender.port)
	}
}

func TestInitTransactionalMailSender_NoCredentialsFallsBackToUnauthenticated(t *testing.T) {
	logger := zap.NewNop()
	sender := initTransactionalMailSender("0.0.0.0", 25, "", 0, "", "", "mail.orvix.email", logger)
	smtpSender, ok := sender.(*smtpMailSender)
	if !ok {
		t.Fatalf("want *smtpMailSender, got %T", sender)
	}
	if smtpSender.username != "" || smtpSender.password != "" {
		t.Fatalf("want no credentials on the fallback unauthenticated sender")
	}
	if smtpSender.host != "0.0.0.0" || smtpSender.port != 25 {
		t.Fatalf("want the legacy unauthenticated path to still use cfgSMTPHost:cfgSMTPPort, got %s:%d", smtpSender.host, smtpSender.port)
	}
}

func TestInitTransactionalMailSender_NoSMTPConfiguredReturnsNoop(t *testing.T) {
	logger := zap.NewNop()
	sender := initTransactionalMailSender("", 0, "", 0, "", "", "mail.orvix.email", logger)
	if _, ok := sender.(*noopMailSender); !ok {
		t.Fatalf("want *noopMailSender when no SMTP host/port is configured, got %T", sender)
	}
}
