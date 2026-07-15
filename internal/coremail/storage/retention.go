package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RetentionPolicy defines how long messages are kept and what happens after expiry.
type RetentionPolicy struct {
	ID                uint          `json:"id"`
	Name              string        `json:"name"`
	RetentionType     RetentionType `json:"retention_type"`
	RetentionDays     int           `json:"retention_days"`
	MaxMessages       int           `json:"max_messages"`
	MaxSizeBytes      int64         `json:"max_size_bytes"`
	DeleteAfterExpiry bool          `json:"delete_after_expiry"`
	Hold              bool          `json:"hold"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// RetentionRepository defines the contract for retention policy persistence.
type RetentionRepository interface {
	Create(ctx context.Context, p *RetentionPolicy, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*RetentionPolicy, error)
	List(ctx context.Context, tx interface{}) ([]RetentionPolicy, error)
	Update(ctx context.Context, p *RetentionPolicy, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
}

var _ RetentionRepository = (*RetentionSQLRepo)(nil)

// RetentionSQLRepo implements RetentionRepository using database/sql.
type RetentionSQLRepo struct {
	db *sql.DB
}

func NewRetentionSQLRepo(db *sql.DB) *RetentionSQLRepo {
	return &RetentionSQLRepo{db: db}
}

func (r *RetentionSQLRepo) exec(tx interface{}) interface {
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

func (r *RetentionSQLRepo) Create(ctx context.Context, p *RetentionPolicy, tx interface{}) error {
	now := nowFn()
	p.CreatedAt = now
	p.UpdatedAt = now
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, `
		INSERT INTO coremail_retention_policies (name, retention_type, retention_days, max_messages, max_size_bytes, delete_after_expiry, hold, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, string(p.RetentionType), p.RetentionDays, p.MaxMessages, p.MaxSizeBytes,
		boolToInt(p.DeleteAfterExpiry), boolToInt(p.Hold), p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create retention policy: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = uint(id)
	return nil
}

func (r *RetentionSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*RetentionPolicy, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, "SELECT id, name, retention_type, retention_days, max_messages, max_size_bytes, delete_after_expiry, hold, created_at, updated_at FROM coremail_retention_policies WHERE id=?", id)
	return scanRetentionPolicy(row)
}

func (r *RetentionSQLRepo) List(ctx context.Context, tx interface{}) ([]RetentionPolicy, error) {
	e := r.exec(tx)
	rows, err := e.QueryContext(ctx, "SELECT id, name, retention_type, retention_days, max_messages, max_size_bytes, delete_after_expiry, hold, created_at, updated_at FROM coremail_retention_policies ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var policies []RetentionPolicy
	for rows.Next() {
		p, err := scanRetentionPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, *p)
	}
	return policies, rows.Err()
}

func (r *RetentionSQLRepo) Update(ctx context.Context, p *RetentionPolicy, tx interface{}) error {
	p.UpdatedAt = nowFn()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, `
		UPDATE coremail_retention_policies SET name=?, retention_type=?, retention_days=?, max_messages=?, max_size_bytes=?,
			delete_after_expiry=?, hold=?, updated_at=? WHERE id=?`,
		p.Name, string(p.RetentionType), p.RetentionDays, p.MaxMessages, p.MaxSizeBytes,
		boolToInt(p.DeleteAfterExpiry), boolToInt(p.Hold), p.UpdatedAt, p.ID)
	return err
}

func (r *RetentionSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, "DELETE FROM coremail_retention_policies WHERE id=?", id)
	return err
}

func scanRetentionPolicy(row interface {
	Scan(dest ...interface{}) error
}) (*RetentionPolicy, error) {
	var p RetentionPolicy
	var retentionType string
	var deleteAfterExpiry, hold int
	err := row.Scan(&p.ID, &p.Name, &retentionType, &p.RetentionDays, &p.MaxMessages, &p.MaxSizeBytes,
		&deleteAfterExpiry, &hold, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan retention policy: %w", err)
	}
	p.RetentionType = RetentionType(retentionType)
	p.DeleteAfterExpiry = intToBool(deleteAfterExpiry)
	p.Hold = intToBool(hold)
	return &p, nil
}

// RetentionFramework provides methods to evaluate and enforce retention policies.
type RetentionFramework struct {
	Store *MailStore
	Repo  RetentionRepository
}

// NewRetentionFramework creates a retention framework.
func NewRetentionFramework(ms *MailStore, repo RetentionRepository) *RetentionFramework {
	return &RetentionFramework{Store: ms, Repo: repo}
}

// EvaluatePolicy checks how many messages would be affected by a policy.
// This is a planning/audit tool, not an enforcement action.
func (rf *RetentionFramework) EvaluatePolicy(ctx context.Context, policyID, mailboxID uint) (int, int64, error) {
	policy, err := rf.Repo.GetByID(ctx, policyID, nil)
	if err != nil {
		return 0, 0, err
	}
	if policy == nil {
		return 0, 0, fmt.Errorf("policy %d not found", policyID)
	}

	// Count messages that would be affected based on the policy type.
	switch policy.RetentionType {
	case RetentionByAge:
		if policy.RetentionDays <= 0 {
			return 0, 0, nil
		}
		cutoff := nowFn().AddDate(0, 0, -policy.RetentionDays)
		// Placeholder: actual implementation would query for messages older than cutoff
		_ = cutoff
	case RetentionByCount:
		if policy.MaxMessages <= 0 {
			return 0, 0, nil
		}
		// Placeholder: count messages exceeding MaxMessages
	case RetentionBySize:
		if policy.MaxSizeBytes <= 0 {
			return 0, 0, nil
		}
		// Placeholder: sum message sizes over MaxSizeBytes
	}
	return 0, 0, nil
}
