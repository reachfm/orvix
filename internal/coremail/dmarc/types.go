package dmarc

// Result represents a DMARC evaluation result per RFC 7489.
type Result int

const (
	ResultNone      Result = iota // No DMARC record or no auth
	ResultPass                    // Pass (alignment + auth pass)
	ResultFail                    // Fail (auth or alignment failed)
	ResultTempError               // Temporary DNS error
	ResultPermError               // Permanent error (malformed record)
)

func (r Result) String() string {
	switch r {
	case ResultNone:
		return "none"
	case ResultPass:
		return "pass"
	case ResultFail:
		return "fail"
	case ResultTempError:
		return "temperror"
	case ResultPermError:
		return "permerror"
	default:
		return "unknown"
	}
}

// Policy represents a DMARC disposition policy.
type Policy int

const (
	PolicyNone       Policy = iota // p=none
	PolicyQuarantine               // p=quarantine
	PolicyReject                   // p=reject
)

func (p Policy) String() string {
	switch p {
	case PolicyNone:
		return "none"
	case PolicyQuarantine:
		return "quarantine"
	case PolicyReject:
		return "reject"
	default:
		return "unknown"
	}
}

// AlignmentMode represents DKIM or SPF alignment mode.
type AlignmentMode int

const (
	AlignmentRelaxed AlignmentMode = iota // r — relaxed alignment
	AlignmentStrict                       // s — strict alignment
)

func (a AlignmentMode) String() string {
	switch a {
	case AlignmentRelaxed:
		return "r"
	case AlignmentStrict:
		return "s"
	default:
		return "r"
	}
}

// DMARCRecord represents a parsed DMARC DNS TXT record.
type DMARCRecord struct {
	Version      string        // "DMARC1"
	Policy       Policy        // p=
	SubdomainPol Policy        // sp= (inherits from p if not set)
	Pct          int           // pct= (default 100)
	RUA          string        // rua=
	RUF          string        // ruf=
	ADKIM        AlignmentMode // adkim= (default r)
	ASPF         AlignmentMode // aspf= (default r)
	Raw          string
}

// EvaluationInput carries the inputs for DMARC evaluation.
type EvaluationInput struct {
	FromDomain        string // RFC5322.From domain
	SPFResult         string // "pass", "fail", "softfail", "neutral", "none", "temperror", "permerror"
	SPFAuthDomain     string // The domain that authenticated via SPF (envelope from domain)
	DKIMResult        string // "pass", "fail", "none", "temperror", "permerror"
	DKIMSigningDomain string // d= tag from DKIM signature
}

// EvaluationResult is the output of DMARC evaluation.
type EvaluationResult struct {
	Result          Result
	Explanation     string
	Policy          Policy
	SubdomainPolicy Policy
	Pct             int
	SPFAligned      bool
	DKIMAligned     bool
	EvaluatedDomain string
	SPFResult       string
	DKIMResult      string
}

// AuthResult is a reusable authentication result model
// for SPF, DKIM, and DMARC (for Authentication-Results header).
type AuthResult struct {
	Method      string // "spf", "dkim", "dmarc"
	Result      string // "pass", "fail", etc.
	Domain      string // The domain evaluated
	Explanation string // Human-readable explanation
}

// AuthResultList holds multiple auth results for header generation.
type AuthResultList struct {
	SPF   *AuthResult
	DKIM  *AuthResult
	DMARC *AuthResult
}
