package importer

import (
	"context"
	"time"
)

type Planner struct {
	tenantID uint
	lookup   EntityLookup
	adapters *Adapters
}

type EntityLookup interface {
	OrgExists(ctx context.Context, domain string, tenantID uint) (bool, error)
	UserExists(ctx context.Context, email string) (bool, error)
	DomainExists(ctx context.Context, name string) (bool, uint, error)
	MailboxExists(ctx context.Context, email string) (bool, error)
}

func NewPlanner(lookup EntityLookup, tenantID uint, adapters *Adapters) *Planner {
	return &Planner{tenantID: tenantID, lookup: lookup, adapters: adapters}
}

func (p *Planner) DryRun(ctx context.Context, source *ParsedSource, conflict ConflictPolicy) (*ValidationReport, error) {
	validator := NewValidator(p.lookup, p.tenantID, source, conflict)
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
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if orderMap[sorted[i].Entity] > orderMap[sorted[j].Entity] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
