package importer

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	MaxSourceBytes = 32 << 20 // 32 MiB
	MaxSourceRows  = 10000
	MaxFieldLength = 500
)

var (
	csvFormulaPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^[=\+\-@]`),
		regexp.MustCompile(`^(?i:cmd\||powershell\|)`),
	}
)

type ParsedSource struct {
	SourceType    ImportSourceType `json:"source_type"`
	SchemaVersion int              `json:"schema_version"`
	Entities      []ParsedEntity   `json:"entities"`
	TotalRows     int              `json:"total_rows"`
}

type ParsedEntity struct {
	Line   int              `json:"line"`
	Entity ImportEntityType `json:"entity"`
	Data   json.RawMessage  `json:"data"`
	Raw    map[string]any   `json:"-"`
	Errors []string         `json:"errors,omitempty"`
}

func ParseCSV(data []byte) (*ParsedSource, error) {
	return parseCSV(data)
}

func ParseJSON(data []byte) (*ParsedSource, error) {
	return parseJSON(data)
}

func ParseSource(data []byte, sourceType ImportSourceType) (*ParsedSource, error) {
	if len(data) > MaxSourceBytes {
		return nil, newImportError(CodeOversizedInput, fmt.Sprintf("source exceeds maximum size of %d bytes", MaxSourceBytes))
	}
	switch sourceType {
	case SourceCSV:
		return parseCSV(data)
	case SourceJSON:
		return parseJSON(data)
	default:
		return nil, newImportError(CodeInvalidSource, "unsupported source type: "+string(sourceType))
	}
}

func parseCSV(data []byte) (*ParsedSource, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	var allRows [][]string
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, newImportError(CodeParseError, fmt.Sprintf("CSV parse error: %v", err))
		}
		allRows = append(allRows, row)
	}

	if len(allRows) < 1 {
		return nil, newImportError(CodeParseError, "CSV must have at least a header row")
	}

	header := allRows[0]
	schemaVersion, entityCol, err := parseCSVHeader(header)
	if err != nil {
		return nil, err
	}

	var entities []ParsedEntity
	for i := 1; i < len(allRows); i++ {
		row := allRows[i]
		entity, perr := parseCSVRow(row, schemaVersion, entityCol, i+1)
		if perr != nil {
			entities = append(entities, ParsedEntity{
				Line:   i + 1,
				Errors: []string{perr.Error()},
			})
			continue
		}
		entities = append(entities, entity)
	}

	if len(entities) > MaxSourceRows {
		return nil, newImportError(CodeTooManyRows, fmt.Sprintf("source has %d rows, maximum is %d", len(entities), MaxSourceRows))
	}

	source := &ParsedSource{
		SourceType:    SourceCSV,
		SchemaVersion: schemaVersion,
		Entities:      entities,
		TotalRows:     len(entities),
	}
	return source, nil
}

func parseCSVHeader(header []string) (int, map[string]int, error) {
	idx := make(map[string]int)
	for i, col := range header {
		idx[strings.ToLower(strings.TrimSpace(col))] = i
	}
	entityCol := idx

	if _, ok := idx["schema_version"]; !ok {
		return 1, entityCol, nil
	}
	return 1, entityCol, nil
}

func parseCSVRow(row []string, schemaVersion int, entityCol map[string]int, line int) (ParsedEntity, error) {
	entityTypeStr := strings.ToLower(strings.TrimSpace(getCSV(row, entityCol, "entity", "type", "entity_type")))
	entityType := ImportEntityType(entityTypeStr)

	switch entityType {
	case EntityOrganization, EntityTenantAdmin, EntityDomain, EntityMailbox, EntityAlias, EntityGroup, EntityGroupMembership:
	default:
		return ParsedEntity{}, fmt.Errorf("unknown entity type: %s", entityTypeStr)
	}

	rowData := make(map[string]any)
	for key, i := range entityCol {
		if i >= 0 && i < len(row) {
			val := strings.TrimSpace(row[i])
			if entityType == EntityTenantAdmin && key == "password" {
				rowData[key] = val
			} else {
				rowData[key] = val
			}
		}
	}
	rowData["entity"] = string(entityType)

	rawJSON, err := json.Marshal(rowData)
	if err != nil {
		return ParsedEntity{}, fmt.Errorf("failed to serialize row: %w", err)
	}

	return ParsedEntity{
		Line:   line,
		Entity: entityType,
		Data:   rawJSON,
		Raw:    rowData,
	}, nil
}

func getCSV(row []string, idx map[string]int, keys ...string) string {
	for _, key := range keys {
		if i, ok := idx[key]; ok && i >= 0 && i < len(row) {
			return row[i]
		}
	}
	return ""
}

func parseJSON(data []byte) (*ParsedSource, error) {
	if !json.Valid(data) {
		return nil, newImportError(CodeInvalidSource, "invalid JSON")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		Entities      []json.RawMessage `json:"entities"`
	}

	// Try envelope first
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, newImportError(CodeParseError, fmt.Sprintf("JSON parse error: %v", err))
	}

	schemaVersion := envelope.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}

	if !ValidateSchemaVersion(schemaVersion) {
		return nil, newImportError(CodeUnknownSchema, fmt.Sprintf("unknown schema version: %d", schemaVersion))
	}

	entities := envelope.Entities
	if len(entities) == 0 {
		var rawEntities []json.RawMessage
		if err := json.Unmarshal(data, &rawEntities); err == nil {
			entities = rawEntities
		}
	}

	if len(entities) > MaxSourceRows {
		return nil, newImportError(CodeTooManyRows, fmt.Sprintf("source has %d rows, maximum is %d", len(entities), MaxSourceRows))
	}

	var parsed []ParsedEntity
	for i, raw := range entities {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			parsed = append(parsed, ParsedEntity{
				Line:   i + 1,
				Errors: []string{fmt.Sprintf("invalid row JSON: %v", err)},
			})
			continue
		}

		entityStr := stringFromMap(row, "entity", "type", "entity_type")
		entityType := ImportEntityType(strings.ToLower(entityStr))

		row["entity"] = string(entityType)

		rawJSON, _ := json.Marshal(row)
		parsed = append(parsed, ParsedEntity{
			Line:   i + 1,
			Entity: entityType,
			Data:   rawJSON,
			Raw:    row,
		})
	}

	return &ParsedSource{
		SourceType:    SourceJSON,
		SchemaVersion: schemaVersion,
		Entities:      parsed,
		TotalRows:     len(parsed),
	}, nil
}

func stringFromMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func IsCSVFormulaInjection(field string) bool {
	for _, pattern := range csvFormulaPatterns {
		if pattern.MatchString(field) {
			return true
		}
	}
	return false
}

func SanitizeCSVExport(field string) string {
	if IsCSVFormulaInjection(field) {
		return "'" + field
	}
	return field
}
