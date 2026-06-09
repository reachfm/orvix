package spf

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// Evaluator evaluates SPF records for a given context.
type Evaluator struct {
	resolver DNSResolver
}

// NewEvaluator creates a new SPF evaluator.
func NewEvaluator(resolver DNSResolver) *Evaluator {
	return &Evaluator{resolver: resolver}
}

// Evaluate evaluates SPF for the given context.
func (e *Evaluator) Evaluate(ctx context.Context, c *Context) (*EvaluationResult, error) {
	if c == nil {
		return nil, fmt.Errorf("nil context")
	}
	if c.ConnectingIP == nil {
		return nil, fmt.Errorf("missing connecting IP")
	}
	domain := c.MailFromDomain
	if domain == "" {
		domain = c.HeloDomain
	}
	if domain == "" {
		return &EvaluationResult{
			Result:          ResultNone,
			Explanation:     "no domain to evaluate",
			EvaluatedDomain: "",
		}, nil
	}

	if c.evaluatedDomains == nil {
		c.evaluatedDomains = make(map[string]bool)
	}

	result, err := e.evaluateDomain(ctx, c, domain, 0)
	if err != nil {
		return nil, err
	}
	result.EvaluatedDomain = domain
	return result, nil
}

func (e *Evaluator) evaluateDomain(ctx context.Context, c *Context, domain string, depth int) (*EvaluationResult, error) {
	if depth > MaxRecursionDepth {
		return &EvaluationResult{
			Result:          ResultPermError,
			Explanation:     fmt.Sprintf("max recursion depth (%d) exceeded", MaxRecursionDepth),
			EvaluatedDomain: domain,
		}, nil
	}

	if c.evaluatedDomains[domain] {
		return &EvaluationResult{
			Result:          ResultPermError,
			Explanation:     fmt.Sprintf("loop detected: %s already evaluated", domain),
			EvaluatedDomain: domain,
		}, nil
	}
	c.evaluatedDomains[domain] = true

	txts, err := e.resolver.LookupTXT(ctx, domain)
	if err != nil {
		return e.dnsErrorResult(domain, err), nil
	}

	var spfRaw string
	for _, txt := range txts {
		txt = strings.TrimSpace(txt)
		if strings.HasPrefix(txt, "v=spf1") {
			spfRaw = txt
			break
		}
	}

	if spfRaw == "" {
		return &EvaluationResult{
			Result:          ResultNone,
			Explanation:     "no SPF record found",
			EvaluatedDomain: domain,
		}, nil
	}

	record, err := ParseRecord(spfRaw)
	if err != nil {
		return &EvaluationResult{
			Result:          ResultPermError,
			Explanation:     fmt.Sprintf("malformed SPF record: %v", err),
			EvaluatedDomain: domain,
		}, nil
	}

	// Evaluate mechanisms in order.
	for _, mec := range record.Mechanisms {
		match, subResult, err := e.evaluateMechanism(ctx, c, &mec, domain, depth)
		if err != nil {
			return &EvaluationResult{
				Result:          ResultTempError,
				Explanation:     fmt.Sprintf("error evaluating %s: %v", mec.Directive, err),
				MatchedMechanism: mec.Directive,
				EvaluatedDomain:  domain,
			}, nil
		}

		// If the mechanism evaluation produced a definitive non-match
		// result (e.g., include:temperror, include:permerror), propagate it.
		if subResult != nil && subResult.Result != ResultNeutral && subResult.Result != ResultNone {
			return subResult, nil
		}

		if match {
			matchedResult := qualifierToResult(mec.Qualifier)
			return &EvaluationResult{
				Result:           matchedResult,
				Explanation:      matchedResultExplanation(matchedResult, mec.Directive, c.ConnectingIP, domain),
				MatchedMechanism: mec.Directive,
				EvaluatedDomain:  domain,
			}, nil
		}
	}

	// No mechanism matched. Check for redirect modifier.
	if redir, ok := record.Modifiers["redirect"]; ok && redir != "" {
		if c.evaluatedDomains[redir] {
			return &EvaluationResult{
				Result:          ResultPermError,
				Explanation:     fmt.Sprintf("redirect loop detected: %s", redir),
				EvaluatedDomain: domain,
			}, nil
		}
		return e.evaluateDomain(ctx, c, redir, depth+1)
	}

	return &EvaluationResult{
		Result:          ResultNeutral,
		Explanation:     "no mechanism matched",
		EvaluatedDomain: domain,
	}, nil
}

func (e *Evaluator) evaluateMechanism(ctx context.Context, c *Context, mec *Mechanism, domain string, depth int) (bool, *EvaluationResult, error) {
	switch mec.Directive {
	case "all":
		return true, nil, nil

	case "ip4":
		return e.evaluateIP4(c, mec), nil, nil

	case "ip6":
		return e.evaluateIP6(c, mec), nil, nil

	case "a":
		return e.evaluateA(ctx, c, mec, domain)

	case "mx":
		return e.evaluateMX(ctx, c, mec, domain)

	case "include":
		return e.evaluateInclude(ctx, c, mec, domain, depth)

	case "ptr", "exist":
		return false, nil, nil

	default:
		return false, nil, fmt.Errorf("unknown mechanism: %s", mec.Directive)
	}
}

func (e *Evaluator) evaluateIP4(c *Context, mec *Mechanism) bool {
	ip := c.ConnectingIP.To4()
	if ip == nil {
		return false
	}
	cidr := mec.CIDRLen
	if cidr < 0 || cidr > 32 {
		cidr = 32
	}
	netIP := net.ParseIP(mec.DomainSpec).To4()
	if netIP == nil {
		return false
	}
	mask := net.CIDRMask(cidr, 32)
	return ip.Mask(mask).Equal(netIP.Mask(mask))
}

func (e *Evaluator) evaluateIP6(c *Context, mec *Mechanism) bool {
	ip := c.ConnectingIP.To16()
	if ip == nil || c.ConnectingIP.To4() != nil {
		return false
	}
	cidr := mec.CIDRLen6
	if cidr < 0 || cidr > 128 {
		cidr = 128
	}
	netIP := net.ParseIP(mec.DomainSpec).To16()
	if netIP == nil {
		return false
	}
	mask := net.CIDRMask(cidr, 128)
	return ip.Mask(mask).Equal(netIP.Mask(mask))
}

func (e *Evaluator) evaluateA(ctx context.Context, c *Context, mec *Mechanism, domain string) (bool, *EvaluationResult, error) {
	targetDomain := mec.DomainSpec
	if targetDomain == "" {
		targetDomain = domain
	}
	cidr := mec.CIDRLen
	if cidr < 0 {
		cidr = 32
	}

	ips4, err := e.resolver.LookupA(ctx, targetDomain)
	if err == nil {
		ip4 := c.ConnectingIP.To4()
		if ip4 != nil {
			mask := net.CIDRMask(cidr, 32)
			for _, ip := range ips4 {
				if ip4.Mask(mask).Equal(ip.Mask(mask)) {
					return true, nil, nil
				}
			}
		}
	}

	if c.ConnectingIP.To4() == nil {
		ips6, err := e.resolver.LookupAAAA(ctx, targetDomain)
		if err == nil {
			for _, ip := range ips6 {
				if c.ConnectingIP.Equal(ip) {
					return true, nil, nil
				}
			}
		}
	}

	return false, nil, nil
}

func (e *Evaluator) evaluateMX(ctx context.Context, c *Context, mec *Mechanism, domain string) (bool, *EvaluationResult, error) {
	targetDomain := mec.DomainSpec
	if targetDomain == "" {
		targetDomain = domain
	}
	cidr := mec.CIDRLen
	if cidr < 0 {
		cidr = 32
	}

	mxs, err := e.resolver.LookupMX(ctx, targetDomain)
	if err != nil {
		return false, nil, nil
	}

	ip4 := c.ConnectingIP.To4()
	mask := net.CIDRMask(cidr, 32)

	for _, mx := range mxs {
		host := strings.TrimSuffix(mx.Host, ".")

		ips4, err := e.resolver.LookupA(ctx, host)
		if err == nil && ip4 != nil {
			for _, ip := range ips4 {
				if ip4.Mask(mask).Equal(ip.Mask(mask)) {
					return true, nil, nil
				}
			}
		}

		if ip4 == nil {
			ips6, err := e.resolver.LookupAAAA(ctx, host)
			if err == nil {
				for _, ip := range ips6 {
					if c.ConnectingIP.Equal(ip) {
						return true, nil, nil
					}
				}
			}
		}
	}

	return false, nil, nil
}

func (e *Evaluator) evaluateInclude(ctx context.Context, c *Context, mec *Mechanism, domain string, depth int) (bool, *EvaluationResult, error) {
	targetDomain := mec.DomainSpec
	if targetDomain == "" {
		return false, nil, nil
	}

	subResult, err := e.evaluateDomain(ctx, c, targetDomain, depth+1)
	if err != nil {
		return false, nil, err
	}

	switch subResult.Result {
	case ResultPass:
		return true, nil, nil
	case ResultFail, ResultSoftFail, ResultNeutral, ResultNone:
		return false, nil, nil
	case ResultTempError:
		return false, &EvaluationResult{
			Result:           ResultTempError,
			Explanation:      fmt.Sprintf("include:%s returned temperror", targetDomain),
			MatchedMechanism: "include:" + targetDomain,
			EvaluatedDomain:  domain,
		}, nil
	case ResultPermError:
		return false, &EvaluationResult{
			Result:           ResultPermError,
			Explanation:      fmt.Sprintf("include:%s returned permerror", targetDomain),
			MatchedMechanism: "include:" + targetDomain,
			EvaluatedDomain:  domain,
		}, nil
	default:
		return false, nil, nil
	}
}

func (e *Evaluator) dnsErrorResult(domain string, err error) *EvaluationResult {
	var dnsErr *net.DNSError
	if ok := AsDNSError(err, &dnsErr); ok {
		if dnsErr.IsNotFound {
			return &EvaluationResult{
				Result:          ResultNone,
				Explanation:     fmt.Sprintf("DNS record not found for %s", domain),
				EvaluatedDomain: domain,
			}
		}
		return &EvaluationResult{
			Result:          ResultTempError,
			Explanation:     fmt.Sprintf("DNS error for %s: %v", domain, err),
			EvaluatedDomain: domain,
		}
	}
	return &EvaluationResult{
		Result:          ResultTempError,
		Explanation:     fmt.Sprintf("DNS error for %s: %v", domain, err),
		EvaluatedDomain: domain,
	}
}

func qualifierToResult(q Qualifier) Result {
	switch q {
	case QualPass:
		return ResultPass
	case QualFail:
		return ResultFail
	case QualSoftFail:
		return ResultSoftFail
	case QualNeutral:
		return ResultNeutral
	default:
		return ResultNeutral
	}
}

func matchedResultExplanation(r Result, directive string, ip net.IP, domain string) string {
	switch r {
	case ResultPass:
		return fmt.Sprintf("%s matches %s for domain %s", ip, directive, domain)
	case ResultFail:
		return fmt.Sprintf("%s not authorized via %s for domain %s", ip, directive, domain)
	case ResultSoftFail:
		return fmt.Sprintf("%s unlikely to be authorized via %s for domain %s", ip, directive, domain)
	default:
		return fmt.Sprintf("mechanism %s matched for domain %s", directive, domain)
	}
}

func AsDNSError(err error, target **net.DNSError) bool {
	if err == nil {
		return false
	}
	dnsErr, ok := err.(*net.DNSError)
	if ok {
		*target = dnsErr
	}
	return ok
}
