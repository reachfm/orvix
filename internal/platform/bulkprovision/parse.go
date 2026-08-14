package bulkprovision

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// knownHeaders is the closed set of recognized template columns.
// "email" alone or "local_part"+"domain" together identify the row;
// exactly one identity form must be present, never both, never
// neither. Any column outside this set makes the whole file rejected
// rather than silently ignored — an unrecognized column is far more
// likely a mistaken/ambiguous mapping than an intentional extra field.
var knownHeaders = map[string]bool{
	"email":             true,
	"local_part":        true,
	"domain":            true,
	"display_name":      true,
	"name":              true, // accepted alias for display_name
	"quota_mb":          true,
	"quota_bytes":       true,
	"access_mode":       true,
	"generate_password": true, // accepted but inert: a password is ALWAYS generated and discarded, never accepted from the file
	"external_ref":      true,
}

// formulaInjectionPrefixes are the leading characters a spreadsheet
// application (or a naive CSV-to-shell pipeline) may interpret as the
// start of a formula/command when a cell is opened. Any cell value
// beginning with one of these is rejected outright, in every column,
// not merely the identity columns.
var formulaInjectionPrefixes = []byte{'=', '+', '-', '@', '\t', '\r'}

func hasFormulaInjectionPrefix(v string) bool {
	if v == "" {
		return false
	}
	b := v[0]
	for _, p := range formulaInjectionPrefixes {
		if b == p {
			return true
		}
	}
	return false
}

// columnMap resolves normalized (lowercased, trimmed) headers to their
// index, rejecting duplicates and unknown columns up front.
type columnMap struct {
	idx map[string]int
}

func newColumnMap(headers []string) (*columnMap, error) {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		norm := strings.ToLower(strings.TrimSpace(h))
		if norm == "" {
			continue
		}
		if !knownHeaders[norm] {
			return nil, fmt.Errorf("%w: %q", ErrUnknownColumn, h)
		}
		if _, dup := idx[norm]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateHeader, h)
		}
		idx[norm] = i
	}
	if _, hasEmail := idx["email"]; hasEmail {
		if _, hasLocal := idx["local_part"]; hasLocal {
			return nil, errors.New("provide either \"email\" or \"local_part\"+\"domain\", not both")
		}
	} else {
		_, hasLocal := idx["local_part"]
		_, hasDomain := idx["domain"]
		if !hasLocal || !hasDomain {
			return nil, errors.New("missing required identity column: \"email\", or both \"local_part\" and \"domain\"")
		}
	}
	if len(idx) > MaxColumnsPerImport {
		return nil, ErrTooManyColumns
	}
	return &columnMap{idx: idx}, nil
}

func (c *columnMap) get(rec []string, key string) (string, bool) {
	i, ok := c.idx[key]
	if !ok || i >= len(rec) {
		return "", false
	}
	return rec[i], true
}

// checkCell enforces the per-cell bound and the formula-injection
// prefix rule uniformly for every column of every row.
func checkCell(v string, rowNum int) error {
	if len(v) > MaxCellLength {
		return fmt.Errorf("%w: row %d", ErrCellTooLong, rowNum)
	}
	if hasFormulaInjectionPrefix(v) {
		return fmt.Errorf("%w: row %d", ErrFormulaInjection, rowNum)
	}
	return nil
}

func buildRawRow(rowNum int, get func(string) (string, bool)) (RawRow, error) {
	row := RawRow{RowNumber: rowNum}

	if email, ok := get("email"); ok {
		if err := checkCell(email, rowNum); err != nil {
			return row, err
		}
		row.Email = strings.TrimSpace(email)
	} else {
		local, _ := get("local_part")
		domain, _ := get("domain")
		if err := checkCell(local, rowNum); err != nil {
			return row, err
		}
		if err := checkCell(domain, rowNum); err != nil {
			return row, err
		}
		local, domain = strings.TrimSpace(local), strings.TrimSpace(domain)
		if local != "" && domain != "" {
			row.Email = local + "@" + domain
		} else if local != "" {
			row.Email = local // domain-less; normalizeEmail rejects this downstream with a clear per-row error
		}
	}

	if name, ok := get("display_name"); ok {
		if err := checkCell(name, rowNum); err != nil {
			return row, err
		}
		row.Name = strings.TrimSpace(name)
	} else if name, ok := get("name"); ok {
		if err := checkCell(name, rowNum); err != nil {
			return row, err
		}
		row.Name = strings.TrimSpace(name)
	}

	if q, ok := get("quota_bytes"); ok {
		if err := checkCell(q, rowNum); err != nil {
			return row, err
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(q), 10, 64); err == nil && v >= 0 {
			row.QuotaMB = v / (1024 * 1024)
		}
	} else if q, ok := get("quota_mb"); ok {
		if err := checkCell(q, rowNum); err != nil {
			return row, err
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(q), 10, 64); err == nil && v >= 0 {
			row.QuotaMB = v
		}
	}

	if am, ok := get("access_mode"); ok {
		if err := checkCell(am, rowNum); err != nil {
			return row, err
		}
		row.AccessMode = AccessMode(strings.ToLower(strings.TrimSpace(am)))
	}

	if ref, ok := get("external_ref"); ok {
		if err := checkCell(ref, rowNum); err != nil {
			return row, err
		}
		row.ExternalRef = strings.TrimSpace(ref)
	}

	// generate_password is accepted but intentionally never read: a
	// mailbox created through bulk import always gets a cryptographically
	// random, immediately-discarded password (see createOneMailbox) —
	// there is no code path by which a value in this column could ever
	// select a caller-supplied password.

	return row, nil
}

// ParseCSV implements the hardened CSV half of the bulk mailbox import
// contract: bounded rows/columns/cell length, duplicate/unknown-header
// rejection, formula-injection rejection on every cell, and UTF-8
// validation of the whole file before any row is read.
func ParseCSV(data []byte) ([]RawRow, error) {
	if len(data) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if !utf8.Valid(data) {
		return nil, ErrInvalidUTF8
	}
	r := csv.NewReader(strings.NewReader(string(data)))
	// Leading-space trimming is deliberately OFF: it would strip a
	// leading tab before the formula-injection check ever saw it,
	// defeating exactly the protection RFC-adjacent spreadsheet tools
	// (and Excel/LibreOffice) actually need — the raw leading byte of
	// each cell, as it appears in the file an operator might reopen, is
	// what a spreadsheet application interprets. Legitimate leading
	// whitespace is still trimmed explicitly, per field, AFTER the
	// injection check has had a chance to see the untouched value.
	r.TrimLeadingSpace = false
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	cols, err := newColumnMap(header)
	if err != nil {
		return nil, err
	}

	var rows []RawRow
	rowNum := 1
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read row %d: %w", rowNum+1, err)
		}
		rowNum++
		if rowNum-1 > MaxRowsPerImport {
			return nil, ErrTooManyRows
		}
		row, err := buildRawRow(rowNum, func(key string) (string, bool) { return cols.get(rec, key) })
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

type jsonRow struct {
	Email   string `json:"email"`
	Name    string `json:"name,omitempty"`
	QuotaMB int64  `json:"quota_mb,omitempty"`
}

// ParseJSON expects a top-level JSON array of {email, name?, quota_mb?}.
// Retained for the pre-existing tenant-session import route; the
// platform contract (Stage 2) is CSV/XLSX only.
func ParseJSON(data []byte) ([]RawRow, error) {
	if len(data) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	var raw []jsonRow
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	if len(raw) > MaxRowsPerImport {
		return nil, ErrTooManyRows
	}
	rows := make([]RawRow, 0, len(raw))
	for i, r := range raw {
		rows = append(rows, RawRow{
			RowNumber: i + 1,
			Email:     strings.TrimSpace(r.Email),
			Name:      strings.TrimSpace(r.Name),
			QuotaMB:   r.QuotaMB,
		})
	}
	return rows, nil
}
