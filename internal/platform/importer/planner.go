package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Planner struct {
	db       *sql.DB
	tenantID uint
}

func NewPlanner(db *sql.DB, tenantID uint) *Planner {
	return &Planner{db: db, tenantID: tenantID}
}

func (p *Planner) DryRun(ctx context.Context, source *ParsedSource, conflict ConflictPolicy) (*ValidationReport, error) {
	validator := NewValidator(p.db, p.tenantID, source, conflict)
	rows, err := validator.ValidateAll(ctx)
	if err != nil {
		return nil, err
	}

	report := &ValidationReport{
		ImportID:      0,
		SourceHash:    "",
		SchemaVersion: source.SchemaVersion,
		Total:         len(rows),
		Rows:          rows,
		GeneratedAt:   time.Now().UTC(),
	}

	for _, row := range rows {
		switch row.Status {
		case RowValid:
			report.Valid++
		case RowInvalid:
			report.Invalid++
		case RowConflict:
			report.Conflict++
		case RowDeferred:
			report.Deferred++
		case RowSkipped:
			report.Unchanged++
		}
	}

	// Build ordered report ensuring dependency order
	report.Rows = reorderByDependency(rows)

	return report, nil
}

func reorderByDependency(rows []ImportRow) []ImportRow {
	orderMap := make(map[ImportEntityType]int)
	for i, entity := range EntityDependencyOrder() {
		orderMap[entity] = i
	}
	sorted := make([]ImportRow, len(rows))
	copy(sorted, rows)
	// Stable sort by dependency order
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if orderMap[sorted[i].Entity] > orderMap[sorted[j].Entity] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func (p *Planner) ValidateForExecution(ctx context.Context, job *ImportJob, currentHash string) error {
	if job.SourceHash != currentHash {
		return newImportError(CodeHashMismatch, "input hash does not match validated hash")
	}
	if job.Status != StatusValidated {
		return newImportError(CodeHashMismatch, "import has not been validated, run validate first")
	}
	var reportStr sql.NullString
	err := p.db.QueryRowContext(ctx, `SELECT validation_report FROM platform_imports WHERE id=?`, job.ID).Scan(&reportStr)
	if err != nil {
		return err
	}
	if !reportStr.Valid || reportStr.String == "" {
		return newImportError(CodeHashMismatch, "no validation report found, run validate first")
	}
	var report ValidationReport
	if err := json.Unmarshal([]byte(reportStr.String), &report); err != nil {
		return err
	}
	if report.Valid == 0 {
		return newImportError(CodeInvalidSource, "validation report has no valid rows")
	}
	return nil
}
