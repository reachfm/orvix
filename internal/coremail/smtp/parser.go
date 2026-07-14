package smtp

import (
	"fmt"
	"strings"
)

// ParsedCommand represents a parsed SMTP command line.
type ParsedCommand struct {
	Verb string
	Args string
	Raw  string
}

const maxLineLength = 1000

// ParseLine parses a single SMTP command line.
// Lines are terminated by <CRLF> or <LF>.
func ParseLine(line string, maxLen int) (*ParsedCommand, error) {
	// Strip trailing CR/LF.
	line = strings.TrimRight(line, "\r\n")

	if len(line) > maxLen {
		return nil, fmt.Errorf("line too long: %d > %d", len(line), maxLen)
	}

	if line == "" {
		return nil, fmt.Errorf("empty line")
	}

	parts := strings.SplitN(line, " ", 2)
	verb := strings.ToUpper(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	return &ParsedCommand{
		Verb: verb,
		Args: args,
		Raw:  line,
	}, nil
}

// ParseMailFrom parses the MAIL FROM: <address> [BODY=8BITMIME] [SIZE=1234]
func ParseMailFrom(args string) (string, int64, error) {
	rest := args

	// Skip "FROM:" prefix (MAIL FROM: <addr>)
	if len(rest) >= 5 && strings.ToUpper(rest[:5]) == "FROM:" {
		rest = rest[5:]
	} else if len(rest) >= 6 && strings.ToUpper(rest[:6]) == "FROM " {
		rest = rest[6:]
	}
	rest = strings.TrimSpace(rest)

	// Extract address between <>
	var address string
	if strings.HasPrefix(rest, "<") {
		closeIdx := strings.Index(rest, ">")
		if closeIdx < 0 {
			return "", 0, fmt.Errorf("malformed MAIL FROM: missing >")
		}
		inner := rest[1:closeIdx]
		if inner == "" {
			address = "<>" // preserve null sender marker
		} else {
			address = inner
		}
		rest = strings.TrimSpace(rest[closeIdx+1:])
	} else {
		// Try space-separated
		parts := strings.SplitN(rest, " ", 2)
		address = parts[0]
		if len(parts) > 1 {
			rest = parts[1]
		} else {
			rest = ""
		}
	}

	// Keep empty address; caller (handleMAIL) decides if null sender is allowed.

	// Parse SIZE parameter.
	var size int64
	rest = strings.ToUpper(rest)
	if idx := strings.Index(rest, "SIZE="); idx >= 0 {
		sizeStr := rest[idx+5:]
		if spaceIdx := strings.Index(sizeStr, " "); spaceIdx >= 0 {
			sizeStr = sizeStr[:spaceIdx]
		}
		// Parse size
		s := int64(0)
		for _, c := range sizeStr {
			if c < '0' || c > '9' {
				break
			}
			s = s*10 + int64(c-'0')
		}
		size = s
	}

	return address, size, nil
}

// ParseRcptTo parses RCPT TO: <address>
func ParseRcptTo(args string) (string, error) {
	rest := args
	rest = strings.TrimSpace(rest)
	// Handle "TO:<addr>" or "TO:<addr> ..." or "TO <addr>"
	up := strings.ToUpper(rest)
	if strings.HasPrefix(up, "TO:") {
		rest = rest[3:]
	} else if strings.HasPrefix(up, "TO ") {
		rest = rest[3:]
	}
	rest = strings.TrimSpace(rest)

	if strings.HasPrefix(rest, "<") {
		closeIdx := strings.Index(rest, ">")
		if closeIdx < 0 {
			return "", fmt.Errorf("malformed RCPT TO: missing >")
		}
		return rest[1:closeIdx], nil
	}

	parts := strings.SplitN(rest, " ", 2)
	return parts[0], nil
}

// ExtractDomain extracts the domain part from an email address.
func ExtractDomain(email string) string {
	// Handle <>
	if email == "" || email == "<>" {
		return ""
	}
	atIdx := strings.LastIndex(email, "@")
	if atIdx < 0 {
		return ""
	}
	return email[atIdx+1:]
}
