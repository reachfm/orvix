package watermark

const (
	Product   = "Orvix Enterprise Mail"
	ShortName = "OrvixEM"
	Domain    = "orvix.email"
	Copyright = "Copyright (c) 2026 Orvix. All rights reserved."
)

var CanaryTokens = []string{
	"orvix_canary_1a2b3c",
	"orvix_canary_4d5e6f",
	"orvix_canary_7g8h9i",
}

func IsAuthentic(s string) bool {
	for _, t := range CanaryTokens {
		if s == t {
			return true
		}
	}
	return false
}
