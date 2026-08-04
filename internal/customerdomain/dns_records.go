package customerdomain

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// This file implements the record checks that go beyond the six original
// summary checks (MX/SPF/DKIM/DMARC/MTA-STS/TLS-RPT):
//
//   - forward addressing of the mail host (A / AAAA)
//   - resolution of every published MX hostname to A/AAAA
//   - reverse DNS (PTR) of the mail host's primary IPv4 address
//   - Outlook autodiscover and Thunderbird autoconfig delegation records
//   - TLSA / DANE
//
// All of them go through the same dnsops.Resolver the existing checks use,
// under the same single inspector-level context timeout, so they inherit the
// identical SSRF/egress and deadline discipline. No new network client, no
// new egress path, and no HTTP fetch is introduced here.
//
// Requiredness is decided from what ORVIX ACTUALLY configures, not from what
// would be nice:
//
//   - A of the mail host and resolution of every MX host are REQUIRED: mail
//     cannot be delivered without them.
//   - AAAA is OPTIONAL: nothing in internal/config requires IPv6.
//   - PTR is REQUIRED: large receivers reject senders lacking rDNS. It is
//     downgraded to a warning (not a hard fail) when it merely does not match
//     the mail host, because a provider-assigned generic rDNS still delivers.
//   - autodiscover/autoconfig are OPTIONAL: ORVIX serves both protocols
//     itself from its own web host (internal/api/router.go registers
//     /autodiscover/autodiscover.xml and
//     /.well-known/autoconfig/mail/config-v1.1.xml), so client setup works
//     with no delegation record at all.
//   - TLSA is NOT_APPLICABLE: there is no DANE/TLSA configuration surface
//     anywhere in internal/config, so ORVIX asserts no requirement. We report
//     it explicitly rather than fabricating one or hiding the row.

// primaryMailHost returns the first expected MX hostname with any leading
// preference number and trailing dot stripped.
func primaryMailHost(expectedMX []string, domain string) string {
	for _, raw := range expectedMX {
		h := strings.TrimSpace(raw)
		if h == "" {
			continue
		}
		if fields := strings.Fields(h); len(fields) == 2 {
			if _, err := fmt.Sscanf(fields[0], "%d", new(int)); err == nil {
				h = fields[1]
			}
		}
		h = strings.TrimSuffix(h, ".")
		if h != "" {
			return h
		}
	}
	return "mail." + domain
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// checkHostA verifies that host has at least one IPv4 address. Required.
func (i *DNSInspector) checkHostA(ctx context.Context, host, now string) *DNSRecordCheck {
	c := &DNSRecordCheck{
		Name:      host,
		Type:      "A",
		Expected:  "one or more public IPv4 addresses",
		CheckedAt: now,
		Guidance:  fmt.Sprintf("Add an A record for %s pointing to the public IPv4 address of the ORVIX mail server.", host),
	}
	ips, err := i.dns.LookupA(ctx, host)
	if err != nil {
		switch {
		case isDNSNotFound(err):
			c.Status = string(DNSStatusFail)
			c.Reason = fmt.Sprintf("no A record found for %s", host)
		case isDNSTimeout(err):
			c.Status = string(DNSStatusUnknown)
			c.Reason = "dns timeout"
		default:
			c.Status = string(DNSStatusUnknown)
			c.Reason = fmt.Sprintf("dns error: %v", err)
		}
		return c
	}
	if len(ips) == 0 {
		c.Status = string(DNSStatusFail)
		c.Reason = fmt.Sprintf("no A record found for %s", host)
		return c
	}
	c.Observed = ipStrings(ips)
	c.Status = string(DNSStatusPass)
	return c
}

// checkHostAAAA verifies IPv6 addressing. Optional: absence is reported as
// "optional", which is excluded from scoring entirely.
func (i *DNSInspector) checkHostAAAA(ctx context.Context, host, now string) *DNSRecordCheck {
	c := &DNSRecordCheck{
		Name:      host,
		Type:      "AAAA",
		Expected:  "one or more public IPv6 addresses (optional)",
		CheckedAt: now,
		Optional:  true,
		Guidance:  fmt.Sprintf("Optional. If the mail server has IPv6 connectivity, add an AAAA record for %s pointing to its public IPv6 address.", host),
	}
	ips, err := i.dns.LookupAAAA(ctx, host)
	if err != nil || len(ips) == 0 {
		c.Status = string(DNSStatusOptional)
		c.Reason = fmt.Sprintf("no AAAA record published for %s; IPv6 is not required by ORVIX", host)
		return c
	}
	c.Observed = ipStrings(ips)
	c.Status = string(DNSStatusPass)
	c.Optional = false
	return c
}

// checkMXHostResolution resolves each MX hostname the domain publishes. An MX
// that resolves to nothing is a hard delivery failure and is REQUIRED.
func (i *DNSInspector) checkMXHostResolution(ctx context.Context, domain, now string) []*DNSRecordCheck {
	mx, err := i.dns.LookupMX(ctx, domain)
	if err != nil || len(mx) == 0 {
		return []*DNSRecordCheck{{
			Name:      domain,
			Type:      "MX-host",
			Status:    string(DNSStatusFail),
			Expected:  "every published MX hostname must resolve to at least one A or AAAA address",
			Reason:    "no MX records published, so no MX hostname could be resolved",
			Guidance:  fmt.Sprintf("Publish an MX record for %s before MX host resolution can be checked.", domain),
			CheckedAt: now,
		}}
	}

	out := make([]*DNSRecordCheck, 0, len(mx))
	seen := make(map[string]bool, len(mx))
	for _, m := range mx {
		host := strings.TrimSuffix(m.Host, ".")
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true

		c := &DNSRecordCheck{
			Name:      host,
			Type:      "MX-host",
			Expected:  "at least one A or AAAA address",
			CheckedAt: now,
			Guidance:  fmt.Sprintf("Add an A record (and optionally AAAA) for the MX host %s pointing to the mail server's public IP address; an MX target that does not resolve cannot receive mail.", host),
		}

		var observed []string
		if a, err := i.dns.LookupA(ctx, host); err == nil {
			for _, ip := range a {
				observed = append(observed, "A "+ip.String())
			}
		}
		if aaaa, err := i.dns.LookupAAAA(ctx, host); err == nil {
			for _, ip := range aaaa {
				observed = append(observed, "AAAA "+ip.String())
			}
		}

		if len(observed) == 0 {
			c.Status = string(DNSStatusFail)
			c.Reason = fmt.Sprintf("MX host %s does not resolve to any A or AAAA address", host)
		} else {
			c.Status = string(DNSStatusPass)
			c.Observed = observed
		}
		out = append(out, c)
	}
	return out
}

// checkPTR performs a reverse lookup of the mail host's first IPv4 address.
func (i *DNSInspector) checkPTR(ctx context.Context, host string, a *DNSRecordCheck, now string) *DNSRecordCheck {
	c := &DNSRecordCheck{
		Type:      "PTR",
		Expected:  host,
		CheckedAt: now,
	}

	if a == nil || len(a.Observed) == 0 {
		c.Name = host
		c.Status = string(DNSStatusFail)
		c.Reason = "mail host has no IPv4 address, so no reverse DNS record can exist"
		c.Guidance = fmt.Sprintf("Resolve the missing A record for %s first; reverse DNS is configured against that IP address.", host)
		return c
	}

	ip := a.Observed[0]
	c.Name = ip
	c.Guidance = fmt.Sprintf("Ask the network or hosting provider that owns %s to set its reverse DNS (PTR) record to %s. Receivers commonly reject mail from IPs whose rDNS is missing or generic.", ip, host)

	names, err := i.dns.LookupPTR(ctx, ip)
	if err != nil || len(names) == 0 {
		c.Status = string(DNSStatusFail)
		c.Reason = fmt.Sprintf("no PTR record published for %s", ip)
		return c
	}

	observed := make([]string, 0, len(names))
	matched := false
	for _, n := range names {
		n = strings.TrimSuffix(n, ".")
		observed = append(observed, n)
		if strings.EqualFold(n, host) {
			matched = true
		}
	}
	c.Observed = observed

	if matched {
		c.Status = string(DNSStatusPass)
		return c
	}
	// rDNS exists but does not match the mail host. Mail still delivers in
	// most cases, so this is a warning rather than a hard failure.
	c.Status = string(DNSStatusWarning)
	c.Reason = fmt.Sprintf("PTR for %s is %s, which does not match the mail host %s", ip, strings.Join(observed, ", "), host)
	return c
}

// checkDelegationCNAME checks an optional client-provisioning delegation name
// (autodiscover / autoconfig). The resolver interface exposes no CNAME or SRV
// lookup, so presence is established by resolving the name to an address —
// which is exactly what a mail client does when it follows the CNAME. Absence
// is OPTIONAL, never a failure, because ORVIX serves both protocols from its
// own web host regardless.
func (i *DNSInspector) checkDelegationCNAME(ctx context.Context, name, target, label, now string) *DNSRecordCheck {
	c := &DNSRecordCheck{
		Name:      name,
		Type:      "CNAME",
		Expected:  target,
		Optional:  true,
		CheckedAt: now,
		Guidance:  fmt.Sprintf("Optional. Add a CNAME record for %s pointing to %s so %s clients discover their settings automatically. ORVIX also serves %s directly from its own web host, so mail works without this record.", name, target, label, label),
	}

	var observed []string
	if a, err := i.dns.LookupA(ctx, name); err == nil {
		for _, ip := range a {
			observed = append(observed, "A "+ip.String())
		}
	}
	if aaaa, err := i.dns.LookupAAAA(ctx, name); err == nil {
		for _, ip := range aaaa {
			observed = append(observed, "AAAA "+ip.String())
		}
	}

	if len(observed) == 0 {
		c.Status = string(DNSStatusOptional)
		c.Reason = fmt.Sprintf("%s is not published; %s autodiscovery falls back to the ORVIX-hosted endpoint", name, label)
		return c
	}
	c.Observed = observed
	c.Status = string(DNSStatusPass)
	c.Optional = false
	return c
}

// checkTLSA reports DANE/TLSA state. ORVIX exposes no DANE configuration
// anywhere (there is no dane/tlsa key in internal/config), so there is no
// requirement to assert and the record is always not_applicable. We do NOT
// invent a requirement, and we do NOT silently drop the row.
func (i *DNSInspector) checkTLSA(host, now string) *DNSRecordCheck {
	return &DNSRecordCheck{
		Name:      "_25._tcp." + host,
		Type:      "TLSA",
		Status:    string(DNSStatusNotApplicable),
		Optional:  true,
		Reason:    "DANE/TLSA is not a configurable feature of this ORVIX deployment, so no TLSA record is required",
		Guidance:  "No action required. ORVIX does not offer DANE/TLSA configuration, so this record is neither published nor validated.",
		CheckedAt: now,
	}
}

// checkAutodiscoverSRV resolves _autodiscover._tcp.<domain> and compares
// every SRV field (priority, weight, port, target) against the single
// canonical, operator-CONFIGURED expectation. Nothing here is invented: the
// expected target/port come from CanonicalExpectations (coremail
// autodiscover_srv_* falling back to the domain's own primary mail host on
// 443, which is where ORVIX itself serves /autodiscover/autodiscover.xml —
// see internal/api/router.go).
//
// RFC 2782 permits several answers for one name. A client picks ONE by
// priority then weighted random, so any single answer may be the one used.
// We therefore treat the record as correct when AT LEAST ONE answer matches
// the expectation exactly, and report every answer in Observed. When
// some-but-not-all answers match we downgrade to warning and name the
// non-matching ones: a stray answer sends a share of clients to the wrong
// endpoint, and hiding that would be worse than a noisy row.
//
// The row is OPTIONAL throughout: a missing SRV record breaks neither mail
// flow nor client setup (ORVIX serves autodiscover directly), so it must
// never drag the health score down.
// formatSRVAnswers renders SRV answers in zone-file order
// (priority weight port target.) so the observed column reads identically to
// the expected column.
func formatSRVAnswers(answers []*net.SRV) []string {
	out := make([]string, 0, len(answers))
	for _, a := range answers {
		if a == nil {
			continue
		}
		out = append(out, fmt.Sprintf("%d %d %d %s.", a.Priority, a.Weight, a.Port, strings.TrimSuffix(a.Target, ".")))
	}
	return out
}

func (i *DNSInspector) checkAutodiscoverSRV(ctx context.Context, domain, mailHost, now string) *DNSRecordCheck {
	name := AutodiscoverSRVName(domain)

	expected, ok := i.expectations.AutodiscoverSRVExpectedString(mailHost)
	if !ok {
		// No configured target. The target is NOT guessed from the mail
		// host: autodiscover may be terminated by a different host, so a
		// guessed expectation would report "wrong target" against a
		// perfectly correct record. Still resolve the record so the
		// operator can SEE what is published today — just do not grade it.
		row := &DNSRecordCheck{
			Name:      name,
			Type:      "SRV",
			Status:    string(DNSStatusConfigRequired),
			Optional:  true,
			Reason:    "server configuration " + ConfigKeySRVTarget + " is not set, so ORVIX cannot state a required autodiscover SRV target for this domain",
			Guidance:  "Optional record. Set " + ConfigKeySRVTarget + " (and optionally the matching port/priority/weight settings) to the host that actually terminates autodiscover for this deployment, then re-check. ORVIX will not guess a target or grade the published record until it is set.",
			CheckedAt: now,
		}
		if answers, err := i.dns.LookupSRV(ctx, name); err == nil && len(answers) > 0 {
			row.Observed = formatSRVAnswers(answers)
		}
		return row
	}

	wantTarget, wantPort, wantPriority, wantWeight, _ := i.expectations.AutodiscoverSRV(mailHost)
	guidance := fmt.Sprintf("Optional. Publish an SRV record at %s with the value %q (priority %d, weight %d, port %d, target %s).",
		name, expected, wantPriority, wantWeight, wantPort, wantTarget)

	answers, err := i.dns.LookupSRV(ctx, name)
	if err != nil {
		if isDNSNotFound(err) {
			return &DNSRecordCheck{
				Name: name, Type: "SRV",
				Status: string(DNSStatusOptional), Optional: true,
				Expected:  expected,
				Reason:    "no autodiscover SRV record published",
				Guidance:  guidance,
				CheckedAt: now,
			}
		}
		// A resolver failure or timeout is NOT the same as "not
		// published": we do not know, and must claim neither.
		reason := fmt.Sprintf("dns error: %v", err)
		if isDNSTimeout(err) {
			reason = fmt.Sprintf("dns lookup timed out: %v", err)
		}
		return &DNSRecordCheck{
			Name: name, Type: "SRV",
			Status: string(DNSStatusUnknown), Optional: true,
			Expected:  expected,
			Reason:    reason,
			Guidance:  guidance,
			CheckedAt: now,
		}
	}

	observed := make([]string, 0, len(answers))
	mismatches := make([]string, 0, len(answers))
	matched := false
	for _, a := range answers {
		if a == nil {
			continue
		}
		target := strings.TrimSuffix(a.Target, ".")
		observed = append(observed, fmt.Sprintf("%d %d %d %s.", a.Priority, a.Weight, a.Port, target))
		if strings.EqualFold(target, wantTarget) && int(a.Port) == wantPort &&
			int(a.Priority) == wantPriority && int(a.Weight) == wantWeight {
			matched = true
			continue
		}
		switch {
		case !strings.EqualFold(target, wantTarget):
			mismatches = append(mismatches, fmt.Sprintf("target %s (want %s)", target, wantTarget))
		case int(a.Port) != wantPort:
			mismatches = append(mismatches, fmt.Sprintf("port %d (want %d)", a.Port, wantPort))
		default:
			mismatches = append(mismatches, fmt.Sprintf("priority/weight %d/%d (want %d/%d)", a.Priority, a.Weight, wantPriority, wantWeight))
		}
	}

	if len(observed) == 0 {
		return &DNSRecordCheck{
			Name: name, Type: "SRV",
			Status: string(DNSStatusOptional), Optional: true,
			Expected:  expected,
			Reason:    "no autodiscover SRV record published",
			Guidance:  guidance,
			CheckedAt: now,
		}
	}

	check := &DNSRecordCheck{
		Name: name, Type: "SRV",
		Optional:  true,
		Expected:  expected,
		Observed:  observed,
		Guidance:  guidance,
		CheckedAt: now,
	}
	switch {
	case matched && len(mismatches) == 0:
		check.Status = string(DNSStatusPass)
	case matched:
		check.Status = string(DNSStatusWarning)
		check.Reason = "an SRV answer matches the expected endpoint, but additional answers do not: " + strings.Join(mismatches, "; ")
	default:
		check.Status = string(DNSStatusWarning)
		check.Reason = "published autodiscover SRV does not match the expected endpoint: " + strings.Join(mismatches, "; ")
	}
	return check
}

// NOTE: the former canonicalSPF()/canonicalDMARC() helpers lived here and
// hard-coded "v=spf1 mx -all" and "rua=mailto:dmarc@<domain>". They were
// removed deliberately: hard-coding silently overrode this deployment's real
// SPF policy and invented an unprovisioned DMARC mailbox. The required values
// now come from CanonicalExpectations (see expectations.go), which is fed
// from config in internal/api/router.go. Do not reintroduce a second source.

// canonicalMTASTS / canonicalTLSRPT are the required values for the two
// policy-reporting TXT records.
func canonicalMTASTS() string { return "v=STSv1; id=<policy-id>" }

func canonicalTLSRPT(domain string) string {
	return fmt.Sprintf("v=TLSRPTv1; rua=mailto:tlsrpt@%s", domain)
}
