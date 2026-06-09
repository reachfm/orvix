# VPS Test Failure

## Failure 1: MIME filename sanitizer

`internal/coremail/mime.TestSanitizeFilename` failed on Linux for:

```text
sanitizeFilename("dir\\file.txt") = "dir\\file.txt", want "file.txt"
```

Root cause: `sanitizeFilename` used `filepath.Base`, which follows the host operating system path separator rules. On Linux, backslash is not a path separator, so Windows-style nested paths were not reduced to their final filename.

## Failure 2: SMTP package killed during full suite

The VPS reported `internal/coremail/smtp` was killed during `go test ./...`.

Targeted local verification was required before making SMTP changes. Both targeted SMTP runs passed:

```text
go test ./internal/coremail/smtp -v
PASS
ok github.com/orvix/orvix/internal/coremail/smtp 43.891s

go test ./internal/coremail/smtp -run Test -count=1 -v
PASS
ok github.com/orvix/orvix/internal/coremail/smtp 44.601s
```

Conclusion: no SMTP source-code failure was reproduced in isolation. The VPS kill is documented as full-suite resource pressure unless reproduced by a targeted SMTP command.
