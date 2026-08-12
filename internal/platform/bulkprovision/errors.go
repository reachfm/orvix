package bulkprovision

import "errors"

var (
	ErrJobNotFound       = errors.New("import job not found")
	ErrEmptyFile         = errors.New("import file contains no rows")
	ErrTooManyRows       = errors.New("import file exceeds the maximum row count")
	ErrJobNotReady       = errors.New("job is not in a state that allows execution")
	ErrJobNotCancellable = errors.New("job is not in a state that allows cancellation")
	ErrJobNotRetryable   = errors.New("job has no failed rows to retry")
	ErrVersionConflict   = errors.New("job was modified concurrently")
	ErrUnsupportedFormat = errors.New("unsupported import file format")
)

const MaxRowsPerImport = 5000
