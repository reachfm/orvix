package relay

import "strings"

// Sender-pattern matching for routing rules (Phase 3A, Fix B).
//
// RoutingRule.SenderPattern was persisted and exposed through the admin API
// but never evaluated by ruleMatches, so every rule behaved as if it had no
// sender selector: a rule an operator scoped to "billing@acme.test" routed ALL
// of acme.test's mail through that pool.
//
// MATCHER CHOICE: this is a bounded literal/glob matcher, deliberately NOT a
// regular expression. SenderPattern is operator-supplied configuration that is
// evaluated on the hot path of every outbound message; Go's regexp is
// linear-time and so not vulnerable to catastrophic backtracking, but a
// regexp-based matcher would still (a) let a pattern's cost scale with input,
// (b) require compilation caching to avoid recompiling per message, and (c)
// make the semantics of an operator's pattern hard to predict. The grammar
// below is total, allocation-free, and runs in O(len(pattern) + len(input))
// with no backtracking at all.
//
// GRAMMAR (matching is case-insensitive; addresses are case-insensitive in
// practice for routing purposes):
//
//	exact@address.test   matches only that envelope sender
//	*@example.test       matches any local part at that domain
//	@example.test        equivalent to *@example.test
//	example.test         a bare domain; matches any sender at that domain
//	prefix*              matches any sender starting with prefix
//	*substring*          matches any sender containing substring
//	*                    matches any sender
//
// A '*' is only special at the start and/or end of the pattern. An interior
// '*' is treated as a literal character, so a pattern can never expand into
// multiple wildcard segments and the matcher stays single-pass.
//
// An EMPTY sender address never matches a non-empty pattern: a null envelope
// sender (bounces, "MAIL FROM:<>") must not silently satisfy a rule that was
// scoped to a specific sender.

// maxSenderPatternLen bounds the pattern length accepted from configuration.
// Anything longer is treated as non-matching rather than evaluated, so a
// pathological stored value cannot affect per-message cost.
const maxSenderPatternLen = 320 // RFC 5321 max path length

func senderPatternMatches(pattern, senderAddress, senderDomain string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true // no selector configured
	}
	if len(pattern) > maxSenderPatternLen {
		// Refuse to evaluate an out-of-spec pattern. Fail CLOSED: the rule
		// does not match, so mail is not routed by a rule we cannot honour.
		return false
	}
	if pattern == "*" {
		return true
	}

	addr := strings.ToLower(strings.TrimSpace(senderAddress))
	dom := strings.ToLower(strings.TrimSpace(senderDomain))
	if addr == "" && dom == "" {
		// Null/unknown sender: only the unconditional "*" (handled above)
		// matches.
		return false
	}
	if dom == "" {
		if at := strings.LastIndex(addr, "@"); at >= 0 {
			dom = addr[at+1:]
		}
	}
	p := strings.ToLower(pattern)

	// "@example.test" — domain-only form.
	if strings.HasPrefix(p, "@") {
		return dom != "" && dom == p[1:]
	}

	leading := strings.HasPrefix(p, "*")
	trailing := strings.HasSuffix(p, "*")
	core := p
	if leading {
		core = core[1:]
	}
	if trailing && len(core) > 0 {
		core = core[:len(core)-1]
	}
	if core == "" {
		return true // "*" or "**"
	}

	switch {
	case leading && trailing:
		return strings.Contains(addr, core)
	case leading:
		// "*@example.test" and "*suffix" alike: suffix match on the address.
		return strings.HasSuffix(addr, core)
	case trailing:
		return strings.HasPrefix(addr, core)
	}

	// No wildcard. An exact address match, or — when the pattern names a bare
	// domain with no local part — a match on the sending domain.
	if core == addr {
		return true
	}
	if !strings.Contains(core, "@") && dom != "" && core == dom {
		return true
	}
	return false
}
