package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Folder represents a mail folder (mailbox) in the IMAP hierarchy.
type Folder struct {
	ID           uint       `json:"id"`
	MailboxID    uint       `json:"mailbox_id"`
	ParentID     *uint      `json:"parent_id,omitempty"`
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	FolderType   FolderType `json:"folder_type"`
	MessageCount int        `json:"message_count"`
	UnreadCount  int        `json:"unread_count"`
	TotalSize    int64      `json:"total_size"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// FolderRepository defines the contract for folder persistence.
type FolderRepository interface {
	Create(ctx context.Context, f *Folder, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Folder, error)
	GetByPath(ctx context.Context, mailboxID uint, path string, tx interface{}) (*Folder, error)
	ListByMailbox(ctx context.Context, mailboxID uint, tx interface{}) ([]Folder, error)
	Update(ctx context.Context, f *Folder, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
	Rename(ctx context.Context, id uint, newName string, tx interface{}) error
	Move(ctx context.Context, id uint, newParentID *uint, tx interface{}) error
	UpdateCounts(ctx context.Context, id uint, deltaMsg, deltaUnread int, deltaSize int64, tx interface{}) error
	EnsureSystemFolders(ctx context.Context, mailboxID uint, tx interface{}) error
}

var _ FolderRepository = (*FolderSQLRepo)(nil)

// FolderSQLRepo implements FolderRepository using database/sql.
type FolderSQLRepo struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewFolderSQLRepo constructs the repository and detects the SQL
// dialect EAGERLY — never lazily inside a transaction (a probe on a
// single-connection SQLite pool would deadlock the acceptance tx).
func NewFolderSQLRepo(db *sql.DB) *FolderSQLRepo {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &FolderSQLRepo{db: db, dialect: dialect}
}

// getDialect returns the dialect detected at construction time.
func (r *FolderSQLRepo) getDialect() *dbdialect.Info {
	return r.dialect
}

// qf rewrites ? placeholders to $N for PostgreSQL (no-op on SQLite).
func (r *FolderSQLRepo) qf(sql string) string {
	return r.getDialect().Rewrite(sql)
}

func (r *FolderSQLRepo) exec(tx interface{}) interface {
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

func (r *FolderSQLRepo) Create(ctx context.Context, f *Folder, tx interface{}) error {
	now := nowFn()
	f.CreatedAt = now
	f.UpdatedAt = now
	if f.FolderType == "" {
		f.FolderType = FolderCustom
	}
	e := r.exec(tx)
	insert := `
		INSERT INTO coremail_folders (mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []interface{}{
		f.MailboxID, f.ParentID, f.Name, f.Path, string(f.FolderType),
		f.MessageCount, f.UnreadCount, f.TotalSize, f.CreatedAt, f.UpdatedAt,
	}
	if r.getDialect().IsPostgres() {
		var id uint
		if err := e.QueryRowContext(ctx, r.qf(insert)+" RETURNING id", args...).Scan(&id); err != nil {
			return fmt.Errorf("create folder: %w", err)
		}
		f.ID = id
		return nil
	}
	res, err := retrySQL(ctx, func() (sql.Result, error) {
		return e.ExecContext(ctx, insert, args...)
	})
	if err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	f.ID = uint(id)
	return nil
}

func (r *FolderSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Folder, error) {
	e := r.exec(tx)
	return retrySQL(ctx, func() (*Folder, error) {
		row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at
		FROM coremail_folders WHERE id = ?`), id)
		return scanFolder(row)
	})
}

func (r *FolderSQLRepo) GetByPath(ctx context.Context, mailboxID uint, path string, tx interface{}) (*Folder, error) {
	e := r.exec(tx)
	return retrySQL(ctx, func() (*Folder, error) {
		row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at
		FROM coremail_folders WHERE mailbox_id = ? AND path = ?`), mailboxID, path)
		return scanFolder(row)
	})
}

func (r *FolderSQLRepo) ListByMailbox(ctx context.Context, mailboxID uint, tx interface{}) ([]Folder, error) {
	return retrySQL(ctx, func() ([]Folder, error) {
		return r.listByMailboxOnce(ctx, mailboxID, tx)
	})
}

func (r *FolderSQLRepo) listByMailboxOnce(ctx context.Context, mailboxID uint, tx interface{}) ([]Folder, error) {
	e := r.exec(tx)
	rows, err := e.QueryContext(ctx, r.qf(`
		SELECT id, mailbox_id, parent_id, name, path, folder_type, message_count, unread_count, total_size, created_at, updated_at
		FROM coremail_folders WHERE mailbox_id = ? ORDER BY path`), mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, *f)
	}
	return folders, rows.Err()
}

func (r *FolderSQLRepo) Update(ctx context.Context, f *Folder, tx interface{}) error {
	f.UpdatedAt = nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, r.qf(`
		UPDATE coremail_folders SET name=?, folder_type=?, message_count=?, unread_count=?, total_size=?, updated_at=?
		WHERE id=?`), f.Name, string(f.FolderType), f.MessageCount, f.UnreadCount, f.TotalSize, f.UpdatedAt, f.ID)
	return err
}

func (r *FolderSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, r.qf("DELETE FROM coremail_folders WHERE id=?"), id)
	return err
}

func (r *FolderSQLRepo) Rename(ctx context.Context, id uint, newName string, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, r.qf("UPDATE coremail_folders SET name=?, path=?, updated_at=? WHERE id=?"), newName, newName, now, id)
	return err
}

func (r *FolderSQLRepo) Move(ctx context.Context, id uint, newParentID *uint, tx interface{}) error {
	now := nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, r.qf("UPDATE coremail_folders SET parent_id=?, updated_at=? WHERE id=?"), newParentID, now, id)
	return err
}

func (r *FolderSQLRepo) UpdateCounts(ctx context.Context, id uint, deltaMsg, deltaUnread int, deltaSize int64, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, r.qf(`
		UPDATE coremail_folders SET message_count = message_count + ?, unread_count = unread_count + ?, total_size = total_size + ?
		WHERE id = ? AND (message_count + ? >= 0)`),
		deltaMsg, deltaUnread, deltaSize, id, deltaMsg)
	return err
}

func (r *FolderSQLRepo) EnsureSystemFolders(ctx context.Context, mailboxID uint, tx interface{}) error {
	defaults := DefaultSystemFolders(mailboxID)
	for _, f := range defaults {
		existing, err := r.GetByPath(ctx, mailboxID, f.Path, tx)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		cp := f
		if err := r.Create(ctx, &cp, tx); err != nil {
			return fmt.Errorf("create system folder %s: %w", f.Name, err)
		}
	}
	return nil
}

func scanFolder(row interface {
	Scan(dest ...interface{}) error
}) (*Folder, error) {
	var f Folder
	var folderType string
	err := row.Scan(&f.ID, &f.MailboxID, &f.ParentID, &f.Name, &f.Path, &folderType,
		&f.MessageCount, &f.UnreadCount, &f.TotalSize, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan folder: %w", err)
	}
	f.FolderType = FolderType(folderType)
	return &f, nil
}
