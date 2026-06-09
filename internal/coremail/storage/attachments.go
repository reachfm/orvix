package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Attachment represents a file attached to an email message.
type Attachment struct {
	ID          uint      `json:"id"`
	MessageID   uint      `json:"message_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	StoragePath string    `json:"storage_path"` // path on local disk; future: S3 key
	CID         string    `json:"cid,omitempty"` // Content-ID for inline attachments
	CreatedAt   time.Time `json:"created_at"`
}

// AttachmentRepository defines the contract for attachment persistence.
type AttachmentRepository interface {
	Create(ctx context.Context, a *Attachment, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Attachment, error)
	ListByMessage(ctx context.Context, messageID uint, tx interface{}) ([]Attachment, error)
	Delete(ctx context.Context, id uint, tx interface{}) error
	DeleteByMessage(ctx context.Context, messageID uint, tx interface{}) error
	CountByMessage(ctx context.Context, messageID uint, tx interface{}) (int64, error)
}

var _ AttachmentRepository = (*AttachmentSQLRepo)(nil)

// AttachmentSQLRepo implements AttachmentRepository using database/sql.
type AttachmentSQLRepo struct {
	db *sql.DB
}

func NewAttachmentSQLRepo(db *sql.DB) *AttachmentSQLRepo {
	return &AttachmentSQLRepo{db: db}
}

func (r *AttachmentSQLRepo) exec(tx interface{}) interface {
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

func (r *AttachmentSQLRepo) Create(ctx context.Context, a *Attachment, tx interface{}) error {
	a.CreatedAt = nowFn()
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `
		INSERT INTO coremail_attachments (message_id, filename, content_type, size_bytes, sha256, storage_path, cid, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.MessageID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.StoragePath, a.CID, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create attachment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = uint(id)
	return nil
}

func (r *AttachmentSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Attachment, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, "SELECT id, message_id, filename, content_type, size_bytes, sha256, storage_path, cid, created_at FROM coremail_attachments WHERE id=?", id)
	return scanAttachment(row)
}

func (r *AttachmentSQLRepo) ListByMessage(ctx context.Context, messageID uint, tx interface{}) ([]Attachment, error) {
	e := r.exec(tx)
	rows, err := e.QueryContext(ctx, "SELECT id, message_id, filename, content_type, size_bytes, sha256, storage_path, cid, created_at FROM coremail_attachments WHERE message_id=? ORDER BY id", messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attachments []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, *a)
	}
	return attachments, rows.Err()
}

func (r *AttachmentSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "DELETE FROM coremail_attachments WHERE id=?", id)
	return err
}

func (r *AttachmentSQLRepo) DeleteByMessage(ctx context.Context, messageID uint, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "DELETE FROM coremail_attachments WHERE message_id=?", messageID)
	return err
}

func (r *AttachmentSQLRepo) CountByMessage(ctx context.Context, messageID uint, tx interface{}) (int64, error) {
	e := r.exec(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_attachments WHERE message_id=?", messageID).Scan(&count)
	return count, err
}

func scanAttachment(row interface{ Scan(dest ...interface{}) error }) (*Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.StoragePath, &a.CID, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan attachment: %w", err)
	}
	return &a, nil
}
