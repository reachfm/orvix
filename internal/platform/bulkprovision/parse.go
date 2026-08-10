package bulkprovision

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseCSV expects a header row containing at least "email", with
// optional "name" and "quota_mb" columns in any order. Row numbers are
// 1-based and count the header, matching what an operator sees when
// they open the file in a spreadsheet — so an error message like "row
// 3" points at the same row they'd click on.
func ParseCSV(data []byte) ([]RawRow, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	emailIdx, ok := col["email"]
	if !ok {
		return nil, fmt.Errorf("missing required column: email")
	}
	nameIdx, hasName := col["name"]
	quotaIdx, hasQuota := col["quota_mb"]

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
		row := RawRow{RowNumber: rowNum}
		if emailIdx < len(rec) {
			row.Email = strings.TrimSpace(rec[emailIdx])
		}
		if hasName && nameIdx < len(rec) {
			row.Name = strings.TrimSpace(rec[nameIdx])
		}
		if hasQuota && quotaIdx < len(rec) {
			if v, err := strconv.ParseInt(strings.TrimSpace(rec[quotaIdx]), 10, 64); err == nil {
				row.QuotaMB = v
			}
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
func ParseJSON(data []byte) ([]RawRow, error) {
	var raw []jsonRow
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
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
