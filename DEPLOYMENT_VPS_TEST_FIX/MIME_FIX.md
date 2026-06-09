# MIME Fix

## Files changed

- `internal/coremail/mime/parser.go`
- `internal/coremail/mime/parser_test.go`

## Fix

`sanitizeFilename` now treats both `/` and `\` as path separators on every OS by normalizing backslashes to forward slashes before extracting the basename.

It also removes null bytes, strips Windows drive prefixes, and rejects basename results of `.` or `..`.

## Regression coverage

`TestSanitizeFilename` now covers:

- `../`
- `..\`
- Unix absolute paths
- Windows drive paths
- nested Unix paths
- nested Windows paths
- null bytes
- bare `.` and `..`

## Verification

```text
go test ./internal/coremail/mime -v
PASS
ok github.com/orvix/orvix/internal/coremail/mime 0.769s
```
