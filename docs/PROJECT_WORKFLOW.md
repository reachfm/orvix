# ORVIX Project Workflow

## Core Rule

**No feature is complete until documentation is updated.** A pull request that changes behavior without a corresponding documentation update is incomplete, regardless of test coverage.

## Whenever code changes, update the relevant subset of:

| Document | Update when... |
|---|---|
| `docs/PROJECT_MAP.md` | A directory's purpose, wiring, or risk level changes; a new top-level module is added or removed |
| `docs/CODEBASE_INDEX.md` | A large file is split; a confirmed defect is fixed (move it from "confirmed defects" to noted-as-resolved, don't delete the entry — annotate it); a new defect is discovered |
| `docs/FEATURE_MATRIX.md` | A feature's status changes (e.g. PARTIAL → COMPLETE); a new feature is started |
| `docs/MASTER_TODO.md` | An item is completed (check it, never delete it) or a new gap is discovered (add it) |
| `docs/ROADMAP.md` | Priority changes, or an Immediate/Next/Future/Enterprise item is completed or added |
| `docs/DECISIONS.md` | Any architectural decision is made — new entry, dated, with Reason/Impact/Alternatives rejected. Never edit or delete a past entry; append a new one that supersedes it if the decision changes |

## Evidence Standard

Every claim in every one of these documents must cite a concrete file path (and line number where practical) or a command that was actually run. "It seems like" or "probably" is not sufficient for a defect entry — either verify it (grep, read the file, run the test) or label it explicitly as "flagged, not confirmed" as several entries in `docs/CODEBASE_INDEX.md` already do.

## Change Review Checklist

Before merging any change:

1. Does it touch a file listed as HIGH risk in `docs/PROJECT_MAP.md`? If so, note the specific risk being mitigated in the PR description.
2. Does it fix an item in `docs/MASTER_TODO.md`? Check it off in the same PR.
3. Does it introduce a new schema-dependent query? Verify the referenced table actually exists (this repo has four confirmed cases where it doesn't — see `docs/CODEBASE_INDEX.md`).
4. Does it touch a tenant-scoped resource? Confirm the handler calls `auth.RequireTenantID` (or equivalent) and every SQL mutation is scoped by `tenant_id`, not just resource `id`. This exact class of bug was found and fixed twice in this codebase's history (mailbox/domain, then alias/group).
5. Does the test call `router.App().Test(req, ...)` (Fiber's test harness)? Always pass `fiber.TestConfig{Timeout: 0}` explicitly. Fiber's default is a hard 1-second deadline with `FailOnTimeout: true` — a login/auth round-trip doing bcrypt/Argon2id hashing can exceed 1s under CPU contention even though it's fast in isolation, and the resulting `os.ErrDeadlineExceeded` will panic (or fail) the test nondeterministically. This exact bug caused `TestRehashOnLogin_ConcurrentPasswordChangeNotOverwritten` to panic during a full-package run while passing every time in isolation; a sibling instance (`tenant_isolation_matrix_test.go`) is still open — see `docs/MASTER_TODO.md`.
6. Run validation scaled to the size of the change — see **Validation Policy** below — before considering the change done.

## Validation Policy

Full-suite validation on every change is wasteful and slows iteration without adding safety once the affected surface is small and well understood. Scale validation to the change:

| Change size | Required validation |
|---|---|
| **Small, targeted fix** (single handler, single bug, single file) | Run the specific new/existing tests for that fix (`go test ./pkg/... -run TestName -v`). No need to run the whole package or suite if the fix is narrowly scoped and the targeted tests pass. |
| **Package-level change** (new handler, schema/migration touching one package, refactor within a package) | Run the full affected package(s): `go test ./internal/api/handlers/... -count=1 -timeout=15m` (adjust timeout to the package's known runtime). Run `go vet ./...` and `go build ./cmd/orvix` — these are cheap and catch cross-package breakage early. |
| **Frontend change** | Build only the touched workspace(s) (`npm run build` in `web/admin`, `web/webmail`, or `web/marketing`). Do not build untouched workspaces. |
| **Before push, merge, release candidate, or any broad schema/architecture change** | Run the full gate: `go mod verify`, `go vet ./...`, `go test ./... -p=1 -count=1 -timeout=60m`, `go build ./cmd/orvix`, and all three frontend builds. This is the only tier that requires the full suite. |

**On full-suite failures:** if a full-suite run reports a failure, do not assume it's flakiness and do not assume it's real — isolate it. Re-run only the specific failing test(s) once, in isolation. If they pass in isolation, the failure was infrastructure flakiness (resource contention, timing) under the full run — report both results honestly and do not silently treat it as a pass, and if it happens twice for the same test, investigate the actual root cause rather than continuing to label it "flakiness" (see below — `TestRehashOnLogin_ConcurrentPasswordChangeNotOverwritten` passed every isolated re-run for several sessions before its real root cause — a missing Fiber `Test()` timeout override — was found and fixed). If they still fail in isolation, it is a real regression tied to the change — fix it, then re-run only what's needed to confirm the fix (see the size-based table above), not necessarily the whole suite again. Never rerun the entire suite speculatively "just to be sure" — only rerun it if a failure was proven caused by this change and a fix was applied, or if the change falls in the "before push/merge/release" tier above.

## Commit Discipline

- One logical change per commit. This session's own history is the model: the Stalwart-era documentation removal (`d5a48cb`) and the customer-mailbox IDOR fix (`9bee80e`) were kept as two separate, independently reviewable commits rather than one large mixed commit, even though both were produced in the same working session.
- Never stage unrelated files. Use explicit paths, not `git add .` / `git add -A`, when a commit must exclude specific in-flight or unrelated changes.
- Never delete production code without first proving it unused (zero import references outside its own package, confirmed via grep across the full repo, not just a spot check).

## Documentation Debt

If a change can't include the documentation update in the same PR (rare, time-pressured exceptions only), it must add a checkbox to `docs/MASTER_TODO.md` under a "Documentation" section noting exactly what's owed. Documentation debt is tracked the same way code debt is — visibly, never silently.

---

*Companion documents: all files in `docs/` this workflow governs. Start at `docs/PROJECT_MAP.md` for orientation.*
