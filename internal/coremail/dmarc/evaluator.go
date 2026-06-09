package dmarc

import (
	"fmt"
	"strings"
)

// Evaluator evaluates DMARC policy for a given set of inputs.
type Evaluator struct {
	resolver DNSResolver
}

// NewEvaluator creates a DMARC evaluator.
func NewEvaluator(resolver DNSResolver) *Evaluator {
	return &Evaluator{resolver: resolver}
}

// DNSResolver abstracts DMARC DNS TXT lookups.
type DNSResolver interface {
	LookupTXT(domain string) ([]string, error)
}

// Evaluate performs DMARC evaluation for the given inputs.
func (e *Evaluator) Evaluate(input *EvaluationInput) (*EvaluationResult, error) {
	if input == nil {
		return nil, fmt.Errorf("nil evaluation input")
	}
	if input.FromDomain == "" {
		return &EvaluationResult{
			Result:  ResultNone,
			Explanation: "no From domain provided",
		}, nil
	}

	// Determine the organizational domain for DMARC lookup (per RFC 7489 §6.3).
	dmarcTarget := input.FromDomain
	if !strings.Contains(dmarcTarget, ".") {
		dmarcTarget = extractOrganizationalDomain(dmarcTarget)
	} else {
		// Use the extract function which handles two-part TLDs.
		orgDomain := extractOrganizationalDomain(dmarcTarget)
		if orgDomain != "" {
			dmarcTarget = orgDomain
		}
	}

	dmarcDomain := "_dmarc." + dmarcTarget

	txts, err := e.resolver.LookupTXT(dmarcDomain)
	if err != nil {
		return e.dnsErrorResult(dmarcTarget, err), nil
	}

	var dmarcRaw string
	for _, txt := range txts {
		txt = strings.TrimSpace(txt)
		if strings.HasPrefix(txt, "v=DMARC1") {
			dmarcRaw = txt
			break
		}
	}

	if dmarcRaw == "" {
		return &EvaluationResult{
			Result:          ResultNone,
			Explanation:     "no DMARC record found",
			EvaluatedDomain: dmarcTarget,
		}, nil
	}

	record, err := ParseRecord(dmarcRaw)
	if err != nil {
		return &EvaluationResult{
			Result:          ResultPermError,
			Explanation:     fmt.Sprintf("malformed DMARC record: %v", err),
			EvaluatedDomain: input.FromDomain,
		}, nil
	}

	return e.evaluateWithRecord(input, record), nil
}

func (e *Evaluator) evaluateWithRecord(input *EvaluationInput, record *DMARCRecord) *EvaluationResult {
	res := &EvaluationResult{
		Policy:          record.Policy,
		SubdomainPolicy: record.SubdomainPol,
		Pct:             record.Pct,
		EvaluatedDomain: input.FromDomain,
		SPFResult:       input.SPFResult,
		DKIMResult:      input.DKIMResult,
	}

	// Check alignment.
	res.SPFAligned = CheckSPFAlignment(input.FromDomain, input.SPFAuthDomain, record.ASPF)
	res.DKIMAligned = CheckDKIMAlignment(input.FromDomain, input.DKIMSigningDomain, record.ADKIM)

	// Determine overall DMARC result per RFC 7489 §6.7.
	spfPass := spfResultIsPass(input.SPFResult) && res.SPFAligned
	dkimPass := dkimResultIsPass(input.DKIMResult) && res.DKIMAligned

	if !spfPass && !dkimPass {
		res.Result = ResultFail
		res.Explanation = "no aligned authentication passed"

		if input.SPFResult == "" || input.SPFResult == "none" {
			if input.DKIMResult == "" || input.DKIMResult == "none" {
				res.Result = ResultNone
				res.Explanation = "no authentication performed"
			}
		}

		return res
	}

	res.Result = ResultPass
	res.Explanation = "aligned authentication passed"
	return res
}

func (e *Evaluator) dnsErrorResult(domain string, err error) *EvaluationResult {
	return &EvaluationResult{
		Result:          ResultTempError,
		Explanation:     fmt.Sprintf("DNS error for _dmarc.%s: %v", domain, err),
		EvaluatedDomain: domain,
	}
}

// spfResultIsPass returns true if the SPF result indicates a pass
// suitable for DMARC (pass only, not softfail/neutral).
func spfResultIsPass(result string) bool {
	return strings.EqualFold(result, "pass")
}

// dkimResultIsPass returns true if the DKIM result indicates a pass.
func dkimResultIsPass(result string) bool {
	return strings.EqualFold(result, "pass")
}
