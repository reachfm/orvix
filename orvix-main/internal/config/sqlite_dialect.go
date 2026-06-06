package config

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// sqliteDialect implements gorm.Dialector for modernc.org/sqlite
// This is a simple dialector that handles SQL generation without a custom migrator
type sqliteDialect struct{}

// Name returns the dialect name
func (d *sqliteDialect) Name() string {
	return "sqlite"
}

// Initialize initializes the dialect
func (d *sqliteDialect) Initialize(db *gorm.DB) error {
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