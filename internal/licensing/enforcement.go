package licensing

import (
	"context"
	"fmt"
)

// CounterFunc is a function that returns the current count of a resource.
type CounterFunc func(ctx context.Context) (int64, error)

// EnforcementService checks license limits before resource creation.
type EnforcementService struct {
	svc             *Service
	domainCount     CounterFunc
	mailboxCount    CounterFunc
}

// NewEnforcementService creates an enforcement service backed by real counters.
func NewEnforcementService(svc *Service, domainCount, mailboxCount CounterFunc) *EnforcementService {
	return &EnforcementService{
		svc:          svc,
		domainCount:  domainCount,
		mailboxCount: mailboxCount,
	}
}

// CanCreateDomain checks if a new domain can be created under current license limits.
func (e *EnforcementService) CanCreateDomain(ctx context.Context) (bool, string) {
	if e.svc == nil {
		return true, "" // no license service → community assumed → allow (limit enforced separately)
	}
	lic := e.svc.GetLicense(ctx)
	if lic == nil {
		// Community edition: allow up to 1 domain.
		count, err := e.domainCount(ctx)
		if err != nil {
			return false, fmt.Sprintf("check domain count: %v", err)
		}
		if count >= 1 {
			return false, "Community edition limited to 1 domain"
		}
		return true, ""
	}

	// Check domain limit.
	if lic.DomainsLimit > 0 {
		count, err := e.domainCount(ctx)
		if err != nil {
			return false, fmt.Sprintf("check domain count: %v", err)
		}
		if count >= lic.DomainsLimit {
			return false, fmt.Sprintf("domain limit reached (%d/%d)", count, lic.DomainsLimit)
		}
	}
	// Unlimited (DomainsLimit == 0) means no limit.
	return true, ""
}

// CanCreateMailbox checks if a new mailbox can be created under current license limits.
func (e *EnforcementService) CanCreateMailbox(ctx context.Context) (bool, string) {
	if e.svc == nil {
		return true, ""
	}
	lic := e.svc.GetLicense(ctx)
	if lic == nil {
		// Community edition: allow up to 5 mailboxes.
		count, err := e.mailboxCount(ctx)
		if err != nil {
			return false, fmt.Sprintf("check mailbox count: %v", err)
		}
		if count >= 5 {
			return false, "Community edition limited to 5 mailboxes"
		}
		return true, ""
	}

	if lic.MailboxesLimit > 0 {
		count, err := e.mailboxCount(ctx)
		if err != nil {
			return false, fmt.Sprintf("check mailbox count: %v", err)
		}
		if count >= lic.MailboxesLimit {
			return false, fmt.Sprintf("mailbox limit reached (%d/%d)", count, lic.MailboxesLimit)
		}
	}
	return true, ""
}

// CheckDomainLimit returns the current usage and limit for domains.
func (e *EnforcementService) CheckDomainLimit(ctx context.Context) (used int64, limit int64, err error) {
	used, err = e.domainCount(ctx)
	if err != nil {
		return 0, 0, err
	}
	if e.svc == nil {
		return used, 1, nil // community default
	}
	lic := e.svc.GetLicense(ctx)
	if lic == nil {
		return used, 1, nil // community default
	}
	if lic.DomainsLimit > 0 {
		return used, lic.DomainsLimit, nil
	}
	return used, 0, nil // 0 = unlimited
}

// CheckMailboxLimit returns the current usage and limit for mailboxes.
func (e *EnforcementService) CheckMailboxLimit(ctx context.Context) (used int64, limit int64, err error) {
	used, err = e.mailboxCount(ctx)
	if err != nil {
		return 0, 0, err
	}
	if e.svc == nil {
		return used, 5, nil // community default
	}
	lic := e.svc.GetLicense(ctx)
	if lic == nil {
		return used, 5, nil // community default
	}
	if lic.MailboxesLimit > 0 {
		return used, lic.MailboxesLimit, nil
	}
	return used, 0, nil // 0 = unlimited
}
