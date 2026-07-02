package storage

import "time"

// FolderType represents the well-known IMAP folder types.
type FolderType string

const (
	FolderInbox   FolderType = "inbox"
	FolderSent    FolderType = "sent"
	FolderDrafts  FolderType = "drafts"
	FolderTrash   FolderType = "trash"
	FolderJunk    FolderType = "junk"
	FolderArchive FolderType = "archive"
	FolderCustom  FolderType = "custom"
)

// Flag represents standard IMAP message flags.
type Flag string

const (
	FlagSeen     Flag = "seen"
	FlagAnswered Flag = "answered"
	FlagFlagged  Flag = "flagged"
	FlagDeleted  Flag = "deleted"
	FlagDraft    Flag = "draft"
)

// Importance levels.
type Importance int

const (
	ImportanceLow    Importance = -1
	ImportanceNormal Importance = 0
	ImportanceHigh   Importance = 1
)

// RetentionType defines how retention is measured.
type RetentionType string

const (
	RetentionByAge   RetentionType = "age"
	RetentionByCount RetentionType = "count"
	RetentionBySize  RetentionType = "size"
)

// nowFn is overridable for testing.
var nowFn = time.Now

// MessageFilter defines search/filter criteria for listing messages.
//
// SearchScope controls which fields participate in the LIKE
// match when Search is non-empty:
//
//   - SearchSubject (default true) — matches against subject.
//   - SearchFrom (default true)    — matches against from_address.
//   - SearchTo (default true)      — matches against to_addresses.
//   - SearchCc (default false)     — matches against cc_addresses
//     when true. Off by default
//     because cc is rarely searched and the LIKE clause adds
//     measurable cost on big mailboxes.
//   - SearchBcc (default false)    — matches against bcc_addresses.
//   - SearchBody (default false)   — matches against the
//     RFC822 body loaded from disk. Off by default for the
//     same reason; callers that enable it must be willing
//     to pay the per-message read cost.
//
// When ALL bools are left at their zero value, the filter
// uses the legacy "subject / from / to" match — keeping
// existing callers unchanged.
type MessageFilter struct {
	MailboxID uint
	FolderID  *uint
	Flags     *struct {
		Seen     *bool
		Flagged  *bool
		Draft    *bool
		Deleted  *bool
		Junk     *bool
	}
	Search       string
	SearchSubject bool
	SearchFrom    bool
	SearchTo      bool
	SearchCc      bool
	SearchBcc     bool
	SearchBody    bool
	Since         *time.Time
	Before        *time.Time
	Limit         int
	Offset        int

	// Cursor pagination (scale-ready).
	//
	// When BeforeID is non-zero, the list query uses
	//   WHERE id < BeforeID
	// instead of OFFSET. This is the only safe way to paginate
	// a billion-row table: OFFSET requires the database to
	// scan and discard every preceding row, so page 1000 of
	// 100-row pages forces a 100,000-row scan. Cursor pagination
	// is constant-cost per page regardless of depth.
	//
	// The cursor is opaque to the caller; webmail encodes the
	// last-seen message id as a base64 string and passes it
	// back on the next request. Id is monotonic and immutable
	// per row, so the cursor remains stable across inserts and
	// deletes within the same mailbox.
	BeforeID uint
	// AfterID, when non-zero, returns messages with id > AfterID.
	// Used by the webmail "new messages" poll.
	AfterID uint
	// UseCursor is the explicit opt-in: if false, the legacy
	// OFFSET path runs. New callers should set UseCursor=true
	// and supply BeforeID or AfterID.
	UseCursor bool
}

// MatchScopeForSearch returns the Search* flags wired up
// for the supplied Search string. The legacy zero-config
// callers get the same subject/from/to match they have
// always had; webmail callers can enable cc/bcc/body by
// setting the fields directly.
func (f *MessageFilter) MatchScopeForSearch() (subject, from, to, cc, bcc, body bool) {
	if f.Search == "" {
		return false, false, false, false, false, false
	}
	// If the caller did not opt in to any specific field
	// (all flags are the zero value), fall back to the
	// historical subject/from/to behaviour. This is the
	// common case for legacy storage callers.
	anySet := f.SearchSubject || f.SearchFrom || f.SearchTo ||
		f.SearchCc || f.SearchBcc || f.SearchBody
	if !anySet {
		return true, true, true, false, false, false
	}
	return f.SearchSubject, f.SearchFrom, f.SearchTo, f.SearchCc, f.SearchBcc, f.SearchBody
}

// Pagination constants.
const (
	MaxPageSize = 1000
	DefPageSize = 100
)

// boolToInt converts a bool to an int (1/0) for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// intToBool converts an int (1/0) from SQLite storage to a bool.
func intToBool(i int) bool {
	return i == 1
}

// MessagesByID implements sort.Interface for sorting messages by ID descending.
type MessagesByID []Message

func (m MessagesByID) Len() int           { return len(m) }
func (m MessagesByID) Less(i, j int) bool { return m[i].ID > m[j].ID }
func (m MessagesByID) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }

