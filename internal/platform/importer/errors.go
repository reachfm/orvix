package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type ErrorCode string

const (
	CodeInvalidSource       ErrorCode = "INVALID_SOURCE"
	CodeParseError          ErrorCode = "PARSE_ERROR"
	CodeInvalidUTF8         ErrorCode = "INVALID_UTF8"
	CodeUnknownSchema       ErrorCode = "UNKNOWN_SCHEMA_VERSION"
	CodeUnknownField        ErrorCode = "UNKNOWN_FIELD"
	CodeOversizedInput      ErrorCode = "OVERSIZED_INPUT"
	CodeTooManyRows         ErrorCode = "TOO_MANY_ROWS"
	CodeDuplicateRow        ErrorCode = "DUPLICATE_ROW"
	CodeMissingParent       ErrorCode = "MISSING_PARENT_DEPENDENCY"
	CodeCrossTenant         ErrorCode = "CROSS_TENANT_CONFLICT"
	CodePlatformRoleInj     ErrorCode = "PLATFORM_ROLE_INJECTION"
	CodeQuotaExceeded       ErrorCode = "QUOTA_EXCEEDED"
	CodeInvalidField        ErrorCode = "INVALID_FIELD"
	CodeForbiddenField      ErrorCode = "FORBIDDEN_FIELD"
	CodeExecutionFailed     ErrorCode = "EXECUTION_FAILED"
	CodeCompensationFailed  ErrorCode = "COMPENSATION_FAILED"
	CodeModifiedAfterImport ErrorCode = "MODIFIED_AFTER_IMPORT"
	CodeNotOwnedByImport    ErrorCode = "NOT_OWNED_BY_IMPORT"
	CodeUnsupportedEntity   ErrorCode = "UNSUPPORTED_ENTITY"
	CodeStagingError        ErrorCode = "STAGING_ERROR"
	CodeHashMismatch        ErrorCode = "HASH_MISMATCH"
	CodeCSVFormulaInjection ErrorCode = "CSV_FORMULA_INJECTION"
	CodeJobsUnavailable     ErrorCode = "JOBS_UNAVAILABLE"
	CodeIdempotencyRequired ErrorCode = "IDEMPOTENCY_KEY_REQUIRED"
	CodeCancelled           ErrorCode = "CANCELLED"
)

type ImportError struct {
	Code    ErrorCode         `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Line    int               `json:"line,omitempty"`
	Entity  ImportEntityType  `json:"entity,omitempty"`
	cause   error
}

func (e *ImportError) Error() string {
	if e == nil {
		return ""
	}
	if e.Line > 0 {
		return fmt.Sprintf("%s: line %d: %s", e.Code, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ImportError) Unwrap() error { return e.cause }

func newImportError(code ErrorCode, message string) *ImportError {
	return &ImportError{Code: code, Message: message}
}

func importValidationError(field string, message string) *ImportError {
	return &ImportError{
		Code:    CodeInvalidField,
		Message: message,
		Fields:  map[string]string{field: message},
	}
}

func ToKernelError(err *ImportError) *kernel.Error {
	if err == nil {
		return nil
	}
	msg := err.Message
	switch err.Code {
	case CodeInvalidSource, CodeParseError, CodeInvalidUTF8, CodeUnknownSchema, CodeUnknownField,
		CodeOversizedInput, CodeTooManyRows, CodeDuplicateRow, CodeInvalidField:
		return kernel.ValidationError(err.Fields)
	case CodeMissingParent, CodeCrossTenant, CodePlatformRoleInj:
		return kernel.Forbidden(msg)
	case CodeQuotaExceeded:
		return kernel.QuotaExceeded(msg)
	case CodeHashMismatch, CodeModifiedAfterImport:
		return kernel.NewError(kernel.ErrCodePreconditionFail, msg)
	case CodeExecutionFailed, CodeCompensationFailed, CodeStagingError:
		return kernel.NewError(kernel.ErrCodeInternal, msg)
	default:
		return kernel.NewError(kernel.ErrCodeInternal, msg)
	}
}

func HashSource(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func DetectSourceType(data []byte) (ImportSourceType, error) {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 {
		return "", newImportError(CodeInvalidSource, "empty source data")
	}
	if trimmed[0] == '{' {
		if !json.Valid(data) {
			return "", newImportError(CodeInvalidSource, "invalid JSON")
		}
		return SourceJSON, nil
	}
	if trimmed[0] == '"' {
		return SourceCSV, nil
	}
	return SourceCSV, nil
}

func ValidateSchemaVersion(version int) bool {
	return version == 1
}
