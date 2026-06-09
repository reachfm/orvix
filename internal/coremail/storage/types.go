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
	Search   string
	Since    *time.Time
	Before   *time.Time
	Limit    int
	Offset   int
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

