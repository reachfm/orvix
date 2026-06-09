package dmarc

import "strings"

// CheckSPFAlignment checks whether SPF is aligned with the RFC5322.From domain.
// Relaxed: SPF domain must share the same organizational domain.
// Strict: SPF domain must exactly match From domain.
func CheckSPFAlignment(fromDomain, spfDomain string, mode AlignmentMode) bool {
	if fromDomain == "" || spfDomain == "" {
		return false
	}
	if mode == AlignmentStrict {
		return strings.EqualFold(fromDomain, spfDomain)
	}
	// Relaxed: both domains must share the same organizational domain.
	return sameOrganizationalDomain(fromDomain, spfDomain)
}

// CheckDKIMAlignment checks whether DKIM is aligned with the RFC5322.From domain.
// Relaxed: DKIM signing domain must share the same organizational domain.
// Strict: DKIM signing domain must exactly match From domain.
func CheckDKIMAlignment(fromDomain, dkimSigningDomain string, mode AlignmentMode) bool {
	if fromDomain == "" || dkimSigningDomain == "" {
		return false
	}
	if mode == AlignmentStrict {
		return strings.EqualFold(fromDomain, dkimSigningDomain)
	}
	// Relaxed: both domains must share the same organizational domain.
	return sameOrganizationalDomain(fromDomain, dkimSigningDomain)
}

// sameOrganizationalDomain checks whether two domains share the same
// organizational domain (the last two non-TLD labels).
// Examples:
//
//	example.com, mail.example.com        → true (org domain: example.com)
//	example.com, example.org             → false
//	example.co.uk, sub.example.co.uk     → true
//	example.com, Example.com             → true
func sameOrganizationalDomain(a, b string) bool {
	orgA := extractOrganizationalDomain(a)
	orgB := extractOrganizationalDomain(b)
	return strings.EqualFold(orgA, orgB)
}

// extractOrganizationalDomain extracts the organizational domain
// (the registrable domain portion) from a fully qualified domain.
// For most domains this is the last 2 labels (e.g., "example.com" from "sub.example.com").
// For known two-part TLDs, it's the last 3 labels.
func extractOrganizationalDomain(domain string) string {
	// Remove trailing dot.
	domain = strings.TrimSuffix(domain, ".")
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return domain
	}

	// Check for known two-part TLDs.
	tld := labels[len(labels)-1]
	sld := labels[len(labels)-2]
	combo := sld + "." + tld

	if isTwoPartTLD(combo) {
		if len(labels) < 3 {
			return domain
		}
		// org domain = labels[len(labels)-3] + "." + combo
		return labels[len(labels)-3] + "." + combo
	}

	// Normal case: last 2 labels.
	return labels[len(labels)-2] + "." + labels[len(labels)-1]
}

// isTwoPartTLD checks if a domain suffix is a known two-part TLD.
// This is a minimal list; a production system would use the publicsuffix package.
func isTwoPartTLD(suffix string) bool {
	twoPart := map[string]bool{
		"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
		"co.jp": true, "or.jp": true, "ne.jp": true, "ac.jp": true,
		"com.au": true, "net.au": true, "org.au": true, "edu.au": true, "gov.au": true,
		"co.nz": true, "net.nz": true, "org.nz": true,
		"co.kr": true, "or.kr": true, "ne.kr": true,
		"com.br": true, "org.br": true, "net.br": true, "gov.br": true,
		"co.za": true, "org.za": true, "net.za": true, "gov.za": true,
		"com.cn": true, "net.cn": true, "org.cn": true, "gov.cn": true,
		"co.in": true, "net.in": true, "org.in": true, "firm.in": true, "gen.in": true, "ind.in": true,
	}
	return twoPart[suffix]
}
