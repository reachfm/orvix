package bulkprovision

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/xuri/excelize/v2"
)

// inspectXLSXArchive opens the upload as a raw ZIP archive (an XLSX
// file IS a ZIP archive) BEFORE excelize ever parses it, and rejects:
//   - a macro-enabled workbook (an xl/vbaProject.bin entry present —
//     the on-disk signature of .xlsm/.xlsb content regardless of the
//     extension the client claims);
//   - any entry whose name is absolute or contains ".." — the upload
//     must never be able to name a path outside the archive's own
//     namespace, defense in depth even though this package never
//     extracts entries to disk itself.
func inspectXLSXArchive(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	for _, f := range zr.File {
		name := f.Name
		if path.IsAbs(name) || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("%w: unsafe archive entry path", ErrUnsupportedFormat)
		}
		if name == "xl/vbaProject.bin" {
			return ErrFormulaRejected
		}
	}
	return nil
}

// ParseXLSX implements the hardened XLSX half of the bulk mailbox
// import contract. Bounds mirror ParseCSV: bounded upload size, a
// single accepted data sheet, bounded rows/columns/cell length,
// duplicate/unknown-header rejection, and formula-injection rejection
// on every cell. Additionally, and unlike CSV, XLSX cells can carry an
// actual FORMULA (not just a string that looks like one) — any cell
// with a formula is rejected outright, the whole file, not just that
// row: a workbook that needed a formula to produce its values is not
// a data file this importer accepts.
func ParseXLSX(data []byte) ([]RawRow, error) {
	if len(data) > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if err := inspectXLSXArchive(data); err != nil {
		return nil, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open workbook: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrEmptyFile
	}
	if len(sheets) > MaxSheets {
		return nil, ErrTooManySheets
	}
	sheet := sheets[0]

	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrEmptyFile
	}
	if len(rows)-1 > MaxRowsPerImport {
		return nil, ErrTooManyRows
	}

	cols, err := newColumnMap(rows[0])
	if err != nil {
		return nil, err
	}

	var out []RawRow
	for i := 1; i < len(rows); i++ {
		rowNum := i + 1 // 1-based, header counted, matches ParseCSV's numbering
		rec := rows[i]
		for col := 0; col < len(rec); col++ {
			cellRef, cerr := excelize.CoordinatesToCellName(col+1, i+1)
			if cerr != nil {
				continue
			}
			if formula, ferr := f.GetCellFormula(sheet, cellRef); ferr == nil && formula != "" {
				return nil, fmt.Errorf("%w: row %d", ErrFormulaRejected, rowNum)
			}
		}
		row, err := buildRawRow(rowNum, func(key string) (string, bool) { return cols.get(rec, key) })
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// TemplateXLSX generates the canonical bulk-mailbox-import template as
// XLSX bytes, ON DEMAND — never a binary file committed to the
// repository. One header row, one commented example row (as literal
// text, never a formula), matching exactly the columns newColumnMap
// accepts.
func TemplateXLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	headers := []string{"email", "display_name", "quota_mb", "access_mode", "generate_password", "external_ref"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			return nil, err
		}
	}
	example := []string{"jane.doe@example.com", "Jane Doe", "2048", "inherit", "true", "hr-import-row-1"}
	for i, v := range example {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		if err := f.SetCellStr(sheet, cell, v); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TemplateCSV generates the equivalent CSV template.
func TemplateCSV() []byte {
	return []byte("email,display_name,quota_mb,access_mode,generate_password,external_ref\n" +
		"jane.doe@example.com,Jane Doe,2048,inherit,true,hr-import-row-1\n")
}
