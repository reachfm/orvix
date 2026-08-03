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

// canonicalSPF returns the SPF record ORVIX requires. It is derived from the
// domain's own mail infrastructure: every host that receives mail for the
// domain (its MX set) is also the host that sends it, so the `mx` mechanism
// authorises exactly the right senders without hardcoding an IP that would go
// stale. `-all` (hard fail) is used because ORVIX is the sole sending path.
func canonicalSPF() string {
	return "v=spf1 mx -all"
}

// canonicalDMARC returns the DMARC record ORVIX requires for a domain. ORVIX
// stores no per-domain DMARC policy preference anywhere (the domain model
// carries only a DMARCEnabled boolean — there is no policy or rua column), so
// this is a fixed, correct template parameterised only by the domain name.
func canonicalDMARC(domain string) string {
	return fmt.Sprintf("v=DMARC1; p=quarantine; rua=mailto:dmarc@%s", domain)
}

// canonicalMTASTS / canonicalTLSRPT are the required values for the two
// policy-reporting TXT records.
func canonicalMTASTS() string { return "v=STSv1; id=<policy-id>" }

func canonicalTLSRPT(domain string) string {
	return fmt.Sprintf("v=TLSRPTv1; rua=mailto:tlsrpt@%s", domain)
}
