package spf

import "net"

// Result represents an SPF evaluation result per RFC 7208.
type Result int

const (
	ResultNone      Result = iota // No SPF record found
	ResultNeutral                 // Neutral (default if no match)
	ResultPass                    // Pass (authorized)
	ResultFail                    // Fail (not authorized)
	ResultSoftFail                // SoftFail (likely not authorized)
	ResultTempError               // Temporary DNS error
	ResultPermError               // Permanent error (malformed record)
)

func (r Result) String() string {
	switch r {
	case ResultNone:
		return "none"
	case ResultNeutral:
		return "neutral"
	case ResultPass:
		return "pass"
	case ResultFail:
		return "fail"
	case ResultSoftFail:
		return "softfail"
	case ResultTempError:
		return "temperror"
	case ResultPermError:
		return "permerror"
	default:
		return "unknown"
	}
}

// Qualifier is the mechanism qualifier: + (pass), - (fail), ~ (softfail), ? (neutral).
type Qualifier int

const (
	QualPass     Qualifier = iota // +
	QualFail                      // -
	QualSoftFail                  // ~
	QualNeutral                   // ?
)

func parseQualifier(b byte) (Qualifier, int) {
	switch b {
	case '+':
		return QualPass, 1
	case '-':
		return QualFail, 1
	case '~':
		return QualSoftFail, 1
	case '?':
		return QualNeutral, 1
	default:
		return QualPass, 0
	}
}

// Mechanism represents a single SPF mechanism.
type Mechanism struct {
	Qualifier  Qualifier
	Directive  string // e.g. "ip4", "ip6", "a", "mx", "include", "all"
	DomainSpec string // e.g. "192.0.2.0/24", "example.com", ""
	CIDRLen    int    // IPv4 CIDR (32 if not specified), -1 if not present
	CIDRLen6   int    // IPv6 CIDR (128 if not specified), -1 if not present
}

// SPFRecord represents a parsed SPF record.
type SPFRecord struct {
	Version    string // "v=spf1"
	Mechanisms []Mechanism
	Modifiers  map[string]string // e.g. "redirect", "exp"
	Raw        string
}

// Context carries the evaluation inputs and state.
type Context struct {
	ConnectingIP   net.IP
	HeloDomain     string
	MailFromDomain string
	// evaluatedDomains tracks include chains for loop detection.
	evaluatedDomains map[string]bool
}

// EvaluationResult is the output of SPF evaluation.
type EvaluationResult struct {
	Result           Result
	Explanation      string
	MatchedMechanism string // e.g. "ip4", "mx", "include:example.com"
	EvaluatedDomain  string // The domain whose SPF was evaluated
}

// AuthResult is a reusable authentication result model
// for SPF, DKIM, and DMARC (future Authentication-Results header).
type AuthResult struct {
	Method      string // "spf", "dkim", "dmarc"
	Result      string // "pass", "fail", "softfail", etc.
	Domain      string // The domain evaluated
	Explanation string // Human-readable explanation
}

// MaxRecursionDepth is the maximum include depth (RFC 7208 §10).
const MaxRecursionDepth = 10
