package delivery

import "time"

// BounceType classifies the nature of a delivery failure.
type BounceType string

const (
	BounceUndetermined   BounceType = "undetermined"
	BounceUserUnknown    BounceType = "user_unknown"
	BounceMailboxFull    BounceType = "mailbox_full"
	BounceMessageTooBig  BounceType = "message_too_big"
	BounceRelayDenied    BounceType = "relay_denied"
	BounceSpamBlocked    BounceType = "spam_blocked"
	BounceTimeout        BounceType = "timeout"
	BounceUnavailable    BounceType = "unavailable"
	BounceConfigError    BounceType = "config_error"
	BounceSystemError    BounceType = "system_error"
)

// BounceEvent represents a delivery failure that may generate a bounce.
type BounceEvent struct {
	QueueEntryID uint      `json:"queue_entry_id"`
	FromAddress  string    `json:"from_address"`
	ToAddress    string    `json:"to_address"`
	BounceType   BounceType `json:"bounce_type"`
	StatusCode   int       `json:"status_code"`
	StatusMsg    string    `json:"status_msg"`
	TempFail     bool      `json:"temp_fail"`
	AttemptCount int       `json:"attempt_count"`
	Timestamp    time.Time `json:"timestamp"`
}

// ClassifyBounce determines the bounce type from an SMTP response.
func ClassifyBounce(code int, msg string) BounceType {
	if code == 0 {
		return BounceSystemError
	}
	if code >= 500 && code < 600 {
		switch {
		case containsAny(msg, "user unknown", "no such user", "mailbox not found", "does not exist"):
			return BounceUserUnknown
		case containsAny(msg, "mailbox full", "quota exceeded", "over quota"):
			return BounceMailboxFull
		case containsAny(msg, "message too large", "exceeds size"):
			return BounceMessageTooBig
		case containsAny(msg, "relay denied", "relay not permitted"):
			return BounceRelayDenied
		case containsAny(msg, "spam", "blocked", "rejected"):
			return BounceSpamBlocked
		default:
			return BounceUserUnknown
		}
	}
	if code >= 400 && code < 500 {
		if containsAny(msg, "timeout", "timed out") {
			return BounceTimeout
		}
		return BounceUnavailable
	}
	return BounceUndetermined
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(sub) > len(s) {
			continue
		}
		// Case-insensitive contains.
		lower := s
		if len(lower) >= len(sub) {
			for i := 0; i <= len(lower)-len(sub); i++ {
				match := true
				for j := 0; j < len(sub); j++ {
					ci := lower[i+j]
					cj := sub[j]
					if ci >= 'A' && ci <= 'Z' {
						ci += 32
					}
					if cj >= 'A' && cj <= 'Z' {
						cj += 32
					}
					if ci != cj {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}
