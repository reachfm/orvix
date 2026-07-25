package config

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// sqliteDialect implements gorm.Dialector for modernc.org/sqlite
// This is a simple dialector that handles SQL generation without a custom migrator
type sqliteDialect struct {
	// ConnPool is the already-opened *sql.DB (or any gorm.ConnPool) to
	// use for this connection. It MUST be set before gorm.Open() is
	// called and MUST be assigned to db.ConnPool from inside
	// Initialize — not by the caller after gorm.Open returns.
	//
	// gorm.Open() builds the *gorm.DB's initial *gorm.Statement
	// (copying db.ConnPool into Statement.ConnPool) immediately after
	// calling Dialector.Initialize, and every later db.Create/First/
	// Save/Delete call clones its per-call Statement from that
	// original one. If db.ConnPool is assigned by the caller AFTER
	// gorm.Open() returns (as this code previously did), the already
	//-cloned Statement.ConnPool stays nil forever, and GORM's
	// cloned Statement.ConnPool stays nil forever, and GORM's
	// generated callbacks panic with a nil pointer dereference the
	// first time they try to run real SQL through it.
	ConnPool gorm.ConnPool
}

// Name returns the dialect name
func (d *sqliteDialect) Name() string {
	return "sqlite"
}

// Initialize initializes the dialect.
//
// SECURITY / CORRECTNESS FIX: this used to be a no-op. GORM's
// db.Create/db.First/db.Where(...).Find/db.Save/db.Delete etc. only do
// anything because gorm.Open() runs Dialector.Initialize(db), and the
// standard dialectors (postgres, the official sqlite driver) use that
// hook to call callbacks.RegisterDefaultCallbacks, which wires the
// Create/Query/Update/Delete/Row callback chains that actually build
// and execute SQL. Because this custom dialector's Initialize did not
// register any callbacks, every GORM CRUD call against a SQLite
// connection silently executed ZERO SQL and returned a nil error
// (there is no callback in the chain to produce anything else) —
// db.Create appeared to "succeed" while never inserting a row, and
// db.Where(...).First(&record) appeared to "succeed" (err == nil)
// while never running a query and leaving `record` at its zero value.
//
// This is what made internal/auth/csrf.go's Middleware() accept any
// cookie/header pair that matched each other: the DB lookup that was
// supposed to reject non-issued tokens never ran, so it never
// returned gorm.ErrRecordNotFound and the request was let through.
// The same silent no-op applied to every other GORM-based read/write
// on SQLite, not just CSRF.
func (d *sqliteDialect) Initialize(db *gorm.DB) error {
	if d.ConnPool != nil {
		db.ConnPool = d.ConnPool
	}
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		// modernc.org/sqlite's driver.Result implements LastInsertId
		// the normal way (no RETURNING clause required and no id
		// reversal quirk), so leave everything else at GORM's
		// defaults (INSERT/VALUES/ON CONFLICT, SELECT/FROM/WHERE/...,
		// UPDATE/SET/WHERE, DELETE/FROM/WHERE).
	})
	return nil
}

// Migrator returns nil - we use raw SQL migrations instead
func (d *sqliteDialect) Migrator(db *gorm.DB) gorm.Migrator {
	return nil
}

// DataTypeOf returns the SQLite data type for a GORM field
func (d *sqliteDialect) DataTypeOf(field *schema.Field) string {
	switch field.DataType {
	case schema.Bool:
		return "boolean"
	case schema.Int, schema.Uint:
		if field.AutoIncrement {
			return "integer PRIMARY KEY AUTOINCREMENT"
		}
		return "integer"
	case schema.Float:
		return "real"
	case schema.String:
		if field.Size > 0 && field.Size <= 65535 {
			return fmt.Sprintf("varchar(%d)", field.Size)
		}
		return "text"
	case schema.Time:
		return "datetime"
	case schema.Bytes:
		return "blob"
	default:
		return "text"
	}
}

// DefaultValueOf returns the default value expression for a field
func (d *sqliteDialect) DefaultValueOf(field *schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

// BindVarTo writes a bind variable placeholder
func (d *sqliteDialect) BindVarTo(w clause.Writer, stmt *gorm.Statement, v interface{}) {
	w.WriteByte('?')
}

// QuoteTo writes a quoted identifier
func (d *sqliteDialect) QuoteTo(w clause.Writer, str string) {
	w.WriteByte('"')
	w.WriteString(str)
	w.WriteByte('"')
}

// Explain returns the SQL with variables
func (d *sqliteDialect) Explain(sql string, vars ...interface{}) string {
	return sql
}
