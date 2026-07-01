package handlers

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// BulkImportRow is one parsed CSV row. Fields are raw — the validator
// produces a BulkImportError per row when something is wrong. The
// Password field is intentionally never logged or echoed in responses.
type BulkImportRow struct {
	Line     int    // 1-based line number in the original CSV (header is line 1)
	Email    string
	Password string
	Name     string
	QuotaMB  int64
}

// BulkImportError is a per-row validation or persistence error. The
// Email and Line fields help the operator identify the failing row; the
// Password field is NEVER included in the response.
type BulkImportError struct {
	Line  int    `json:"line"`
	Email string `json:"email,omitempty"`
	Error string `json:"error"`
}

// BulkImportResult is the response shape for /mailboxes/import and
// /mailboxes/import/dry-run. Created, Skipped, Errors, and DryRun are
// always returned. Passwords are NEVER included.
type BulkImportResult struct {
	DryRun  bool               `json:"dryRun"`
	Created int                `json:"created"`
	Skipped int                `json:"skipped"`
	Errors  []BulkImportError  `json:"errors"`
	Planned []BulkImportRow    `json:"planned,omitempty"` // dry-run only
}

// Hard caps to prevent OOM and lock contention on a single request.
const (
	bulkImportMaxRows      = 5000
	bulkImportMaxBytes     = 8 << 20 // 8 MiB
	bulkImportMinPassword  = 8
	bulkImportDefaultQuota = 1024
)

// ImportMailboxesCSV serves POST /api/v1/admin/mailboxes/import.
//
// The endpoint accepts a CSV body with columns:
//   email,password,name,quota_mb
//
// The header row is required. The handler validates every row, then
// either rolls everything back (default all-or-nothing) or inserts the
// valid rows (when allow_partial=true).
//
// Security: passwords are NEVER included in the response, NEVER logged.
// The audit log records only counts, not credentials.
func (h *Handler) ImportMailboxesCSV(c fiber.Ctx) error {
	return h.importMailboxes(c, false)
}

// ImportMailboxesDryRun serves POST /api/v1/admin/mailboxes/import/dry-run.
//
// The endpoint parses and validates the CSV the same way as the real
// import but does NOT touch the database. The response includes the
// planned rows so the operator can review before committing.
func (h *Handler) ImportMailboxesDryRun(c fiber.Ctx) error {
	return h.importMailboxes(c, true)
}

func (h *Handler) importMailboxes(c fiber.Ctx, dryRun bool) error {
	// Cap body size so a hostile or runaway client cannot OOM the
	// process. Fiber's body_limit config catches the obvious cases;
	// we add an explicit guard here for the multipart path too.
	body := c.Body()
	if len(body) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "empty request body"})
	}
	if len(body) > bulkImportMaxBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "CSV body too large"})
	}

	// allow_partial is a body or query flag; the default is
	// all-or-nothing so a single bad row never produces a partial
	// import the operator did not intend.
	allowPartial := false
	if q := c.Query("allow_partial"); q != "" {
		allowPartial = parseBulkBool(q)
	}
	// allow_partial is also accepted in a small JSON envelope for
	// clients that prefer to keep query strings out of URLs. The
	// envelope can also carry the CSV body in the `csv` field so a
	// client that cannot set Content-Type: text/csv cleanly still
	// has a working path.
	if c.Method() == fiber.MethodPost && len(body) > 0 && body[0] == '{' {
		var envelope struct {
			CSV          string `json:"csv"`
			AllowPartial bool   `json:"allow_partial"`
		}
		if err := json.Unmarshal(body, &envelope); err == nil && (envelope.CSV != "" || envelope.AllowPartial) {
			if envelope.CSV != "" {
				body = []byte(envelope.CSV)
			}
			if envelope.AllowPartial {
				allowPartial = true
			}
		}
	}

	rows, parseErrs := parseBulkImportCSV(body)
	// A hard parse error (no header / completely malformed) aborts
	// the whole request before any row-level work.
	if len(rows) == 0 && len(parseErrs) > 0 && !dryRun {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":  "CSV could not be parsed",
			"errors": parseErrs,
		})
	}
	if len(rows) > bulkImportMaxRows {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": fmt.Sprintf("CSV too large: %d rows, max %d", len(rows), bulkImportMaxRows),
		})
	}

	// Resolve password-min-length from config (fall back to a safe
	// default so a missing config never silently accepts a 1-char
	// password).
	pwMin := bulkImportMinPassword
	if h.cfg != nil && h.cfg.Auth.PasswordMinLen > 0 {
		pwMin = h.cfg.Auth.PasswordMinLen
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	if dryRun {
		// Dry-run: validate every row against the live DB state but
		// never write. Duplicate / unknown-domain / weak-password
		// / etc. all become row-level errors. The "planned" slice
		// never includes the password.
		planned := make([]BulkImportRow, 0, len(rows))
		var outErrs []BulkImportError
		// Append parse errors first so the operator sees the full picture.
		outErrs = append(outErrs, parseErrs...)
		for _, row := range rows {
			if errStr := validateBulkRow(sqlDB, row, pwMin); errStr != "" {
				outErrs = append(outErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: errStr})
				continue
			}
			planned = append(planned, BulkImportRow{
				Line:    row.Line,
				Email:   row.Email,
				Name:    row.Name,
				QuotaMB: row.QuotaMB,
			})
		}
		if outErrs == nil {
			outErrs = []BulkImportError{}
		}
		return c.JSON(BulkImportResult{
			DryRun:  true,
			Created: 0,
			Skipped: 0,
			Errors:  outErrs,
			Planned: planned,
		})
	}

	// Real import: validate every row first, then either commit or
	// roll back based on allow_partial.
	var rowErrs []BulkImportError
	rowErrs = append(rowErrs, parseErrs...)
	seenEmails := make(map[string]int, len(rows))
	var valid []BulkImportRow
	for _, row := range rows {
		// In-batch duplicate check: catches two rows with the same
		// email BEFORE either reaches the database. The DB-level
		// UNIQUE constraint is a second line of defence.
		if _, dup := seenEmails[row.Email]; dup {
			rowErrs = append(rowErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: "duplicate email in batch"})
			continue
		}
		seenEmails[row.Email] = row.Line
		if errStr := validateBulkRow(sqlDB, row, pwMin); errStr != "" {
			rowErrs = append(rowErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: errStr})
			continue
		}
		valid = append(valid, row)
	}

	if !allowPartial && len(rowErrs) > 0 {
		// All-or-nothing: refuse to import anything.
		h.writeAuditLog(c, "mailbox.bulk_create", fmt.Sprintf("rejected|count:%d|valid:%d|errors:%d", len(rows), len(valid), len(rowErrs)))
		return c.Status(fiber.StatusBadRequest).JSON(BulkImportResult{
			DryRun:  false,
			Created: 0,
			Skipped: 0,
			Errors:  rowErrs,
		})
	}

	// Wrap the actual inserts in a transaction so a mid-import
	// failure (e.g. DB error) rolls back everything. The pre-check
	// already guarded the unique constraint; the tx is the
	// second-line defence against partial writes.
	tx, err := sqlDB.BeginTx(c.Context(), nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	created := 0
	insertErrs := make([]BulkImportError, 0)
	for _, row := range valid {
		hash, herr := hashPasswordArgon2id(row.Password)
		if herr != nil {
			insertErrs = append(insertErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: "password hashing failed"})
			if !allowPartial {
				_ = tx.Rollback()
				rolledBack = true
				return c.Status(fiber.StatusInternalServerError).JSON(BulkImportResult{
					DryRun:  false,
					Created: 0,
					Skipped: 0,
					Errors:  append(rowErrs, insertErrs...),
				})
			}
			continue
		}
		parts := strings.SplitN(row.Email, "@", 2)
		var domainID, tenantID int64
		err := tx.QueryRowContext(c.Context(),
			`SELECT id, tenant_id FROM coremail_domains WHERE name = ? AND deleted_at IS NULL AND status = 'active'`,
			parts[1]).Scan(&domainID, &tenantID)
		if err != nil {
			insertErrs = append(insertErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: "domain not found or inactive"})
			if !allowPartial {
				_ = tx.Rollback()
				rolledBack = true
				return c.Status(fiber.StatusBadRequest).JSON(BulkImportResult{
					DryRun:  false,
					Created: 0,
					Skipped: 0,
					Errors:  append(rowErrs, insertErrs...),
				})
			}
			continue
		}
		quota := row.QuotaMB
		if quota <= 0 {
			quota = bulkImportDefaultQuota
		}
		res, ierr := tx.ExecContext(c.Context(),
			`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, 'argon2id', 'active', ?, 0, ?, ?)`,
			domainID, tenantID, parts[0], row.Email, row.Name, hash, quota, now, now)
		if ierr != nil {
			insertErrs = append(insertErrs, BulkImportError{Line: row.Line, Email: row.Email, Error: "insert failed"})
			if !allowPartial {
				_ = tx.Rollback()
				rolledBack = true
				return c.Status(fiber.StatusInternalServerError).JSON(BulkImportResult{
					DryRun:  false,
					Created: 0,
					Skipped: 0,
					Errors:  append(rowErrs, insertErrs...),
				})
			}
			continue
		}
		mboxID, _ := res.LastInsertId()
		// System folders (INBOX, Sent, …) are provisioned on the
		// first webmail login via the same safety net the single
		// CreateMailbox handler relies on. Doing it inside the bulk
		// tx would require either (a) running the provision on a
		// different connection (which can deadlock with SQLite's
		// _txlock=immediate setups used in production-style DSNs)
		// or (b) extending EnsureMailboxSystemFolders to accept a
		// *sql.Tx. Both are larger than the bulk-import contract
		// warrants; the webmail-login re-provision is documented
		// to backfill any mailbox created without folders, and the
		// single-row CreateMailbox path already uses it as the
		// primary failure-recovery mechanism.
		_ = mboxID
		created++
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		rolledBack = true
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "commit failed"})
	}
	rolledBack = true // commit succeeded

	h.writeAuditLog(c, "mailbox.bulk_create",
		fmt.Sprintf("count:%d|created:%d|errors:%d|allow_partial:%t", len(rows), created, len(insertErrs)+len(rowErrs), allowPartial))

	allErrs := append(rowErrs, insertErrs...)
	return c.Status(fiber.StatusCreated).JSON(BulkImportResult{
		DryRun:  false,
		Created: created,
		Skipped: len(rowErrs) + len(insertErrs),
		Errors:  allErrs,
	})
}

func parseBulkBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

// parseBulkImportCSV parses a CSV body into rows. It enforces the
// required header (email,password,name,quota_mb) and surfaces
// per-row parse errors without stopping at the first one.
func parseBulkImportCSV(body []byte) ([]BulkImportRow, []BulkImportError) {
	reader := csv.NewReader(bytes.NewReader(body))
	reader.FieldsPerRecord = -1 // allow variable length; we validate per row
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err == io.EOF {
		return nil, []BulkImportError{{Line: 0, Error: "empty CSV"}}
	}
	if err != nil {
		return nil, []BulkImportError{{Line: 0, Error: "CSV header could not be parsed"}}
	}
	idx, herr := bulkHeaderIndex(header)
	if herr != "" {
		return nil, []BulkImportError{{Line: 1, Error: herr}}
	}

	var rows []BulkImportRow
	var errs []BulkImportError
	line := 1
	for {
		line++
		rec, rerr := reader.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			errs = append(errs, BulkImportError{Line: line, Error: "row could not be parsed: " + rerr.Error()})
			continue
		}
		row := BulkImportRow{
			Line:    line,
			Email:   strings.TrimSpace(getCSVField(rec, idx["email"])),
			Password: getCSVField(rec, idx["password"]), // do NOT trim — passwords are literal
			Name:    strings.TrimSpace(getCSVField(rec, idx["name"])),
		}
		if q := strings.TrimSpace(getCSVField(rec, idx["quota_mb"])); q != "" {
			n, perr := strconv.ParseInt(q, 10, 64)
			if perr != nil {
				errs = append(errs, BulkImportError{Line: line, Email: row.Email, Error: "invalid quota_mb"})
				continue
			}
			row.QuotaMB = n
		}
		rows = append(rows, row)
	}
	return rows, errs
}

func bulkHeaderIndex(header []string) (map[string]int, string) {
	want := []string{"email", "password", "name", "quota_mb"}
	idx := make(map[string]int, len(want))
	for i, col := range header {
		idx[strings.ToLower(strings.TrimSpace(col))] = i
	}
	var missing []string
	for _, w := range want {
		if _, ok := idx[w]; !ok {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Sprintf("missing required columns: %s", strings.Join(missing, ","))
	}
	return idx, ""
}

func getCSVField(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// validateBulkRow checks a parsed row against the live database state
// and password policy. It returns "" for valid rows or a stable,
// human-readable error label otherwise. The returned string never
// includes the password.
func validateBulkRow(sqlDB *sql.DB, row BulkImportRow, pwMin int) string {
	if row.Email == "" {
		return "email is required"
	}
	parsed, err := mail.ParseAddress(row.Email)
	if err != nil || parsed.Address != row.Email || strings.ContainsAny(row.Email, " \t\r\n") {
		return "invalid email format"
	}
	parts := strings.SplitN(row.Email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "invalid email format"
	}
	if row.Password == "" {
		return "password is required"
	}
	if len(row.Password) < pwMin {
		return fmt.Sprintf("password must be at least %d characters", pwMin)
	}
	// Domain must be local, active.
	var domainID int64
	err = sqlDB.QueryRow(
		`SELECT id FROM coremail_domains WHERE name = ? AND deleted_at IS NULL AND status = 'active'`,
		parts[1]).Scan(&domainID)
	if errors.Is(err, sql.ErrNoRows) {
		return "domain not found or inactive: " + parts[1]
	}
	if err != nil {
		return "domain lookup failed"
	}
	// Duplicate mailbox check.
	var existing int64
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL`,
		row.Email).Scan(&existing); err == nil && existing > 0 {
		return "mailbox already exists: " + row.Email
	}
	// Quota sanity.
	if row.QuotaMB < 0 {
		return "quota_mb must be >= 0"
	}
	return ""
}
