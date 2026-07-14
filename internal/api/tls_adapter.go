package api

// tlsConfigAdapter bridges *config.Config to
// tlsmgmt.ConfigProvider. The adapter is purposefully
// read-only and never mutates the config.
//
// The adapter translates the operator-facing "smtps /
// imaps / pop3s / jmaps" port + enable flags that
// already exist in CoreMailConfig into the "SMTP / IMAP
// / POP3 / JMAP TLS enabled" boolean interface that
// tlsmgmt.ConfigProvider expects.
//
// When the smtps_* / imaps_* / pop3s_* pair is unset,
// tlsConfigAdapter reports TLS as disabled for that
// protocol even though the listener may still announce
// STARTTLS. The runtime layer (release/scripts/setup-smtp-tls.sh)
// is the authority on listener TLS posture; tlsmgmt is
// only used to surface readable certificate inventory.

import (
	"strconv"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/tlsmgmt"
)

type tlsConfigAdapter struct{ cfg *config.Config }

func (a *tlsConfigAdapter) GetCertPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.TLSCertFile
}

func (a *tlsConfigAdapter) GetKeyPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.TLSKeyFile
}

func (a *tlsConfigAdapter) SMTPTLSEnabled() bool { return a.cfg != nil && a.cfg.CoreMail.SMTPsEnabled }
func (a *tlsConfigAdapter) IMAPTLSEnabled() bool { return a.cfg != nil && a.cfg.CoreMail.IMAPsEnabled }
func (a *tlsConfigAdapter) POP3TLSEnabled() bool { return a.cfg != nil && a.cfg.CoreMail.POP3sEnabled }

// JMAP runs TLS-terminated by the reverse proxy (Caddy)
// in our topology. The listener itself does not own a
// TLS handshake. Report false from this adapter; the
// admin UI exposes that decision explicitly.
func (a *tlsConfigAdapter) JMAPTLSEnabled() bool { return false }

func (a *tlsConfigAdapter) SMTPAddress() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.SMTPHost + ":" + strconv.Itoa(a.cfg.CoreMail.SMTPsPort)
}

func (a *tlsConfigAdapter) IMAPAddress() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.IMAPHost + ":" + strconv.Itoa(a.cfg.CoreMail.IMAPsPort)
}

func (a *tlsConfigAdapter) POP3Address() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.POP3Host + ":" + strconv.Itoa(a.cfg.CoreMail.POP3sPort)
}

func (a *tlsConfigAdapter) JMAPAddress() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.CoreMail.JMAPHost + ":" + strconv.Itoa(a.cfg.CoreMail.JMAPPort)
}

// Compile-time assertion: *tlsConfigAdapter implements
// tlsmgmt.ConfigProvider.
var _ tlsmgmt.ConfigProvider = (*tlsConfigAdapter)(nil)
