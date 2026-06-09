package delivery

import (
	"bytes"
	"strings"
)

// LoopDetector checks for mail loops that could cause infinite delivery attempts.
type LoopDetector struct {
	MaxReceivedHeaders int
	MaxDeferralCount   int
	localDomain        string
}

// NewLoopDetector creates a loop detector.
func NewLoopDetector(maxReceivedHeaders, maxDeferralCount int, localDomain string) *LoopDetector {
	return &LoopDetector{
		MaxReceivedHeaders: maxReceivedHeaders,
		MaxDeferralCount:   maxDeferralCount,
		localDomain:        localDomain,
	}
}

// LoopResult holds the outcome of a loop detection check.
type LoopResult struct {
	IsLoop bool
	Reason string
}

// CheckReceivedHeaders counts Received headers in the raw message.
// If the count exceeds the threshold, it's likely a loop.
func (ld *LoopDetector) CheckReceivedHeaders(rfc822Data []byte) *LoopResult {
	if ld.MaxReceivedHeaders <= 0 {
		return &LoopResult{}
	}
	count := 0
	lines := bytes.Split(rfc822Data, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 10 && bytes.HasPrefix(bytes.ToUpper(trimmed), []byte("RECEIVED:")) {
			count++
			if count > ld.MaxReceivedHeaders {
				return &LoopResult{
					IsLoop: true,
					Reason: "excessive Received headers",
				}
			}
		}
	}
	return &LoopResult{}
}

// CheckSelfDelivery detects if a message is being sent to a local domain
// that this server serves, which could indicate a loop.
func (ld *LoopDetector) CheckSelfDelivery(recipientDomain string) *LoopResult {
	if ld.localDomain == "" {
		return &LoopResult{}
	}
	if strings.EqualFold(recipientDomain, ld.localDomain) {
		return &LoopResult{
			IsLoop: true,
			Reason: "self-delivery loop detected",
		}
	}
	return &LoopResult{}
}

// CheckDeferralLoop detects repeated deferral patterns on the same queue entry.
func (ld *LoopDetector) CheckDeferralLoop(attemptCount, maxAttempts int) *LoopResult {
	if ld.MaxDeferralCount <= 0 {
		ld.MaxDeferralCount = 10
	}
	if maxAttempts <= 0 {
		maxAttempts = 16
	}
	// Use the lower of the two thresholds.
	threshold := ld.MaxDeferralCount
	if maxAttempts < threshold {
		threshold = maxAttempts
	}
	if attemptCount >= threshold {
		return &LoopResult{
			IsLoop: true,
			Reason: "excessive deferral count",
		}
	}
	return &LoopResult{}
}
