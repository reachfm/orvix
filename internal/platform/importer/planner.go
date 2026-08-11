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

	GetOrg(ctx context.Context, domain string, tenantID uint) (*EntityInfo, error)
	GetUser(ctx context.Context, email string) (*EntityInfo, error)
	GetDomain(ctx context.Context, name string) (*EntityInfo, error)
	GetMailbox(ctx context.Context, email string) (*EntityInfo, error)
	GetGroup(ctx context.Context, name string, tenantID uint) (*EntityInfo, error)
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
		case RowUpdated:
			report.Updated++
		case RowDeferred:
			report.Deferred++
		case RowSkipped:
			report.Unchanged++
		}
	}

	// Redact any secret-like field values before persisting the report so a
	// dry-run diff never leaks credentials.
	for i := range rows {
		rows[i].SafeData = RedactSensitive(rows[i].SafeData)
		rows[i].BeforeImage = RedactSensitive(rows[i].BeforeImage)
		rows[i].AfterImage = RedactSensitive(rows[i].AfterImage)
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
