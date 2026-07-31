package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
)

// respondAPIError writes a stable machine-readable error body:
//
//	{"code": "<CODE>", "message": "<safe human-readable message>"}
//
// The code is the versioned contract the frontend maps to user-facing
// messages. The message is always a safe, non-internal string.
func respondAPIError(c fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{
		"code":    code,
		"message": message,
	})
}

// domainServiceError maps a domain service error to a typed HTTP response.
// Cross-tenant lookups deliberately resolve to the same not-found contract
// used for an absent domain so no information about another tenant's
// resources is revealed.
func domainServiceError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, domain.ErrDomainNotFound):
		return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
	case errors.Is(err, domain.ErrDomainDisabled):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainDisabled, "Domain is disabled.")
	case errors.Is(err, domain.ErrDomainSuspended):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainSuspended, "Domain is suspended.")
	case errors.Is(err, domain.ErrDomainLocked):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainLocked, "Domain is locked.")
	case errors.Is(err, domain.ErrDomainUnavailable):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainUnavailable, "Domain is not available for this operation.")
	case errors.Is(err, domain.ErrDomainNotVerified), errors.Is(err, domain.ErrDomainNotActive):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainNotVerified, "Domain is not verified or not eligible.")
	case errors.Is(err, domain.ErrDomainAlreadyExists):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainAlreadyExists, "Domain already exists.")
	case errors.Is(err, domain.ErrInvalidDomainName):
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeInvalidDomainName, "Invalid domain name.")
	case errors.Is(err, domain.ErrInvalidDomainStatus):
		return respondAPIError(c, fiber.StatusBadRequest, domain.CodeDomainStatusInvalid, "Unsupported domain status.")
	case errors.Is(err, domain.ErrDomainHasMailboxes):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainHasMailboxes, "Domain has mailboxes and cannot be deleted.")
	case errors.Is(err, domain.ErrDomainHasDependencies):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainHasDependencies, "Domain has dependencies and cannot be deleted.")
	case errors.Is(err, domain.ErrDomainLimitReached):
		return respondAPIError(c, fiber.StatusForbidden, domain.CodeDomainLimitReached, "Domain limit reached for your plan.")
	case errors.Is(err, domain.ErrDKIMAlreadyConfigured):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDKIMAlreadyConfigured, "DKIM key already exists for this domain. Confirm rotation to replace it.")
	case errors.Is(err, domain.ErrDKIMNotConfigured):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDKIMNotConfigured, "DKIM is not configured for this domain.")
	default:
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
}

// mailboxEligibilityError maps mailbox-service errors — including the
// domain-eligibility sentinels surfaced during mailbox creation — to the
// typed HTTP contract.
func mailboxEligibilityError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, domain.ErrDomainNotFound):
		return respondAPIError(c, fiber.StatusNotFound, domain.CodeDomainNotFound, "Domain not found.")
	case errors.Is(err, domain.ErrDomainDisabled):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainDisabled, "Domain is disabled.")
	case errors.Is(err, domain.ErrDomainSuspended):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainSuspended, "Domain is suspended.")
	case errors.Is(err, domain.ErrDomainLocked):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainLocked, "Domain is locked.")
	case errors.Is(err, domain.ErrDomainUnavailable):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainUnavailable, "Domain is not available for this operation.")
	case errors.Is(err, domain.ErrDomainNotVerified), errors.Is(err, domain.ErrDomainNotActive):
		return respondAPIError(c, fiber.StatusConflict, domain.CodeDomainNotVerified, "Domain is not verified or not eligible.")
	case errors.Is(err, mailbox.ErrMailboxExists):
		return respondAPIError(c, fiber.StatusConflict, "MAILBOX_ALREADY_EXISTS", "Mailbox already exists.")
	case errors.Is(err, mailbox.ErrInvalidEmail):
		return respondAPIError(c, fiber.StatusBadRequest, "INVALID_EMAIL", "Invalid email address.")
	case errors.Is(err, mailbox.ErrPasswordRequired):
		return respondAPIError(c, fiber.StatusBadRequest, "INVALID_PASSWORD", "Password is required.")
	case errors.Is(err, mailbox.ErrMailboxNotFound):
		return respondAPIError(c, fiber.StatusNotFound, "MAILBOX_NOT_FOUND", "Mailbox not found.")
	default:
		return respondAPIError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "An internal error occurred.")
	}
}
