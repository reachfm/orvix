package imap

import (
	"fmt"
	"strconv"
	"strings"
)

// SequenceSet represents a parsed IMAP sequence set.
type SequenceSet struct {
	ranges  []seqRange
	hasStar bool
}

type seqRange struct {
	start, end int // 0 = unset (start only, or *)
}

// ParseSequenceSet parses an RFC 3501 sequence set string.
// Returns a SequenceSet or an error if the set is invalid.
func ParseSequenceSet(s string) (*SequenceSet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty sequence set")
	}

	ss := &SequenceSet{}

	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty element in sequence set")
		}

		if part == "*" {
			ss.hasStar = true
			continue
		}

		if strings.Contains(part, ":") {
			// Range: start:end
			sub := strings.SplitN(part, ":", 2)
			startStr := strings.TrimSpace(sub[0])
			endStr := strings.TrimSpace(sub[1])

			if startStr == "" || endStr == "" {
				return nil, fmt.Errorf("invalid range: %s", part)
			}

			var start, end int

			if startStr == "*" {
				start = 0
				ss.hasStar = true
			} else {
				n, err := strconv.Atoi(startStr)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("invalid sequence number: %s", startStr)
				}
				start = n
			}

			if endStr == "*" {
				end = 0
				ss.hasStar = true
			} else {
				n, err := strconv.Atoi(endStr)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("invalid sequence number: %s", endStr)
				}
				end = n
			}

			if start > 0 && end > 0 && start > end {
				return nil, fmt.Errorf("invalid range: %d > %d", start, end)
			}

			ss.ranges = append(ss.ranges, seqRange{start: start, end: end})
		} else {
			// Single number.
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid sequence number: %s", part)
			}
			ss.ranges = append(ss.ranges, seqRange{start: n, end: n})
		}
	}

	if len(ss.ranges) == 0 && !ss.hasStar {
		return nil, fmt.Errorf("empty sequence set")
	}

	return ss, nil
}

// Resolve expands the sequence set against a total message count.
// Returns a sorted, deduplicated list of 1-based message indices.
func (ss *SequenceSet) Resolve(total int) []int {
	if total == 0 {
		return nil
	}

	seen := make(map[int]bool)
	var result []int

	for _, r := range ss.ranges {
		start := r.start
		end := r.end

		// Resolve wildcards.
		if start == 0 {
			start = 1
		}
		if end == 0 {
			end = total
		}

		// Clamp to valid range.
		if start < 1 {
			start = 1
		}
		if end > total {
			end = total
		}

		for i := start; i <= end; i++ {
			if !seen[i] {
				seen[i] = true
				result = append(result, i)
			}
		}
	}

	if ss.hasStar && len(result) == 0 {
		// Only star was specified (e.g., "*")
		for i := 1; i <= total; i++ {
			result = append(result, i)
		}
	}

	return result
}
