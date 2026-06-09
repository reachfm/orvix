package dmarc

import (
	"fmt"
	"net"
	"strings"
)

// FormatAuthResults generates an RFC 8601-style Authentication-Results header value.
// Format: "mx.example.com; spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com; dmarc=pass header.from=example.com"
func FormatAuthResults(authResults *AuthResultList, authServID string, remoteIP net.IP) string {
	if authResults == nil {
		return ""
	}

	parts := make([]string, 0, 4)
	parts = append(parts, authServID)

	if authResults.SPF != nil {
		spfPart := fmt.Sprintf("spf=%s smtp.mailfrom=%s", authResults.SPF.Result, authResults.SPF.Domain)
		parts = append(parts, spfPart)
	}

	if authResults.DKIM != nil {
		dkimPart := fmt.Sprintf("dkim=%s header.d=%s", authResults.DKIM.Result, authResults.DKIM.Domain)
		parts = append(parts, dkimPart)
	}

	if authResults.DMARC != nil {
		dmarcPart := fmt.Sprintf("dmarc=%s", authResults.DMARC.Result)
		if authResults.DMARC.Domain != "" {
			dmarcPart += fmt.Sprintf(" header.from=%s", authResults.DMARC.Domain)
		}
		parts = append(parts, dmarcPart)
	}

	return strings.Join(parts, "; ")
}

// AuthResultFromDMARC converts a DMARC evaluation result into an AuthResult.
func AuthResultFromDMARC(result *EvaluationResult) *AuthResult {
	if result == nil {
		return &AuthResult{
			Method: "dmarc",
			Result: "none",
		}
	}
	return &AuthResult{
		Method:      "dmarc",
		Result:      result.Result.String(),
		Domain:      result.EvaluatedDomain,
		Explanation: result.Explanation,
	}
}

// PolicyExplanation returns a human-readable explanation of the DMARC policy.
func PolicyExplanation(p Policy) string {
	switch p {
	case PolicyNone:
		return "no action required"
	case PolicyQuarantine:
		return "message should be quarantined"
	case PolicyReject:
		return "message should be rejected"
	default:
		return "unknown policy"
	}
}
