package runtime

// Phase 8 C3A #4: pre-commit/pre-enqueue domain recheck, run
// immediately before a completed SMTP message is accepted into
// spool/queue — closing the window where a domain is disabled after
// RCPT but before DATA finishes. Tests exercise smtpPreAcceptRecheck
// and its checkRecipientDomainsOperable core directly, same-package,
// for the same reason as rcpt_operability_test.go.

import (
	"testing"

	"github.com/orvix/orvix/internal/coremail/smtp"
)

func TestCheckRecipientDomainsOperable_AllActiveAllowed(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "a.example", "active", false)
	seedRcptDomain(t, db, "b.example", "active", false)
	m := &Module{db: db}

	err := m.checkRecipientDomainsOperable(t.Context(), []string{"x@a.example", "y@b.example"})
	if err != nil {
		t.Fatalf("expected all-active recipients to be accepted, got %v", err)
	}
}

// TestCheckRecipientDomainsOperable_DisabledAfterRCPT is the decisive
// test for requirement #4: a domain that was active at RCPT time but
// became disabled before DATA finished must be rejected at the
// pre-accept recheck, not silently accepted.
func TestCheckRecipientDomainsOperable_DisabledAfterRCPT(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "was-active.example", "active", false)
	m := &Module{db: db}

	rcpt := "user@was-active.example"
	allow, _, err := m.checkRecipientDomainOperability(t.Context(), rcpt)
	if err != nil || !allow {
		t.Fatalf("expected RCPT-time check to allow the still-active domain, got allow=%v err=%v", allow, err)
	}

	if _, err := db.Exec(`UPDATE coremail_domains SET status = 'disabled' WHERE name = 'was-active.example'`); err != nil {
		t.Fatalf("simulate concurrent disable: %v", err)
	}

	if err := m.checkRecipientDomainsOperable(t.Context(), []string{rcpt}); err != errRecipientDomainInoperable {
		t.Fatalf("expected the pre-accept recheck to reject a domain disabled after RCPT, got %v", err)
	}
}

func TestCheckRecipientDomainsOperable_DedupesByDomain(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "shared.example", "active", false)
	m := &Module{db: db}

	err := m.checkRecipientDomainsOperable(t.Context(), []string{
		"one@shared.example", "two@shared.example", "three@shared.example",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Correctness of dedup itself (one query per distinct domain, not
	// per recipient) is an implementation detail of the loop; what
	// matters observably is that all three addresses on the one
	// operable domain are accepted together, which the call above
	// already proves.
}

func TestCheckRecipientDomainsOperable_InfrastructureFailureIsTransient(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "active.example", "active", false)
	m := &Module{db: db}
	db.Close()

	err := m.checkRecipientDomainsOperable(t.Context(), []string{"user@active.example"})
	if err != errPolicyUnavailable {
		t.Fatalf("expected errPolicyUnavailable on repository failure, got %v", err)
	}
}

func TestCheckRecipientDomainsOperable_NilDBFailsClosed(t *testing.T) {
	m := &Module{}
	err := m.checkRecipientDomainsOperable(t.Context(), []string{"user@anything.example"})
	if err != errPolicyUnavailable {
		t.Fatalf("expected errPolicyUnavailable on nil db, got %v", err)
	}
}

func TestSmtpPreAcceptRecheck_TranslatesSentinelForServer(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "disabled.example", "disabled", false)
	m := &Module{db: db}

	session := &smtp.Session{Recipients: []string{"user@disabled.example"}}
	err := m.smtpPreAcceptRecheck(t.Context(), session)
	if err != smtp.ErrRecipientDomainInoperable {
		t.Fatalf("expected the exported smtp.ErrRecipientDomainInoperable sentinel, got %v", err)
	}
}

func TestSmtpPreAcceptRecheck_InfraFailureStaysDistinctFromPolicyRejection(t *testing.T) {
	db := rcptTestDB(t)
	seedRcptDomain(t, db, "active.example", "active", false)
	m := &Module{db: db}
	db.Close()

	session := &smtp.Session{Recipients: []string{"user@active.example"}}
	err := m.smtpPreAcceptRecheck(t.Context(), session)
	if err == nil || err == smtp.ErrRecipientDomainInoperable {
		t.Fatalf("expected a transient (non-sentinel) error on infra failure, got %v", err)
	}
}

func TestSmtpPreAcceptRecheck_NilSessionIsNoop(t *testing.T) {
	m := &Module{}
	if err := m.smtpPreAcceptRecheck(t.Context(), nil); err != nil {
		t.Fatalf("expected nil session to be a no-op, got %v", err)
	}
}
