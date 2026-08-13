package importer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/orvix/orvix/internal/dbdialect"
)

// H-7 read-only diagnostic.
//
// The application-level guards in this package make NEW tenant-0 rows
// impossible. They do nothing about rows an installation may already carry
// from before the fix, and silently repairing such rows is not safe: only an
// operator can decide which tenant an orphan should have belonged to, and
// guessing would attach real mailboxes to the wrong customer.
//
// This diagnostic therefore only REPORTS. It performs no UPDATE, DELETE, or
// INSERT of any kind, so it is safe to run against production.

// OrphanCount is one table's tenant-0 row count.
type OrphanCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// OrphanReport is the full read-only scan result.
type OrphanReport struct {
	Counts []OrphanCount `json:"counts"`
	Total  int64         `json:"total"`
}

// tenantOwnedTables are the tenant-owned tables an import can write. Each is
// scanned for tenant_id = 0. `tenants` itself is deliberately excluded (it has
// no tenant_id), as are platform-owned tables, which legitimately have none.
var tenantOwnedTables = []string{
	"users",
	"coremail_domains",
	"coremail_mailboxes",
	"coremail_aliases",
	"coremail_groups",
}

// ScanTenantZeroOrphans reports how many tenant-owned rows carry tenant_id = 0.
//
// It is strictly read-only. A table that does not exist on this installation
// is skipped rather than failing the whole scan, so the diagnostic is usable
// across schema versions.
func ScanTenantZeroOrphans(ctx context.Context, db *sql.DB, dialect *dbdialect.Info) (*OrphanReport, error) {
	if db == nil {
		return nil, fmt.Errorf("orphan scan: database handle is required")
	}
	if dialect == nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	report := &OrphanReport{}
	for _, table := range tenantOwnedTables {
		var n int64
		// Table names come from the fixed allowlist above, never from input.
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE tenant_id = ` + dialect.Placeholder(1)
		if err := db.QueryRowContext(ctx, q, 0).Scan(&n); err != nil {
			// Missing table / missing column on this installation: skip it
			// rather than aborting the scan.
			continue
		}
		report.Counts = append(report.Counts, OrphanCount{Table: table, Rows: n})
		report.Total += n
	}
	return report, nil
}
