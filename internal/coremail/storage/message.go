package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
		where = append(where, "(subject LIKE ? OR from_address LIKE ? OR to_addresses LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s, s)
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
