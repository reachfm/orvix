package importer

import (
	"context"
	"database/sql"

	"github.com/orvix/orvix/internal/dbdialect"
)

// insertReturningID executes a dialect-portable INSERT that must produce a
// new row id. On PostgreSQL it appends `RETURNING id` and scans the returned
// id; on SQLite it uses LastInsertId. It rewrites `?` placeholders via the
// dialect so both databases share one SQL string.
func insertReturningID(ctx context.Context, db *sql.DB, dialect *dbdialect.Info, query string, args ...any) (uint, error) {
	rewritten := dialect.Rewrite(query)
	if dialect.IsPostgres() {
		var id uint
		err := db.QueryRowContext(ctx, rewritten+` RETURNING id`, args...).Scan(&id)
		if err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := db.ExecContext(ctx, rewritten, args...)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}
