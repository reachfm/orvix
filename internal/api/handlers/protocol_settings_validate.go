package handlers

// Semantic validation for PATCH /api/v1/admin/settings/protocol/:protocol.
//
// coerceForType (enterprise_admin_v3.go) only checks the JSON
// primitive type — a port of -1 or 999999, a negative duration, or an
// out-of-order warning/critical percentage pair all pass it cleanly
// and used to be persisted as-is. Every validator below is derived
// from how the value is actually consumed at runtime (net.Listen
// port range, time.ParseDuration, the monitoring threshold
// comparison, etc.) — never an invented range. A key with no
// validator entry here has no provable canonical constraint and must
// be marked ReadOnly in protocolDefs instead of guessed at.

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// protocolKeyValidator normalizes and validates one field's already
// type-coerced value (from coerceForType). It returns the normalized
// value to persist, or an error describing exactly why the value is
// rejected.
type protocolKeyValidator func(v any) (any, error)

func portValidator(v any) (any, error) {
	n, ok := v.(int64)
	if !ok {
		return nil, fmt.Errorf("expected an integer port")
	}
	if n < 1 || n > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535, got %d", n)
	}
	return n, nil
}

// percentValidator enforces 0..100. Cross-field warning<critical
// ordering is enforced separately in PatchProtocolSettings, since it
// needs both values in the same request/effective-state at once.
func percentValidator(v any) (any, error) {
	n, ok := v.(int64)
	if !ok {
		return nil, fmt.Errorf("expected an integer percentage")
	}
	if n < 0 || n > 100 {
		return nil, fmt.Errorf("percentage must be between 0 and 100, got %d", n)
	}
	return n, nil
}

// positiveIntValidator returns a validator requiring min <= n <= max.
func positiveIntValidator(min, max int64) protocolKeyValidator {
	return func(v any) (any, error) {
		n, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("expected an integer")
		}
		if n < min || n > max {
			return nil, fmt.Errorf("must be between %d and %d, got %d", min, max, n)
		}
		return n, nil
	}
}

// durationValidator requires a strictly positive Go duration literal
// (the exact format time.ParseDuration accepts, since that is what
// the runtime uses to parse it).
func durationValidator(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expected a duration string")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("not a valid duration (e.g. \"5s\", \"15m\", \"720h\"): %v", err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("duration must be strictly positive, got %q", s)
	}
	// Normalize to Go's canonical rendering so what's persisted is
	// exactly what time.ParseDuration/Duration.String() agree on.
	return d.String(), nil
}

// bindHostValidator requires a literal IPv4/IPv6 address — the exact
// format net.Listen expects for a bind host in this codebase (see
// coremail.smtp_host/imap_host/pop3_host/jmap_host/submission_host,
// all consumed as net.Listen(addr) where addr = host+":"+port).
func bindHostValidator(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expected a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("bind host is required")
	}
	if net.ParseIP(s) == nil {
		return nil, fmt.Errorf("must be a valid IPv4/IPv6 bind address (e.g. \"0.0.0.0\", \"127.0.0.1\", \"::\"), got %q", s)
	}
	return s, nil
}

// ipOrEmptyValidator allows a literal IP address or the empty string
// (the runtime's own documented "unset / auto-detect" value for
// CoreMail.PublicIPv4/PublicIPv6 — see config.go's DNS section).
func ipOrEmptyValidator(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expected a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return s, nil
	}
	if net.ParseIP(s) == nil {
		return nil, fmt.Errorf("must be a valid IP address or empty, got %q", s)
	}
	return s, nil
}

// cookieDomainValidator requires either the empty string (no
// cross-subdomain cookie) or a leading-dot domain, the exact form the
// label ("Auth cookie domain (.parent.com)") and the auth cookie
// setter both document.
func cookieDomainValidator(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expected a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return s, nil
	}
	if !strings.HasPrefix(s, ".") || strings.ContainsAny(s, " \t/\\") {
		return nil, fmt.Errorf("cookie domain must start with \".\" (e.g. \".example.com\") or be empty, got %q", s)
	}
	return s, nil
}

// mailtoOrEmptyValidator requires the "mailto:" form VAPID subjects
// must take (RFC 8292 §2), or empty (push disabled/unconfigured).
func mailtoOrEmptyValidator(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expected a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return s, nil
	}
	if !strings.HasPrefix(s, "mailto:") || len(s) <= len("mailto:") {
		return nil, fmt.Errorf("VAPID subject must be a \"mailto:\" URI or empty, got %q", s)
	}
	return s, nil
}

func boolValidator(v any) (any, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("expected a boolean")
	}
	return b, nil
}

// protocolKeyValidators maps every writable protocol-settings key to
// its semantic validator. A key present in protocolDefs but absent
// here (and not ReadOnly) is a bug — PatchProtocolSettings treats a
// missing validator as an unconditional rejection rather than
// silently accepting an unvalidated value.
var protocolKeyValidators = map[string]protocolKeyValidator{
	"coremail.smtp_port":                   portValidator,
	"coremail.smtp_host":                   bindHostValidator,
	"coremail.require_tls_for_auth":        boolValidator,
	"coremail.require_auth_for_submission": boolValidator,
	"coremail.max_attachment_size_mb":      positiveIntValidator(1, 1024),
	"coremail.max_attachments_per_message": positiveIntValidator(1, 1000),
	"coremail.queue_workers":               positiveIntValidator(1, 256),
	"coremail.worker_interval":             durationValidator,

	"coremail.submission_enabled": boolValidator,
	"coremail.submission_port":    portValidator,
	"coremail.submission_host":    bindHostValidator,
	"coremail.smtps_enabled":      boolValidator,
	"coremail.smtps_port":         portValidator,
	"outbound.prefer_ipv4":        boolValidator,

	"coremail.imap_host":     bindHostValidator,
	"coremail.imap_port":     portValidator,
	"coremail.imaps_enabled": boolValidator,
	"coremail.imaps_port":    portValidator,

	"coremail.pop3_host":     bindHostValidator,
	"coremail.pop3_port":     portValidator,
	"coremail.pop3s_enabled": boolValidator,
	"coremail.pop3s_port":    portValidator,

	"auth.cookie_domain":   cookieDomainValidator,
	"auth.jwt_access_ttl":  durationValidator,
	"auth.jwt_refresh_ttl": durationValidator,

	"auth.password_min_len":              positiveIntValidator(8, 128),
	"monitoring.disk_usage_warning_pct":  percentValidator,
	"monitoring.disk_usage_critical_pct": percentValidator,

	"dns.public_ipv4":            ipOrEmptyValidator,
	"dns.public_ipv6":            ipOrEmptyValidator,
	"dns.namecheap_enable_apply": boolValidator,

	// coremail.imap_idle_enabled: deliberately absent — it is marked
	// ReadOnly in protocolDefs because no live config field backs it.

	"coremail.jmap_host": bindHostValidator,
	"coremail.jmap_port": portValidator,

	"coremail.vapid_subject": mailtoOrEmptyValidator,
}
