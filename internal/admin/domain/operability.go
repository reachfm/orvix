package domain

import (
	"context"
	"database/sql"
)

// ── Canonical domain operability guard (Phase 8 C1) ─────────────────
//
// This is the ONE place any subsystem asks "can this domain currently
// accept mailbox/alias creation, DKIM mutation, or mail flow?" — every
// caller gets the same answer from the same query and the same typed
// error mapping, instead of re-deriving domain-state rules per
// subsystem (which is exactly how divergent, inconsistent enforcement
// happens).
//
// Origin: internal/admin/mailbox/service.go's CreateMailbox already
// had this exact pattern inlined (tenant-scoped, FOR UPDATE-lockable,
// safe-not-found, typed-error-mapped) before this file existed. This
// extracts it so every OTHER subsystem (aliases, bulk provisioning,
// DKIM, SMTP inbound, webmail send, queue redelivery — none of which
// currently check domain operability at all) can share it rather than
// re-implementing or drifting from it. mailbox/service.go itself was
// refactored in the same change to call StatusError instead of
// duplicating the switch.

// OperabilityOutcome is the typed result of a domain operability
// check — the "operational | deactivated | suspended | locked |
// not-found | cross-tenant | repository-unavailable" outcome set the
// remediation asked for, expressed via this repo's EXISTING typed
// error vocabulary (ErrDomainDisabled, ErrDomainSuspended, ...)
// rather than inventing a parallel one.
type OperabilityOutcome struct {
	// DomainID is populated only when the domain was found and owned
	// by the requested tenant (Err == nil or a status-rejection err).
	// It is zero for ErrDomainNotFound / cross-tenant / infra errors.
	DomainID uint
	Status   DomainStatus
	// Err is nil (operational), one of the typed Err* vars above for
	// a real domain-state rejection, ErrDomainNotFound for
	// missing/cross-tenant/soft-deleted (deliberately the same value
	// for all three — a tenant-scoped caller must never learn which
	// case it was), or a raw, unwrapped infrastructure error for a
	// genuine repository failure (fail-closed: never silently
	// reinterpreted as "domain active").
	Err error
}

// Operational reports whether the domain may be used for the action
// under check (mailbox/alias creation, DKIM mutation, outbound mail).
func (o OperabilityOutcome) Operational() bool { return o.Err == nil }

// StatusError maps a persisted coremail_domains.status value to the
// typed rejection error a caller should surface. Extracted verbatim
// from the switch that used to live inline in
// internal/admin/mailbox/service.go's CreateMailbox — same mapping,
// now shared. An unrecognized/legacy status value fails closed to
// ErrDomainUnavailable rather than being treated as active.
func StatusError(status DomainStatus) error {
	if status == DomainStatusActive {
		return nil
	}
	switch status {
	case DomainStatusDisabled:
		return ErrDomainDisabled
	case DomainStatusSuspended:
		return ErrDomainSuspended
	case DomainStatusLocked:
		return ErrDomainLocked
	default:
		return ErrDomainUnavailable
	}
}

// CheckOperabilityTx resolves a domain's operability by NAME, scoped
// to tenantID, inside the caller's own transaction (tx must be a
// *sql.Tx obtained from the same *sql.DB this repo wraps — pass
// r.WithTx(tx) to get a repo bound to it, or call this method on a
// repo already constructed via WithTx). lock requests a FOR UPDATE
// read on PostgreSQL (a no-op on SQLite, matching
// ResolveDomainAllocation's existing convention) so the operability
// check and the caller's subsequent mutation are atomic — this is
// what closes the TOCTOU race the remediation calls out: a second,
// non-transactional lookup would let a domain get deactivated between
// the check and the mutation.
//
// Not-found, cross-tenant, and soft-deleted all resolve to the same
// ErrDomainNotFound — a tenant-scoped caller must never be able to
// distinguish "doesn't exist" from "belongs to someone else" from the
// error alone.
func (r *DomainAdminRepo) CheckOperabilityTx(ctx context.Context, name string, tenantID uint, lock bool) OperabilityOutcome {
	q := "SELECT id, status, deleted_at FROM coremail_domains WHERE name=" +
		r.dialect.Placeholder(1) + " AND tenant_id=" + r.dialect.Placeholder(2)
	if lock && r.dialect.IsPostgres() {
		q += " FOR UPDATE"
	}
	var id uint
	var status string
	var deletedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, q, name, tenantID).Scan(&id, &status, &deletedAt)
	if err == sql.ErrNoRows {
		return OperabilityOutcome{Err: ErrDomainNotFound}
	}
	if err != nil {
		// Repository/infrastructure failure — fail closed. This is
		// NOT ErrDomainNotFound and NOT "operational"; callers must
		// treat a non-nil, non-typed error as "cannot verify, refuse
		// the action" rather than defaulting to permissive behavior.
		return OperabilityOutcome{Err: err}
	}
	if deletedAt.Valid {
		return OperabilityOutcome{Err: ErrDomainNotFound}
	}
	ds := DomainStatus(status)
	return OperabilityOutcome{DomainID: id, Status: ds, Err: StatusError(ds)}
}

// CheckOperabilityByIDTx is CheckOperabilityTx's by-ID counterpart,
// for callers that already hold a domain_id (mailboxes, aliases,
// queue entries, DKIM config keyed elsewhere) rather than a domain
// name. Same tenant-scoping, locking, and error-mapping contract.
func (r *DomainAdminRepo) CheckOperabilityByIDTx(ctx context.Context, domainID, tenantID uint, lock bool) OperabilityOutcome {
	q := "SELECT status, deleted_at FROM coremail_domains WHERE id=" +
		r.dialect.Placeholder(1) + " AND tenant_id=" + r.dialect.Placeholder(2)
	if lock && r.dialect.IsPostgres() {
		q += " FOR UPDATE"
	}
	var status string
	var deletedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, q, domainID, tenantID).Scan(&status, &deletedAt)
	if err == sql.ErrNoRows {
		return OperabilityOutcome{Err: ErrDomainNotFound}
	}
	if err != nil {
		return OperabilityOutcome{Err: err}
	}
	if deletedAt.Valid {
		return OperabilityOutcome{Err: ErrDomainNotFound}
	}
	ds := DomainStatus(status)
	return OperabilityOutcome{DomainID: domainID, Status: ds, Err: StatusError(ds)}
}
