package spf

import (
	"fmt"
	"net"
)

// FormatReceivedSPF generates an RFC-style Received-SPF header value.
// Format: "Received-SPF: <result> (explanation) receiver=<hostname>; identity=mailfrom; envelope-from=<domain>; client-ip=<ip>"
func FormatReceivedSPF(result *EvaluationResult, clientIP net.IP, receiverHostname string) string {
	if result == nil {
		return ""
	}

	envelopeFrom := result.EvaluatedDomain
	if envelopeFrom == "" {
		envelopeFrom = "unknown"
	}

	line := fmt.Sprintf("%s (%s) receiver=%s; identity=mailfrom; envelope-from=%s; client-ip=%s",
		result.Result,
		result.Explanation,
		receiverHostname,
		envelopeFrom,
		clientIP.String(),
	)

	return line
}

// AuthResultFromSPF converts an SPF evaluation result to a generic AuthResult.
func AuthResultFromSPF(result *EvaluationResult) *AuthResult {
	if result == nil {
		return &AuthResult{
			Method: "spf",
			Result: "none",
		}
	}
	return &AuthResult{
		Method:      "spf",
		Result:      result.Result.String(),
		Domain:      result.EvaluatedDomain,
		Explanation: result.Explanation,
	}
}
