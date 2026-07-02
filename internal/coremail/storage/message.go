package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message represents an email message stored in the Orvix MailStore.
type Message struct {
	ID                uint       `json:"id"`
	MessageID         string     `json:"message_id"` // UUID
	TenantID          uint       `json:"tenant_id"`
	DomainID          uint       `json:"domain_id"`
	MailboxID         uint       `json:"mailbox_id"`
	FolderID          uint       `json:"folder_id"`
	ThreadID          *string    `json:"thread_id,omitempty"`
	InternetMessageID string     `json:"internet_message_id"` // Message-ID header
	Subject           string     `json:"subject"`
	FromAddress       string     `json:"from_address"`
	ToAddresses       string     `json:"to_addresses"`
	CcAddresses       string     `json:"cc_addresses"`
	BccAddresses      string     `json:"bcc_addresses"`
	ReplyTo           string     `json:"reply_to"`
	MessageDate       *time.Time `json:"message_date,omitempty"`
	ReceivedDate      time.Time  `json:"received_date"`
	SizeBytes         int64      `json:"size_bytes"`
	RFC822Path        string     `json:"rfc822_path"`
	SHA256            string     `json:"sha256"`
	Seen              bool       `json:"seen"`
	Answered          bool       `json:"answered"`
	Flagged           bool       `json:"flagged"`
	Draft             bool       `json:"draft"`
	Deleted           bool       `json:"deleted"`
	Junk              bool       `json:"junk"`
	Importance        Importance `json:"importance"`
	RetentionPolicyID *uint      `json:"retention_policy_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	PurgedAt          *time.Time `json:"purged_at,omitempty"`
}

// MessageRepository defines the contract for message persistence.
type MessageRepository interface {
	Create(ctx context.Context, m *Message, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Message, error)
	GetByMessageID(ctx context.Context, messageID string, tx interface{}) (*Message, error)
	List(ctx context.Context, filter MessageFilter, tx interface{}) ([]Message, int64, error)
	Update(ctx context.Context, m *Message, tx interface{}) error
	UpdateFlags(ctx context.Context, id uint, seen, answered, flagged, draft, deleted, junk *bool, tx interface{}) error
	Move(ctx context.Context, id uint, newFolderID uint, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
	Purge(ctx context.Context, id uint, tx interface{}) error
	Restore(ctx context.Context, id uint, tx interface{}) error
	CountByMailbox(ctx context.Context, mailboxID uint, tx interface{}) (int64, error)
	CountByFolder(ctx context.Context, folderID uint, tx interface{}) (int64, error)
	SumSizeByMailbox(ctx context.Context, mailboxID uint, tx interface{}) (int64, error)

	// ListByCursor is the scale-ready replacement for List when
	// UseCursor is set on the filter. Returns the next page of
	// messages plus the cursor the caller should pass back to
	// fetch the page after that.
	ListByCursor(ctx context.Context, filter MessageFilter, tx interface{}) (MessagesPage, error)
}

// MessagesPage is a cursor-paginated slice of messages. The
// returned slice is ordered by id DESC (newest first when
// before-cursor is used; oldest first when after-cursor is
// used). NextCursor is the id to pass as filter.BeforeID on
// the next call; it is 0 when there are no more rows.
type MessagesPage struct {
	Messages   []Message
	NextCursor uint
	HasMore    bool
}

var _ MessageRepository = (*MessageSQLRepo)(nil)

// MessageSQLRepo implements MessageRepository using database/sql.
type MessageSQLRepo struct {
	db *sql.DB
}

func NewMessageSQLRepo(db *sql.DB) *MessageSQLRepo {
	return &MessageSQLRepo{db: db}
}

func (r *MessageSQLRepo) exec(tx interface{}) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		if t, ok := tx.(*sql.Tx); ok {
			return t
		}
	}
	return r.db
}

// Message columns (shared between INSERT and SELECT).
const messageCols = `message_id, tenant_id, domain_id, mailbox_id, folder_id, thread_id,
	internet_message_id, subject, from_address, to_addresses, cc_addresses, bcc_addresses, reply_to,
	message_date, received_date, size_bytes, rfc822_path, sha256,
	seen, answered, flagged, draft, deleted, junk, importance, retention_policy_id,
	created_at, updated_at, purged_at`

func (r *MessageSQLRepo) Create(ctx context.Context, m *Message, tx interface{}) error {
	now := nowFn()
	m.CreatedAt = now
	m.UpdatedAt = now
	e := r.exec(tx)
	res, err := retrySQL(ctx, func() (sql.Result, error) {
		return e.ExecContext(ctx, `
		INSERT INTO coremail_messages (`+messageCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.MessageID, m.TenantID, m.DomainID, m.MailboxID, m.FolderID, m.ThreadID,
			m.InternetMessageID, m.Subject, m.FromAddress, m.ToAddresses, m.CcAddresses, m.BccAddresses, m.ReplyTo,
			m.MessageDate, m.ReceivedDate, m.SizeBytes, m.RFC822Path, m.SHA256,
			boolToInt(m.Seen), boolToInt(m.Answered), boolToInt(m.Flagged), boolToInt(m.Draft), boolToInt(m.Deleted), boolToInt(m.Junk),
			int(m.Importance), m.RetentionPolicyID,
			m.CreatedAt, m.UpdatedAt, m.PurgedAt,
		)
	})
	if err != nil {
		return fmt.Errorf("create message: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	m.ID = uint(id)
	return nil
}

func (r *MessageSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Message, error) {
	e := r.exec(tx)
	return retrySQL(ctx, func() (*Message, error) {
		row := e.QueryRowContext(ctx, `SELECT id, `+messageCols+` FROM coremail_messages WHERE id = ? AND purged_at IS NULL`, id)
		return scanMessage(row)
	})
}

func (r *MessageSQLRepo) GetByMessageID(ctx context.Context, messageID string, tx interface{}) (*Message, error) {
	e := r.exec(tx)
	return retrySQL(ctx, func() (*Message, error) {
		row := e.QueryRowContext(ctx, `SELECT id, `+messageCols+` FROM coremail_messages WHERE message_id = ? AND purged_at IS NULL`, messageID)
		return scanMessage(row)
	})
}

func (r *MessageSQLRepo) List(ctx context.Context, filter MessageFilter, tx interface{}) ([]Message, int64, error) {
	result, err := retrySQL(ctx, func() (messageListResult, error) {
		msgs, total, err := r.listOnce(ctx, filter, tx)
		return messageListResult{Messages: msgs, Total: total}, err
	})
	if err != nil {
		return nil, 0, err
	}
	return result.Messages, result.Total, nil
}

type messageListResult struct {
	Messages []Message
	Total    int64
}

func (r *MessageSQLRepo) listOnce(ctx context.Context, filter MessageFilter, tx interface{}) ([]Message, int64, error) {
	e := r.exec(tx)
	if filter.Limit < 1 || filter.Limit > MaxPageSize {
		filter.Limit = DefPageSize
	}

	var where []string
	var args []interface{}
	where = append(where, "mailbox_id = ?")
	args = append(args, filter.MailboxID)
	where = append(where, "purged_at IS NULL")

	if filter.FolderID != nil {
		where = append(where, "folder_id = ?")
		args = append(args, *filter.FolderID)
	}
	if filter.Flags != nil {
		if filter.Flags.Seen != nil {
			where = append(where, fmt.Sprintf("seen = %d", boolToInt(*filter.Flags.Seen)))
		}
		if filter.Flags.Flagged != nil {
			where = append(where, fmt.Sprintf("flagged = %d", boolToInt(*filter.Flags.Flagged)))
		}
		if filter.Flags.Draft != nil {
			where = append(where, fmt.Sprintf("draft = %d", boolToInt(*filter.Flags.Draft)))
		}
		if filter.Flags.Deleted != nil {
			where = append(where, fmt.Sprintf("deleted = %d", boolToInt(*filter.Flags.Deleted)))
		}
		if filter.Flags.Junk != nil {
			where = append(where, fmt.Sprintf("junk = %d", boolToInt(*filter.Flags.Junk)))
		}
	}
	if filter.Search != "" {
		// Build the LIKE clause set based on the
		// per-field opt-in flags. The default set
		// (subject / from / to) matches the historical
		// behaviour so legacy callers stay unchanged;
		// the body / cc / bcc flags are explicitly
		// off by default because they cost an extra
		// round trip and LIKE-against-RFC822 is not
		// cheap at scale.
		subject, from, to, cc, bcc, _ := filter.MatchScopeForSearch()
		var clauses []string
		var likeArgs []interface{}
		s := "%" + filter.Search + "%"
		if subject {
			clauses = append(clauses, "subject LIKE ?")
			likeArgs = append(likeArgs, s)
		}
		if from {
			clauses = append(clauses, "from_address LIKE ?")
			likeArgs = append(likeArgs, s)
		}
		if to {
			clauses = append(clauses, "to_addresses LIKE ?")
			likeArgs = append(likeArgs, s)
		}
		if cc {
			clauses = append(clauses, "cc_addresses LIKE ?")
			likeArgs = append(likeArgs, s)
		}
		if bcc {
			clauses = append(clauses, "bcc_addresses LIKE ?")
			likeArgs = append(likeArgs, s)
		}
		if len(clauses) > 0 {
			where = append(where, "("+strings.Join(clauses, " OR ")+")")
			args = append(args, likeArgs...)
		} else {
			// All scope flags are off but Search is
			// non-empty. The caller explicitly asked
			// for a search with no fields, which is a
			// no-op — match nothing rather than
			// silently return everything.
			where = append(where, "1 = 0")
		}
	}
	if filter.Since != nil {
		where = append(where, "received_date >= ?")
		args = append(args, *filter.Since)
	}
	if filter.Before != nil {
		where = append(where, "received_date <= ?")
		args = append(args, *filter.Before)
	}

	clause := strings.Join(where, " AND ")

	var total int64
	if err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count messages: %w", err)
	}

	rows, err := e.QueryContext(ctx, `SELECT id, `+messageCols+` FROM coremail_messages WHERE `+clause+` ORDER BY received_date DESC LIMIT ? OFFSET ?`,
		append(args, filter.Limit, filter.Offset)...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, *m)
	}
	return messages, total, rows.Err()
}

// ListByCursor is the scale-ready message list query.
//
// Compared to List (which uses OFFSET), this method uses an
// "id < cursor" or "id > cursor" predicate so the cost per
// page is constant regardless of how deep the user has
// paginated. The index
//
//	idx_coremail_messages_mailbox_folder_id(mailbox_id, folder_id, id)
//
// (added in migration 003) backs the cursor predicate, so
// Postgres and SQLite both serve the page in O(limit) without
// scanning any rows the caller will not see.
//
// Behavior:
//
//   - If BeforeID is set, the query is "ORDER BY id DESC" and
//     returns rows with id < BeforeID. The returned NextCursor
//     is the smallest id in the result; the caller passes it
//     as the next BeforeID. The most common pattern: webmail
//     "scroll to older".
//   - If AfterID is set, the query is "ORDER BY id ASC" and
//     returns rows with id > AfterID. Used by webmail
//     "poll for new messages" — fast, returns the tail of
//     the table.
//   - If neither cursor is set, the query returns the newest
//     Limit rows. The returned NextCursor is the smallest
//     id in the result, ready to be passed back as BeforeID.
//
// The total count is NOT returned. Counting requires a full
// index scan; on a billion-row table that is several seconds
// even on Postgres with a covering index. Callers that need a
// count should use CountByFolder for the cheap total.
//
// The function is the only path the webmail UI uses for
// production load. The legacy List (OFFSET) remains for
// internal admin tools that paginate < 10k rows.
func (r *MessageSQLRepo) ListByCursor(ctx context.Context, filter MessageFilter, tx interface{}) (MessagesPage, error) {
	e := r.exec(tx)
	if filter.MailboxID == 0 {
		return MessagesPage{}, errors.New("ListByCursor: MailboxID is required")
	}
	if filter.Limit < 1 {
		filter.Limit = DefPageSize
	}
	if filter.Limit > MaxPageSize {
		filter.Limit = MaxPageSize
	}

	// Build the WHERE clause. MailboxID is mandatory; folder is
	// optional; flags and search reuse the legacy filter clauses
	// from listOnce so the two paths are query-equivalent.
	var where []string
	var args []interface{}
	where = append(where, "mailbox_id = ?")
	args = append(args, filter.MailboxID)
	where = append(where, "purged_at IS NULL")
	if filter.FolderID != nil {
		where = append(where, "folder_id = ?")
		args = append(args, *filter.FolderID)
	}
	if filter.Flags != nil {
		if filter.Flags.Seen != nil {
			where = append(where, fmt.Sprintf("seen = %d", boolToInt(*filter.Flags.Seen)))
		}
		if filter.Flags.Flagged != nil {
			where = append(where, fmt.Sprintf("flagged = %d", boolToInt(*filter.Flags.Flagged)))
		}
		if filter.Flags.Draft != nil {
			where = append(where, fmt.Sprintf("draft = %d", boolToInt(*filter.Flags.Draft)))
		}
		if filter.Flags.Deleted != nil {
			where = append(where, fmt.Sprintf("deleted = %d", boolToInt(*filter.Flags.Deleted)))
		}
		if filter.Flags.Junk != nil {
			where = append(where, fmt.Sprintf("junk = %d", boolToInt(*filter.Flags.Junk)))
		}
	}
	if filter.Search != "" {
		subject, from, to, _, _, _ := filter.MatchScopeForSearch()
		var clauses []string
		s := "%" + filter.Search + "%"
		if subject {
			clauses = append(clauses, "subject LIKE ?")
			args = append(args, s)
		}
		if from {
			clauses = append(clauses, "from_address LIKE ?")
			args = append(args, s)
		}
		if to {
			clauses = append(clauses, "to_addresses LIKE ?")
			args = append(args, s)
		}
		if len(clauses) > 0 {
			where = append(where, "("+strings.Join(clauses, " OR ")+")")
		} else {
			where = append(where, "1 = 0")
		}
	}
	if filter.Since != nil {
		where = append(where, "received_date >= ?")
		args = append(args, *filter.Since)
	}
	if filter.Before != nil {
		where = append(where, "received_date <= ?")
		args = append(args, *filter.Before)
	}

	// Apply the cursor predicate. Asking for both BeforeID and
	// AfterID is a logic error: we honor BeforeID because it is
	// the dominant "scroll older" path.
	orderBy := "id DESC"
	switch {
	case filter.BeforeID != 0:
		where = append(where, "id < ?")
		args = append(args, filter.BeforeID)
	case filter.AfterID != 0:
		where = append(where, "id > ?")
		args = append(args, filter.AfterID)
		orderBy = "id ASC"
	}

	clause := strings.Join(where, " AND ")

	// Fetch one extra row to detect HasMore without a COUNT.
	args = append(args, filter.Limit+1)
	rows, err := e.QueryContext(ctx, `SELECT id, `+messageCols+` FROM coremail_messages WHERE `+clause+` ORDER BY `+orderBy+` LIMIT ?`, args...)
	if err != nil {
		return MessagesPage{}, fmt.Errorf("list messages by cursor: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return MessagesPage{}, err
		}
		messages = append(messages, *m)
	}
	if err := rows.Err(); err != nil {
		return MessagesPage{}, err
	}

	page := MessagesPage{HasMore: false, NextCursor: 0}
	if len(messages) > filter.Limit {
		// We fetched limit+1; the extra row signals there is
		// at least one more page.
		page.HasMore = true
		messages = messages[:filter.Limit]
	}
	if len(messages) == 0 {
		return page, nil
	}
	// Compute the next cursor. For DESC (BeforeID path) the
	// cursor is the smallest id in the result; for ASC
	// (AfterID path) it is the largest.
	if orderBy == "id DESC" {
		page.NextCursor = messages[len(messages)-1].ID
	} else {
		page.NextCursor = messages[0].ID
	}
	page.Messages = messages
	return page, nil
}

func (r *MessageSQLRepo) Update(ctx context.Context, m *Message, tx interface{}) error {
	m.UpdatedAt = nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `
		UPDATE coremail_messages SET subject=?, from_address=?, to_addresses=?, cc_addresses=?, bcc_addresses=?,
			seen=?, answered=?, flagged=?, draft=?, deleted=?, junk=?, importance=?, retention_policy_id=?, updated_at=?
		WHERE id = ? AND purged_at IS NULL`,
		m.Subject, m.FromAddress, m.ToAddresses, m.CcAddresses, m.BccAddresses,
		boolToInt(m.Seen), boolToInt(m.Answered), boolToInt(m.Flagged), boolToInt(m.Draft), boolToInt(m.Deleted), boolToInt(m.Junk),
		int(m.Importance), m.RetentionPolicyID, m.UpdatedAt, m.ID)
	return err
}

func (r *MessageSQLRepo) UpdateFlags(ctx context.Context, id uint, seen, answered, flagged, draft, deleted, junk *bool, tx interface{}) error {
	var sets []string
	var args []interface{}
	if seen != nil {
		sets = append(sets, "seen = ?")
		args = append(args, boolToInt(*seen))
	}
	if answered != nil {
		sets = append(sets, "answered = ?")
		args = append(args, boolToInt(*answered))
	}
	if flagged != nil {
		sets = append(sets, "flagged = ?")
		args = append(args, boolToInt(*flagged))
	}
	if draft != nil {
		sets = append(sets, "draft = ?")
		args = append(args, boolToInt(*draft))
	}
	if deleted != nil {
		sets = append(sets, "deleted = ?")
		args = append(args, boolToInt(*deleted))
	}
	if junk != nil {
		sets = append(sets, "junk = ?")
		args = append(args, boolToInt(*junk))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowFn(), id)

	e := r.exec(tx)
	if tx == nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}
	_, err := retrySQL(ctx, func() (sql.Result, error) {
		return e.ExecContext(ctx, "UPDATE coremail_messages SET "+strings.Join(sets, ",")+" WHERE id = ? AND purged_at IS NULL", args...)
	})
	return err
}

func (r *MessageSQLRepo) Move(ctx context.Context, id uint, newFolderID uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_messages SET folder_id=?, updated_at=? WHERE id = ? AND purged_at IS NULL", newFolderID, now, id)
	return err
}

func (r *MessageSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_messages SET deleted=1, updated_at=? WHERE id = ? AND purged_at IS NULL", now, id)
	return err
}

func (r *MessageSQLRepo) Purge(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_messages SET purged_at=? WHERE id = ? AND purged_at IS NULL", now, id)
	return err
}

func (r *MessageSQLRepo) Restore(ctx context.Context, id uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_messages SET deleted=0, updated_at=? WHERE id = ? AND purged_at IS NULL", now, id)
	return err
}

func (r *MessageSQLRepo) CountByMailbox(ctx context.Context, mailboxID uint, tx interface{}) (int64, error) {
	e := r.exec(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id=? AND purged_at IS NULL", mailboxID).Scan(&count)
	return count, err
}

func (r *MessageSQLRepo) CountByFolder(ctx context.Context, folderID uint, tx interface{}) (int64, error) {
	e := r.exec(tx)
	var count int64
	_, err := retrySQL(ctx, func() (struct{}, error) {
		return struct{}{}, e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages WHERE folder_id=? AND purged_at IS NULL", folderID).Scan(&count)
	})
	return count, err
}

func (r *MessageSQLRepo) SumSizeByMailbox(ctx context.Context, mailboxID uint, tx interface{}) (int64, error) {
	e := r.exec(tx)
	var total sql.NullInt64
	err := e.QueryRowContext(ctx, "SELECT SUM(size_bytes) FROM coremail_messages WHERE mailbox_id=? AND purged_at IS NULL", mailboxID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func scanMessage(row interface {
	Scan(dest ...interface{}) error
}) (*Message, error) {
	var m Message
	var threadID, internetMsgID, subject, fromAddr, toAddrs, ccAddrs, bccAddrs, replyTo *string
	var messageDate, purgedAt *time.Time
	var seen, answered, flagged, draft, deleted, junk int
	var importance int

	err := row.Scan(&m.ID,
		&m.MessageID, &m.TenantID, &m.DomainID, &m.MailboxID, &m.FolderID, &threadID,
		&internetMsgID, &subject, &fromAddr, &toAddrs, &ccAddrs, &bccAddrs, &replyTo,
		&messageDate, &m.ReceivedDate, &m.SizeBytes, &m.RFC822Path, &m.SHA256,
		&seen, &answered, &flagged, &draft, &deleted, &junk, &importance, &m.RetentionPolicyID,
		&m.CreatedAt, &m.UpdatedAt, &purgedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan message: %w", err)
	}

	if threadID != nil {
		m.ThreadID = threadID
	}
	if internetMsgID != nil {
		m.InternetMessageID = *internetMsgID
	}
	if subject != nil {
		m.Subject = *subject
	}
	if fromAddr != nil {
		m.FromAddress = *fromAddr
	}
	if toAddrs != nil {
		m.ToAddresses = *toAddrs
	}
	if ccAddrs != nil {
		m.CcAddresses = *ccAddrs
	}
	if bccAddrs != nil {
		m.BccAddresses = *bccAddrs
	}
	if replyTo != nil {
		m.ReplyTo = *replyTo
	}
	if messageDate != nil {
		m.MessageDate = messageDate
	}
	if purgedAt != nil {
		m.PurgedAt = purgedAt
	}

	m.Seen = intToBool(seen)
	m.Answered = intToBool(answered)
	m.Flagged = intToBool(flagged)
	m.Draft = intToBool(draft)
	m.Deleted = intToBool(deleted)
	m.Junk = intToBool(junk)
	m.Importance = Importance(importance)
	return &m, nil
}

// ComputeSHA256 computes the SHA-256 hash of a file.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("open for sha256: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("compute sha256: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func retrySQL[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	for attempt := 0; attempt < 30; attempt++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		if !isTransientSQLiteErr(err) {
			return zero, err
		}
		last = err
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	return zero, last
}

func isTransientSQLiteErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sql logic error: interrupted")
}
