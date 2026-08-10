package kernel

import "strings"

// RequireTypedConfirmation implements the platform-wide convention already
// established by the admin console's ConfirmDialog component
// (web/admin/src/components/ConfirmDialog.tsx, requireTypedName): a
// destructive backend mutation (organization delete, mailbox purge,
// certificate delete, tenant admin removal) must reject the request
// unless the caller's typed confirmation exactly matches the expected
// resource identifier. Constant-time comparison is not required here —
// this is not a secret comparison, it's a "did you mean to do this"
// UX/safety gate, not an authentication check.
func RequireTypedConfirmation(expected, typed string) error {
	if typed == "" || typed != expected {
		return NewError(ErrCodeValidation, "typed confirmation does not match; expected the exact resource identifier")
	}
	return nil
}

// NormalizeConfirmation trims surrounding whitespace only — it does NOT
// lowercase, since resource identifiers (slugs, emails) are frequently
// case-sensitive and silently accepting a case-mismatched confirmation
// would weaken the safety gate.
func NormalizeConfirmation(s string) string {
	return strings.TrimSpace(s)
}
