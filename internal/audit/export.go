package audit

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type ExportFormat string

const (
	ExportJSON ExportFormat = "json"
	ExportCSV  ExportFormat = "csv"
)

type ExportJob struct {
	ID        uint         `json:"id"`
	Format    ExportFormat `json:"format"`
	Status    string       `json:"status"`
	Filters   string       `json:"filters,omitempty"`
	CreatedBy uint         `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
}

func (s *Store) ExportTo(ctx context.Context, q *Query, format ExportFormat, w io.Writer) error {
	entries, _, err := s.Search(ctx, q)
	if err != nil {
		return err
	}
	switch format {
	case ExportCSV:
		cw := csv.NewWriter(w)
		defer cw.Flush()
		if err := cw.Write([]string{"id", "timestamp", "actor", "role", "action", "target", "result", "tenant_id", "ip"}); err != nil {
			return err
		}
		for _, e := range entries {
			if err := cw.Write([]string{
				fmt.Sprintf("%d", e.ID),
				e.Timestamp.Format(time.RFC3339),
				e.Actor, e.Role, e.Action, e.Target, e.Result,
				fmt.Sprintf("%d", e.TenantID), e.IP,
			}); err != nil {
				return err
			}
		}
	case ExportJSON:
		enc := json.NewEncoder(w)
		return enc.Encode(entries)
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
	return nil
}

func (s *Store) GetEntry(ctx context.Context, id int64) (*Entry, error) {
	var e Entry
	err := s.db.QueryRowContext(ctx, "SELECT id, actor, role, action, target, result, ip, user_agent, tenant_id, timestamp FROM coremail_audit WHERE id=?", id).
		Scan(&e.ID, &e.Actor, &e.Role, &e.Action, &e.Target, &e.Result, &e.IP, &e.UserAgent, &e.TenantID, &e.Timestamp)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

var ErrNotFound = &auditError{"audit entry not found"}

type auditError struct{ msg string }

func (e *auditError) Error() string { return e.msg }
