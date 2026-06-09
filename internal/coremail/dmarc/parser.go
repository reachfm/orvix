package dmarc

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRecord parses a DMARC DNS TXT record string into a DMARCRecord.
func ParseRecord(record string) (*DMARCRecord, error) {
	record = strings.TrimSpace(record)
	if record == "" {
		return nil, fmt.Errorf("empty DMARC record")
	}

	// The DMARC record may be split across multiple DNS TXT strings.
	// If there are multiple strings, they should be concatenated.
	// Each tag=value pair is separated by ";".

	res := &DMARCRecord{
		Policy:       PolicyNone,
		SubdomainPol: PolicyNone,
		Pct:          100,
		ADKIM:        AlignmentRelaxed,
		ASPF:         AlignmentRelaxed,
		Raw:          record,
	}

	// Split by semicolons.
	pairs := strings.Split(record, ";")
	seenP := false
	seenV := false

	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return nil, fmt.Errorf("malformed tag (no '='): %q", pair)
		}
		tag := strings.TrimSpace(pair[:idx])
		value := strings.TrimSpace(pair[idx+1:])

		switch tag {
		case "v":
			if seenV {
				return nil, fmt.Errorf("duplicate v tag")
			}
			seenV = true
			if value != "DMARC1" {
				return nil, fmt.Errorf("unsupported version: %q", value)
			}
			res.Version = value

		case "p":
			if seenP {
				return nil, fmt.Errorf("duplicate p tag")
			}
			seenP = true
			pol, err := parsePolicy(value)
			if err != nil {
				return nil, fmt.Errorf("p: %w", err)
			}
			res.Policy = pol

		case "sp":
			pol, err := parsePolicy(value)
			if err != nil {
				return nil, fmt.Errorf("sp: %w", err)
			}
			res.SubdomainPol = pol

		case "pct":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 100 {
				return nil, fmt.Errorf("pct: invalid value %q", value)
			}
			res.Pct = n

		case "rua":
			res.RUA = value

		case "ruf":
			res.RUF = value

		case "adkim":
			switch value {
			case "r":
				res.ADKIM = AlignmentRelaxed
			case "s":
				res.ADKIM = AlignmentStrict
			default:
				return nil, fmt.Errorf("adkim: invalid value %q", value)
			}

		case "aspf":
			switch value {
			case "r":
				res.ASPF = AlignmentRelaxed
			case "s":
				res.ASPF = AlignmentStrict
			default:
				return nil, fmt.Errorf("aspf: invalid value %q", value)
			}

		default:
			// Unknown tags are ignored per RFC 7489 §6.3.
		}
	}

	if !seenV {
		return nil, fmt.Errorf("missing v tag")
	}
	if !seenP {
		return nil, fmt.Errorf("missing p tag")
	}
	// If sp is not set, inherit from p.
	if res.SubdomainPol == PolicyNone && !hasExplicitSP(record) {
		res.SubdomainPol = res.Policy
	}

	return res, nil
}

func parsePolicy(value string) (Policy, error) {
	switch strings.ToLower(value) {
	case "none":
		return PolicyNone, nil
	case "quarantine":
		return PolicyQuarantine, nil
	case "reject":
		return PolicyReject, nil
	default:
		return PolicyNone, fmt.Errorf("invalid policy %q", value)
	}
}

func hasExplicitSP(record string) bool {
	for _, pair := range strings.Split(record, ";") {
		pair = strings.TrimSpace(pair)
		if strings.HasPrefix(pair, "sp=") || strings.HasPrefix(pair, "sp =") {
			return true
		}
	}
	return false
}
