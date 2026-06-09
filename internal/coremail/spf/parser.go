package spf

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ParseRecord parses an SPF record string into a structured SPFRecord.
func ParseRecord(record string) (*SPFRecord, error) {
	if idx := strings.IndexByte(record, '#'); idx >= 0 {
		record = record[:idx]
	}
	record = strings.TrimSpace(record)

	if record == "" {
		return nil, fmt.Errorf("empty SPF record")
	}

	parts := strings.Fields(record)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty SPF record")
	}

	if parts[0] != "v=spf1" {
		return nil, fmt.Errorf("not an SPF record: missing v=spf1")
	}

	res := &SPFRecord{
		Version:    parts[0],
		Mechanisms: make([]Mechanism, 0),
		Modifiers:  make(map[string]string),
		Raw:        record,
	}

	for i := 1; i < len(parts); i++ {
		token := parts[i]
		if token == "" {
			continue
		}

		if idx := strings.IndexByte(token, '='); idx >= 0 {
			name := token[:idx]
			value := token[idx+1:]
			if name == "redirect" || name == "exp" {
				res.Modifiers[name] = value
			}
			continue
		}

		mec, err := parseMechanism(token)
		if err != nil {
			return nil, fmt.Errorf("parse mechanism %q: %w", token, err)
		}
		res.Mechanisms = append(res.Mechanisms, *mec)
	}

	return res, nil
}

func parseMechanism(token string) (*Mechanism, error) {
	m := &Mechanism{
		CIDRLen:  -1,
		CIDRLen6: -1,
	}

	qual, consumed := parseQualifier(token[0])
	m.Qualifier = qual
	token = token[consumed:]

	if token == "all" {
		m.Directive = "all"
		return m, nil
	}

	if strings.HasPrefix(token, "ip4:") {
		m.Directive = "ip4"
		spec := token[4:]
		ip, cidr, err := parseCIDRSpec(spec, 32)
		if err != nil {
			return nil, fmt.Errorf("ip4: %w", err)
		}
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("ip4: invalid IP address %q", ip)
		}
		m.DomainSpec = ip
		m.CIDRLen = cidr
		return m, nil
	}

	if strings.HasPrefix(token, "ip6:") {
		m.Directive = "ip6"
		spec := token[4:]
		ip, cidr, err := parseCIDRSpec(spec, 128)
		if err != nil {
			return nil, fmt.Errorf("ip6: %w", err)
		}
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("ip6: invalid IP address %q", ip)
		}
		m.DomainSpec = ip
		m.CIDRLen6 = cidr
		return m, nil
	}

	if strings.HasPrefix(token, "include:") {
		m.Directive = "include"
		m.DomainSpec = token[8:]
		if m.DomainSpec == "" {
			return nil, fmt.Errorf("include: missing domain")
		}
		return m, nil
	}

	if strings.HasPrefix(token, "a") {
		m.Directive = "a"
		parseDomainCIDR(token[1:], m)
		return m, nil
	}

	if strings.HasPrefix(token, "mx") {
		m.Directive = "mx"
		parseDomainCIDR(token[2:], m)
		return m, nil
	}

	if token == "ptr" || strings.HasPrefix(token, "ptr:") {
		m.Directive = "ptr"
		if strings.HasPrefix(token, "ptr:") {
			m.DomainSpec = token[4:]
		}
		return m, nil
	}

	if token == "exist" || strings.HasPrefix(token, "exist:") {
		m.Directive = "exist"
		if strings.HasPrefix(token, "exist:") {
			m.DomainSpec = token[6:]
		}
		return m, nil
	}

	return nil, fmt.Errorf("unknown mechanism: %s", token)
}

// parseDomainCIDR parses the portion after "a" or "mx" to extract
// optional :domain and optional /cidr.
func parseDomainCIDR(rest string, m *Mechanism) {
	if rest == "" {
		return
	}
	if rest[0] == ':' {
		rest = rest[1:]
	} else if rest[0] == '/' {
		cidr, err := strconv.Atoi(rest[1:])
		if err == nil && cidr >= 0 && cidr <= 32 {
			m.CIDRLen = cidr
		}
		return
	} else {
		return
	}

	// Now rest may be "domain" or "domain/cidr".
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		m.DomainSpec = rest[:idx]
		cidr, err := strconv.Atoi(rest[idx+1:])
		if err == nil && cidr >= 0 && cidr <= 32 {
			m.CIDRLen = cidr
		}
	} else {
		m.DomainSpec = rest
	}
}

func parseCIDRSpec(spec string, defaultCIDR int) (string, int, error) {
	if idx := strings.IndexByte(spec, '/'); idx >= 0 {
		ip := spec[:idx]
		cidr, err := strconv.Atoi(spec[idx+1:])
		if err != nil {
			return "", 0, fmt.Errorf("invalid cidr: %s", spec[idx+1:])
		}
		return ip, cidr, nil
	}
	return spec, defaultCIDR, nil
}
