package bulkprovision

import "errors"

var (
	ErrJobNotFound           = errors.New("import job not found")
	ErrEmptyFile             = errors.New("import file contains no rows")
	ErrTooManyRows           = errors.New("import file exceeds the maximum row count")
	ErrTooManyColumns        = errors.New("import file exceeds the maximum column count")
	ErrCellTooLong           = errors.New("import file contains a cell that exceeds the maximum length")
	ErrTooManySheets         = errors.New("workbook contains more than one data sheet")
	ErrDuplicateHeader       = errors.New("import file contains a duplicate column header")
	ErrUnknownColumn         = errors.New("import file contains an unrecognized or ambiguous identity column")
	ErrFormulaRejected       = errors.New("import file contains a formula, which is not permitted")
	ErrFormulaInjection      = errors.New("import file contains a value with a formula-injection prefix")
	ErrInvalidUTF8           = errors.New("import file is not valid UTF-8")
	ErrJobNotReady           = errors.New("job is not in a state that allows execution")
	ErrJobNotCancellable     = errors.New("job is not in a state that allows cancellation")
	ErrJobNotRetryable       = errors.New("job has no failed rows to retry")
	ErrVersionConflict       = errors.New("job was modified concurrently")
	ErrUnsupportedFormat     = errors.New("unsupported import file format")
	ErrInvalidAccessMode     = errors.New("row specifies an unsupported mail access mode")
	ErrInvalidConflictPolicy = errors.New("unsupported conflict policy")
	ErrSourceHashMismatch    = errors.New("import source has changed since validation; revalidate before executing")
	ErrUploadTooLarge        = errors.New("uploaded file exceeds the maximum allowed size")
)

const (
	MaxRowsPerImport    = 5000
	MaxUploadBytes      = 8 << 20 // 8 MiB
	MaxColumnsPerImport = 32
	MaxCellLength       = 512
	MaxSheets           = 1
	// SchemaVersion is the current validation/template contract
	// version. A future incompatible template change increments this;
	// CreateJob binds each job to the version its validation ran under.
	SchemaVersion = 1
)
