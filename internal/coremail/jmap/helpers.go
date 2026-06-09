package jmap

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

func (s *Server) applyQueryFilter(msgs []storage.Message, filter *EmailQueryFilter) []storage.Message {
	if filter == nil {
		return msgs
	}

	var result []storage.Message
	for _, m := range msgs {
		if !matchFilter(&m, filter) {
			continue
		}
		result = append(result, m)
	}
	return result
}

func matchFilter(m *storage.Message, f *EmailQueryFilter) bool {
	if len(f.InMailbox) > 0 {
		found := false
		for _, id := range f.InMailbox {
			_ = id
			found = true
			break
		}
		if !found {
			return false
		}
	}

	if f.From != "" {
		if !containsCi(m.FromAddress, f.From) {
			return false
		}
	}

	if f.To != "" {
		if !containsCi(m.ToAddresses, f.To) {
			return false
		}
	}

	if f.Subject != "" {
		if !containsCi(m.Subject, f.Subject) {
			return false
		}
	}

	if f.Text != "" {
		if !containsCi(m.Subject, f.Text) && !containsCi(m.FromAddress, f.Text) {
			return false
		}
	}

	if f.Before != "" {
		t, err := parseTime(f.Before)
		if err == nil && !m.ReceivedDate.Before(t) {
			return false
		}
	}

	if f.After != "" {
		t, err := parseTime(f.After)
		if err == nil && !m.ReceivedDate.After(t) {
			return false
		}
	}

	if f.HasKeyword != nil && *f.HasKeyword != "" {
		k := *f.HasKeyword
		switch k {
		case "$seen":
			if !m.Seen { return false }
		case "$answered":
			if !m.Answered { return false }
		case "$flagged":
			if !m.Flagged { return false }
		case "$draft":
			if !m.Draft { return false }
		case "$deleted":
			if !m.Deleted { return false }
		}
	}

	if f.NotKeyword != nil && *f.NotKeyword != "" {
		k := *f.NotKeyword
		switch k {
		case "$seen":
			if m.Seen { return false }
		case "$answered":
			if m.Answered { return false }
		case "$flagged":
			if m.Flagged { return false }
		case "$draft":
			if m.Draft { return false }
		case "$deleted":
			if m.Deleted { return false }
		}
	}

	return true
}

func (s *Server) applyQuerySort(msgs []storage.Message, sort []*EmailQuerySort) []storage.Message {
	if len(sort) == 0 {
		sort = []*EmailQuerySort{{Property: "receivedAt", IsAscending: boolPtr(false)}}
	}

	result := make([]storage.Message, len(msgs))
	copy(result, msgs)

	for _, s := range sort {
		asc := s.IsAscending != nil && *s.IsAscending
		switch s.Property {
		case "receivedAt":
			sortByReceivedAt(result, asc)
		case "size":
			sortBySize(result, asc)
		case "subject":
			sortBySubject(result, asc)
		}
	}

	return result
}

func sortByReceivedAt(msgs []storage.Message, asc bool) {
	sort.Slice(msgs, func(i, j int) bool {
		if asc {
			return msgs[i].ReceivedDate.Before(msgs[j].ReceivedDate)
		}
		return msgs[i].ReceivedDate.After(msgs[j].ReceivedDate)
	})
}

func sortBySize(msgs []storage.Message, asc bool) {
	sort.Slice(msgs, func(i, j int) bool {
		if asc {
			return msgs[i].SizeBytes < msgs[j].SizeBytes
		}
		return msgs[i].SizeBytes > msgs[j].SizeBytes
	})
}

func sortBySubject(msgs []storage.Message, asc bool) {
	sort.Slice(msgs, func(i, j int) bool {
		if asc {
			return msgs[i].Subject < msgs[j].Subject
		}
		return msgs[i].Subject > msgs[j].Subject
	})
}

func parseTime(s string) (time.Time, error) {
	formats := []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}

func containsCi(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}

func maxMsgID(msgs []storage.Message) uint {
	var max uint
	for _, m := range msgs {
		if m.ID > max {
			max = m.ID
		}
	}
	return max
}

func boolPtr(b bool) *bool { return &b }
