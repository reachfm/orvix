package incident

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	if _, err := r.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS platform_incidents ("+
		"id "+autoInc+", "+
		"title TEXT NOT NULL, "+
		"description TEXT NOT NULL DEFAULT '', "+
		"severity TEXT NOT NULL, "+
		"status TEXT NOT NULL DEFAULT 'investigating', "+
		"services TEXT NOT NULL DEFAULT '[]', "+
		"regions TEXT NOT NULL DEFAULT '[]', "+
		"tenant_ids TEXT NOT NULL DEFAULT '[]', "+
		"version INTEGER NOT NULL DEFAULT 1, "+
		"created_at "+ts+" NOT NULL, "+
		"updated_at "+ts+" NOT NULL, "+
		"resolved_at "+ts+")"); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS platform_incident_events ("+
		"id "+autoInc+", "+
		"incident_id INTEGER NOT NULL, "+
		"status TEXT NOT NULL DEFAULT '', "+
		"message TEXT NOT NULL, "+
		"operator TEXT NOT NULL DEFAULT '', "+
		"created_at "+ts+" NOT NULL)")
	return err
}

func (r *Repository) Insert(ctx context.Context, inc *Incident) error {
	srv, _ := json.Marshal(inc.Services)
	reg, _ := json.Marshal(inc.Regions)
	tid, _ := json.Marshal(inc.TenantIDs)
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx,
		"INSERT INTO platform_incidents (title, description, severity, status, services, regions, tenant_ids, version, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		inc.Title, inc.Description, inc.Severity, StatusInvestigating, string(srv), string(reg), string(tid), 1, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	inc.ID = uint(id)
	inc.CreatedAt = now
	inc.UpdatedAt = now
	inc.version = 1
	return nil
}

func (r *Repository) Update(ctx context.Context, inc *Incident) error {
	inc.UpdatedAt = time.Now().UTC()
	srv, _ := json.Marshal(inc.Services)
	reg, _ := json.Marshal(inc.Regions)
	tid, _ := json.Marshal(inc.TenantIDs)
	res, err := r.db.ExecContext(ctx,
		"UPDATE platform_incidents SET title=?, description=?, severity=?, status=?, services=?, regions=?, tenant_ids=?, version=version+1, updated_at=?, resolved_at=? WHERE id=? AND version=?",
		inc.Title, inc.Description, inc.Severity, inc.Status, string(srv), string(reg), string(tid), inc.UpdatedAt, inc.ResolvedAt, inc.ID, inc.version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errStale
	}
	inc.version++
	return nil
}

var errStale = &incidentError{"concurrent modification detected"}

func (r *Repository) Get(ctx context.Context, id uint) (*Incident, error) {
	row := r.db.QueryRowContext(ctx, "SELECT id, title, description, severity, status, services, regions, tenant_ids, version, created_at, updated_at, resolved_at FROM platform_incidents WHERE id=?", id)
	return r.scanIncident(row)
}

func (r *Repository) List(ctx context.Context, status string, limit int) ([]Incident, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = r.db.QueryContext(ctx, "SELECT id, title, description, severity, status, services, regions, tenant_ids, version, created_at, updated_at, resolved_at FROM platform_incidents WHERE status=? ORDER BY created_at DESC LIMIT ?", status, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, "SELECT id, title, description, severity, status, services, regions, tenant_ids, version, created_at, updated_at, resolved_at FROM platform_incidents ORDER BY created_at DESC LIMIT ?", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		inc, err := r.scanIncidentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inc)
	}
	return out, rows.Err()
}

func (r *Repository) AddTimelineEvent(ctx context.Context, ev *TimelineEvent) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO platform_incident_events (incident_id, status, message, operator, created_at) VALUES (?,?,?,?,?)",
		ev.IncidentID, ev.Status, ev.Message, ev.Operator, time.Now().UTC())
	return err
}

func (r *Repository) Timeline(ctx context.Context, incidentID uint) ([]TimelineEvent, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, incident_id, status, message, operator, created_at FROM platform_incident_events WHERE incident_id=? ORDER BY created_at ASC", incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimelineEvent
	for rows.Next() {
		var ev TimelineEvent
		if err := rows.Scan(&ev.ID, &ev.IncidentID, &ev.Status, &ev.Message, &ev.Operator, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (r *Repository) PublicStatus(ctx context.Context) (*PublicStatus, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, title, severity, status, created_at, updated_at FROM platform_incidents WHERE status IN ('investigating','identified','monitoring') ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := &PublicStatus{Overall: "operational", UpdatedAt: time.Now().UTC()}
	for rows.Next() {
		var pi PublicIncident
		if err := rows.Scan(&pi.ID, &pi.Title, &pi.Severity, &pi.Status, &pi.CreatedAt, &pi.UpdatedAt); err != nil {
			return nil, err
		}
		if pi.Severity == SevCritical {
			st.Overall = "outage"
		} else if st.Overall != "outage" {
			st.Overall = "degraded"
		}
		st.Incidents = append(st.Incidents, pi)
	}
	maintRows, err := r.db.QueryContext(ctx, "SELECT id, title, severity, status, created_at, updated_at FROM platform_incidents WHERE severity=? ORDER BY created_at DESC", SevScheduled)
	if err == nil {
		defer maintRows.Close()
		for maintRows.Next() {
			var pi PublicIncident
			if err := maintRows.Scan(&pi.ID, &pi.Title, &pi.Severity, &pi.Status, &pi.CreatedAt, &pi.UpdatedAt); err == nil {
				st.Maintenance = append(st.Maintenance, pi)
			}
		}
		if len(st.Maintenance) > 0 && st.Overall == "operational" {
			st.Overall = "maintenance"
		}
	}
	return st, rows.Err()
}

func (r *Repository) scanIncident(row *sql.Row) (*Incident, error) {
	var inc Incident
	var srvJSON, regJSON, tidJSON string
	var resolvedAt sql.NullTime
	err := row.Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &srvJSON, &regJSON, &tidJSON, &inc.version, &inc.CreatedAt, &inc.UpdatedAt, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(srvJSON), &inc.Services)
	json.Unmarshal([]byte(regJSON), &inc.Regions)
	json.Unmarshal([]byte(tidJSON), &inc.TenantIDs)
	if resolvedAt.Valid {
		inc.ResolvedAt = &resolvedAt.Time
	}
	return &inc, nil
}

func (r *Repository) scanIncidentRows(rows *sql.Rows) (*Incident, error) {
	var inc Incident
	var srvJSON, regJSON, tidJSON string
	var resolvedAt sql.NullTime
	if err := rows.Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &srvJSON, &regJSON, &tidJSON, &inc.version, &inc.CreatedAt, &inc.UpdatedAt, &resolvedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(srvJSON), &inc.Services)
	json.Unmarshal([]byte(regJSON), &inc.Regions)
	json.Unmarshal([]byte(tidJSON), &inc.TenantIDs)
	if resolvedAt.Valid {
		inc.ResolvedAt = &resolvedAt.Time
	}
	return &inc, nil
}

type incidentError struct{ msg string }

func (e *incidentError) Error() string { return e.msg }
