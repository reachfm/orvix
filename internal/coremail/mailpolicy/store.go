package mailpolicy

import (
	"context"
	"database/sql"
	"errors"

	"github.com/orvix/orvix/internal/coremail"
)

// EngineStore implements Store over the canonical coremail Engine
// repositories (Mailboxes, Aliases, Domains, Auth). It is the store
// the production runtime and the API handlers wire; nothing else in
// the delivery paths reads these policy inputs directly.
type EngineStore struct {
	Engine *coremail.Engine
}

// NewEngineStoreFromDB builds an EngineStore over an existing *sql.DB.
// It is used by API handlers that do not hold the runtime module's
// engine instance; the engine construction is cheap (repository
// wiring only, no listeners).
func NewEngineStoreFromDB(db *sql.DB) *EngineStore {
	return &EngineStore{Engine: coremail.NewEngine(coremail.EngineConfig{DB: db})}
}

// SenderIdentity resolves an authenticated sender mailbox and its
// effective mode through the canonical repositories.
func (s *EngineStore) SenderIdentity(ctx context.Context, mailboxEmail string) (SenderIdentity, error) {
	mbox, err := s.Engine.Mailboxes.GetByEmail(ctx, mailboxEmail, nil)
	if err != nil {
		return SenderIdentity{}, err
	}
	if mbox == nil || mbox.Status != coremail.MailboxActive {
		return SenderIdentity{}, ErrSenderUnknown
	}
	dom, err := s.Engine.Domains.GetByID(ctx, mbox.DomainID, nil)
	if err != nil {
		return SenderIdentity{}, err
	}
	if dom == nil {
		return SenderIdentity{}, ErrSenderUnknown
	}
	domainMode, err := s.Engine.Domains.GetMailAccessMode(ctx, dom.Name, nil)
	if err != nil {
		return SenderIdentity{}, err
	}
	eff, _ := ResolveEffectiveMode(mbox.MailAccessMode, string(domainMode))
	return SenderIdentity{
		MailboxID:      mbox.ID,
		TenantID:       mbox.TenantID,
		DomainID:       mbox.DomainID,
		MailboxEmail:   mbox.Email,
		ConfiguredMode: eff.Configured,
		EffectiveMode:  eff.Effective,
	}, nil
}

// RecipientIsLocal resolves an address and reports whether it is
// genuinely local — i.e. it resolves (directly, or via forwarder /
// alias / catchall fan-out) to at least one real, active, locally
// hosted mailbox.
//
// This delegates to RecipientEffectiveMode rather than checking
// AuthService.ResolveAddress directly, because ResolveAddress returns
// its raw delivery targets verbatim: an alias's to_addr or a
// forwarder's forward_to is frequently, and legitimately, an EXTERNAL
// address (e.g. "forward all mail for sales@ourco.com to
// sales@partner.example.com"). The earlier implementation treated
// "ResolveAddress succeeded" as "is local", so an internal-only
// mailbox sending to a local address that happened to be an
// externally-forwarding alias was allowed straight through — the
// message actually left the organization. RecipientEffectiveMode
// already validates that at least one resolved target is a real local
// mailbox (it looks each one up and skips anything that isn't), so
// reusing it here closes that gap instead of duplicating a second,
// weaker resolution path.
func (s *EngineStore) RecipientIsLocal(ctx context.Context, address string) (bool, error) {
	_, err := s.RecipientEffectiveMode(ctx, address)
	if err != nil {
		if errors.Is(err, ErrRecipientUnknown) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecipientEffectiveMode resolves the effective mode of the FINAL
// local recipient target(s). When an address resolves to several
// targets (alias/forwarder fan-out), the MOST RESTRICTIVE target mode
// wins: an internal-only target must not receive external mail
// through any alias. ErrRecipientUnknown when there is no local
// target.
func (s *EngineStore) RecipientEffectiveMode(ctx context.Context, address string) (EffectiveMode, error) {
	targets, err := s.Engine.Auth.ResolveAddress(ctx, address)
	if err != nil {
		return EffectiveMode{}, ErrRecipientUnknown
	}
	if len(targets) == 0 {
		return EffectiveMode{}, ErrRecipientUnknown
	}
	mostRestrictive := EffectiveMode{Configured: ModeInherit, Effective: ModeInternalExternal}
	found := false
	for _, t := range targets {
		mbox, merr := s.Engine.Mailboxes.GetByEmail(ctx, t, nil)
		if merr != nil {
			return EffectiveMode{}, merr
		}
		if mbox == nil || mbox.Status != coremail.MailboxActive {
			// A non-active target blocks delivery anyway; the
			// recipient cannot be considered reachable, which is the
			// most restrictive reading for policy purposes.
			continue
		}
		dom, derr := s.Engine.Domains.GetByID(ctx, mbox.DomainID, nil)
		if derr != nil {
			return EffectiveMode{}, derr
		}
		if dom == nil {
			continue
		}
		domainMode, dmErr := s.Engine.Domains.GetMailAccessMode(ctx, dom.Name, nil)
		if dmErr != nil {
			return EffectiveMode{}, dmErr
		}
		eff, _ := ResolveEffectiveMode(mbox.MailAccessMode, string(domainMode))
		found = true
		if eff.Effective == ModeInternalOnly {
			return eff, nil
		}
		mostRestrictive = eff
	}
	if !found {
		return EffectiveMode{}, ErrRecipientUnknown
	}
	return mostRestrictive, nil
}

// IsLocalDomain reports whether the domain is hosted and active via
// the canonical domain repository.
func (s *EngineStore) IsLocalDomain(ctx context.Context, domain string) (bool, error) {
	d, err := s.Engine.Domains.GetByName(ctx, domain, nil)
	if err != nil {
		return false, err
	}
	return d != nil && d.Status == coremail.DomainActive, nil
}
