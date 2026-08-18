package bulkprovision

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseCSV_ValidFileParsesAllColumns(t *testing.T) {
	csv := "email,display_name,quota_mb,access_mode,external_ref\n" +
		"a@x.test,Alice,1024,internal_only,ref-1\n"
	rows, err := ParseCSV([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Email != "a@x.test" || r.Name != "Alice" || r.QuotaMB != 1024 || r.AccessMode != AccessInternalOnly || r.ExternalRef != "ref-1" {
		t.Fatalf("unexpected row: %+v", r)
	}
}

func TestParseCSV_LocalPartDomainIdentityForm(t *testing.T) {
	csv := "local_part,domain\nalice,x.test\n"
	rows, err := ParseCSV([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rows[0].Email != "alice@x.test" {
		t.Fatalf("expected composed email, got %q", rows[0].Email)
	}
}

func TestParseCSV_UnknownColumnRejected(t *testing.T) {
	csv := "email,favorite_color\na@x.test,blue\n"
	if _, err := ParseCSV([]byte(csv)); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("expected ErrUnknownColumn, got %v", err)
	}
}

func TestParseCSV_DuplicateHeaderRejected(t *testing.T) {
	csv := "email,email\na@x.test,b@x.test\n"
	if _, err := ParseCSV([]byte(csv)); !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("expected ErrDuplicateHeader, got %v", err)
	}
}

func TestParseCSV_FormulaInjectionRejected(t *testing.T) {
	for _, prefix := range []string{"=", "+", "-", "@", "\t", "\r"} {
		csv := "email,display_name\na@x.test," + prefix + "cmd|'/bin/bash'\n"
		if _, err := ParseCSV([]byte(csv)); !errors.Is(err, ErrFormulaInjection) {
			t.Fatalf("prefix %q: expected ErrFormulaInjection, got %v", prefix, err)
		}
	}
}

func TestParseCSV_FormulaInjectionInIdentityColumnRejected(t *testing.T) {
	csv := "email\n=cmd|'/bin/bash'!A1\n"
	if _, err := ParseCSV([]byte(csv)); !errors.Is(err, ErrFormulaInjection) {
		t.Fatalf("expected ErrFormulaInjection, got %v", err)
	}
}

func TestParseCSV_OversizedUploadRejected(t *testing.T) {
	big := make([]byte, MaxUploadBytes+1)
	if _, err := ParseCSV(big); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("expected ErrUploadTooLarge, got %v", err)
	}
}

func TestParseCSV_ExcessiveRowsRejected(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("email\n")
	for i := 0; i < MaxRowsPerImport+1; i++ {
		sb.WriteString("a@x.test\n")
	}
	if _, err := ParseCSV([]byte(sb.String())); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("expected ErrTooManyRows, got %v", err)
	}
}

func TestParseCSV_ExcessiveCellLengthRejected(t *testing.T) {
	csv := "email,display_name\na@x.test," + strings.Repeat("x", MaxCellLength+1) + "\n"
	if _, err := ParseCSV([]byte(csv)); !errors.Is(err, ErrCellTooLong) {
		t.Fatalf("expected ErrCellTooLong, got %v", err)
	}
}

func TestParseCSV_InvalidUTF8Rejected(t *testing.T) {
	bad := []byte("email\n\xff\xfe@x.test\n")
	if _, err := ParseCSV(bad); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("expected ErrInvalidUTF8, got %v", err)
	}
}

func TestParseCSV_MalformedCSVSurfacesAsError(t *testing.T) {
	bad := "email,name\n\"unterminated"
	if _, err := ParseCSV([]byte(bad)); err == nil {
		t.Fatal("expected an error for malformed CSV")
	}
}

func TestParseCSV_MissingIdentityColumnRejected(t *testing.T) {
	csv := "display_name\nAlice\n"
	if _, err := ParseCSV([]byte(csv)); err == nil {
		t.Fatal("expected an error: no identity column present")
	}
}

func TestParseCSV_BothIdentityFormsRejected(t *testing.T) {
	csv := "email,local_part,domain\na@x.test,a,x.test\n"
	if _, err := ParseCSV([]byte(csv)); err == nil {
		t.Fatal("expected an error: ambiguous identity (both email and local_part+domain)")
	}
}

// ── XLSX ──────────────────────────────────────────────────────────

func buildXLSX(t *testing.T, headers []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			t.Fatalf("set header: %v", err)
		}
	}
	for r, row := range rows {
		for i, v := range row {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return buf.Bytes()
}

func TestParseXLSX_ValidWorkbookParses(t *testing.T) {
	data := buildXLSX(t, []string{"email", "display_name", "quota_mb"}, [][]string{{"a@x.test", "Alice", "512"}})
	rows, err := ParseXLSX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 1 || rows[0].Email != "a@x.test" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestParseXLSX_FormulaCellRejected(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	_ = f.SetCellStr(sheet, "A1", "email")
	_ = f.SetCellStr(sheet, "B1", "display_name")
	_ = f.SetCellStr(sheet, "A2", "a@x.test")
	if err := f.SetCellFormula(sheet, "B2", "=1+1"); err != nil {
		t.Fatalf("set formula: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ParseXLSX(buf.Bytes()); !errors.Is(err, ErrFormulaRejected) {
		t.Fatalf("expected ErrFormulaRejected, got %v", err)
	}
}

func TestParseXLSX_MultipleSheetsRejected(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if _, err := f.NewSheet("Sheet2"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	_ = f.SetCellStr(f.GetSheetName(0), "A1", "email")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ParseXLSX(buf.Bytes()); !errors.Is(err, ErrTooManySheets) {
		t.Fatalf("expected ErrTooManySheets, got %v", err)
	}
}

func TestParseXLSX_UnknownColumnRejected(t *testing.T) {
	data := buildXLSX(t, []string{"email", "favorite_color"}, [][]string{{"a@x.test", "blue"}})
	if _, err := ParseXLSX(data); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("expected ErrUnknownColumn, got %v", err)
	}
}

func TestParseXLSX_FormulaInjectionStringValueRejected(t *testing.T) {
	data := buildXLSX(t, []string{"email", "display_name"}, [][]string{{"a@x.test", "=cmd|'/bin/bash'!A1"}})
	if _, err := ParseXLSX(data); !errors.Is(err, ErrFormulaInjection) {
		t.Fatalf("expected ErrFormulaInjection, got %v", err)
	}
}

func TestParseXLSX_MalformedWorkbookSurfacesAsError(t *testing.T) {
	if _, err := ParseXLSX([]byte("this is not a zip/xlsx file at all")); err == nil {
		t.Fatal("expected an error for a malformed workbook")
	}
}

func TestParseXLSX_OversizedUploadRejected(t *testing.T) {
	big := make([]byte, MaxUploadBytes+1)
	if _, err := ParseXLSX(big); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("expected ErrUploadTooLarge, got %v", err)
	}
}

func TestParseXLSX_MacroEnabledArchiveRejected(t *testing.T) {
	data := buildXLSX(t, []string{"email"}, [][]string{{"a@x.test"}})
	// Splice a vbaProject.bin entry into the archive to simulate a
	// macro-enabled workbook without depending on excelize's own
	// (version-specific) macro-authoring API.
	var buf bytes.Buffer
	zw := newZipWithExtraEntry(t, data, "xl/vbaProject.bin", []byte("fake macro bytes"))
	buf.Write(zw)
	if _, err := ParseXLSX(buf.Bytes()); !errors.Is(err, ErrFormulaRejected) {
		t.Fatalf("expected ErrFormulaRejected for macro-enabled archive, got %v", err)
	}
}

func TestParseXLSX_PathTraversalArchiveEntryRejected(t *testing.T) {
	data := buildXLSX(t, []string{"email"}, [][]string{{"a@x.test"}})
	tampered := newZipWithExtraEntry(t, data, "../../etc/passwd", []byte("x"))
	if _, err := ParseXLSX(tampered); err == nil {
		t.Fatal("expected an error for an archive entry with a path-traversal name")
	}
}

// TestTemplateXLSX_RoundTrips proves the service-generated template
// itself parses cleanly (never a committed binary; generated fresh).
func TestTemplateXLSX_RoundTrips(t *testing.T) {
	data, err := TemplateXLSX()
	if err != nil {
		t.Fatalf("generate template: %v", err)
	}
	rows, err := ParseXLSX(data)
	if err != nil {
		t.Fatalf("parse generated template: %v", err)
	}
	if len(rows) != 1 || rows[0].Email != "jane.doe@example.com" {
		t.Fatalf("unexpected template rows: %+v", rows)
	}
}

func TestTemplateCSV_RoundTrips(t *testing.T) {
	rows, err := ParseCSV(TemplateCSV())
	if err != nil {
		t.Fatalf("parse generated template: %v", err)
	}
	if len(rows) != 1 || rows[0].Email != "jane.doe@example.com" {
		t.Fatalf("unexpected template rows: %+v", rows)
	}
}

// newZipWithExtraEntry rebuilds a ZIP archive with every entry from
// src plus one additional entry, for tests that need to simulate a
// tampered/macro-enabled workbook without depending on excelize's own
// (version-specific) authoring surface for that feature.
func newZipWithExtraEntry(t *testing.T, src []byte, extraName string, extraContent []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("open source zip: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("create entry %s: %v", f.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy entry %s: %v", f.Name, err)
		}
		rc.Close()
	}
	w, err := zw.Create(extraName)
	if err != nil {
		t.Fatalf("create extra entry: %v", err)
	}
	if _, err := w.Write(extraContent); err != nil {
		t.Fatalf("write extra entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}
