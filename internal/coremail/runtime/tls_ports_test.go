package runtime

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
)

func TestTLSIMAPsPortDisabledByDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.IMAPsEnabled {
		t.Error("IMAPS should be disabled by default")
	}
}

func TestTLSPOP3sPortDisabledByDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.POP3sEnabled {
		t.Error("POP3S should be disabled by default")
	}
}

func TestTLSSubmissionRequiresAuth(t *testing.T) {
	cfg := config.Defaults()
	if !cfg.CoreMail.RequireAuthForSubmission {
		t.Error("submission must require AUTH by default")
	}
}

func TestTLSRequiresTLSForAuth(t *testing.T) {
	cfg := config.Defaults()
	if !cfg.CoreMail.RequireTLSForAuth {
		t.Error("submission must require TLS for AUTH by default")
	}
}

func TestIMAPsPortDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.IMAPsPort != 993 {
		t.Errorf("IMAPS default port should be 993, got %d", cfg.CoreMail.IMAPsPort)
	}
}

func TestPOP3sPortDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.POP3sPort != 995 {
		t.Errorf("POP3S default port should be 995, got %d", cfg.CoreMail.POP3sPort)
	}
}

func TestSubmissionPortDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.SubmissionPort != 587 {
		t.Errorf("Submission default port should be 587, got %d", cfg.CoreMail.SubmissionPort)
	}
}

func TestSMTPsPortDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CoreMail.SMTPsPort != 465 {
		t.Errorf("SMTPs default port should be 465, got %d", cfg.CoreMail.SMTPsPort)
	}
}
