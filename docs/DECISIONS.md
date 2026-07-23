# ORVIX Architecture Decisions

Permanent engineering history. Entries are never deleted, only appended or annotated as superseded.

---

### 2026-07-23 — Delete `release/admin/CHANGELOG.md`

**Decision:** Confirm and finalize the deletion of `release/admin/CHANGELOG.md` (489 lines, legacy vanilla-JS admin console changelog) as part of the documentation-cleanup commit.

**Reason:** Verified via `grep -rln "CHANGELOG.md" release/scripts/*.sh Makefile` and a repo-wide grep for `release/admin/CHANGELOG` across `*.sh`/`*.go`/`Makefile` — zero references in any build, runtime, or release script. Not loaded by `release/admin/index.html` (which loads only the Vite SPA bundle). Not part of the intentional minimal-toolchain fallback (`release/admin/modules/`, `release/admin/app.js`, which *are* referenced by `release/scripts/lib-admin-build.sh` and were explicitly preserved). Purely historical release notes for a superseded admin-console generation.

**Impact:** 489 lines removed from tracked docs. No build, test, or runtime behavior affected — confirmed by a full green test suite and all three frontend builds both before and after this deletion was first made.

**Alternatives rejected:** Restoring the file — rejected because no evidence of current use exists; restoring a proven-dead file would just re-introduce noise without informational value (its content is fully preserved in git history if ever needed).

---

### 2026-06-06 (approx.) — Move from Stalwart-embedded mail engine to native `internal/coremail`

**Decision:** Replace the original architecture (Orvix as an orchestration layer around an embedded/managed Stalwart Mail Server subprocess) with a fully native Go mail engine (`internal/coremail`) implementing SMTP, Submission, IMAP, POP3, JMAP, DKIM/SPF/DMARC, queue, and delivery directly.

**Reason:** Not recorded in available project history — inferred from the fact that the entire original architecture (documented in the now-deleted `MVP.md`, `HANDOFF.md`, `AGENT_INSTRUCTIONS.md`, etc.) assumed Stalwart as the mail engine, while the current, actively-developed codebase has zero Stalwart runtime code and a mature native engine instead.

**Impact:** All mail-protocol code lives under `internal/coremail/`. No external mail-server dependency, no subprocess management, no REST-API/webhook bridge to a third party.

**Alternatives rejected:** Continuing to wrap Stalwart (evidenced by the parallel, unrelated `D:\Orvix Enterprise Mail` repository outside this workspace's scope, which still contains a full Stalwart integration on a different module path — treated as an abandoned parallel branch, not merged).

---

### 2026-07-23 — Stalwart zero-tolerance documentation sweep

**Decision:** Delete 14 documentation files whose entire subject matter was the obsolete Stalwart-embedded architecture (`MVP.md`, `HANDOFF.md`, `AGENT_INSTRUCTIONS.md`, `WORK_CONTEXT.md`, `VPS_TEST_PLAN.md`, `ROADMAP.md`, `RELEASE_AUDIT.md`, `PROGRESS.md`, `ORVIX_STATUS_MATRIX.md`, `ORVIX_INDEPENDENT_AUDIT.md`, `FULL_FEATURE_PLAN.md`, `AUDIT.md`, `release/RELEASE_NOTES.md`, `docs/RC4_RELEASE_ACCEPTANCE_GATE.md`). Rename `docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md` → `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md` and scrub its Stalwart branding, since it is a current, active competitive-benchmark document, not legacy architecture documentation. Scrub comment-only Stalwart references from 10 source/script files. Explicitly preserve two protective regression tests (`installer_test.go`, `release_packaging_test.go`) that assert Stalwart is absent.

**Reason:** These 14 files could not be scrubbed of the word "Stalwart" without gutting their entire subject — they describe an architecture that no longer exists and are actively misleading (e.g. `MVP.md`'s premise was "Built on Stalwart Mail Server"). Six weeks stale (single-commit batch, 2026-06-06) relative to ongoing development.

**Impact:** 2972 lines removed from tracked documentation. Zero production code touched. Verified via full test suite (80 packages, 0 failures) and all three frontend builds passing after the change.

**Alternatives rejected:** Scrubbing the word in-place instead of deleting — rejected because it would have produced incoherent documents (e.g. `MVP.md` with "Stalwart" replaced by a placeholder throughout its 896 lines reads as nonsense, since the whole document's structure depends on that premise).

---

### 2026-07-23 — Tenant-authorization fix scoped strictly to `customer_mail.go`, not the underlying `requireTenantActive` bug

**Decision:** Fix the confirmed cross-tenant IDOR in `DeleteAlias`/`DeleteGroup`/`AddGroupMember`/`RemoveGroupMember` (`internal/api/handlers/customer_mail.go`) by adding `auth.RequireTenantID` checks and tenant-scoped SQL, without also fixing the separately-discovered bug where `requireTenantActive` (`router.go:890`) queries a nonexistent `organizations` table.

**Reason:** The two bugs are independent. Fixing `requireTenantActive` requires either a schema migration (creating `organizations`) or a router.go change (repointing the query at `tenants`) — both are out of scope for a change explicitly authorized as "only `customer_mail.go` and its directly relevant tests." Mixing them into one commit would have made the fix harder to review and revert independently.

**Impact:** The alias/group tenant-authorization fix is real and tested (12 new tests, verified to fail against the pre-fix code), but cannot currently be exercised end-to-end through the live HTTP router, because `requireTenantActive` rejects/panics on every `/enterprise` mutation regardless of this fix. Tests were therefore written at the handler level, bypassing the full router, with two test-local-only tables (`coremail_groups`, `coremail_group_members`) created since neither exists in production schema either.

**Alternatives rejected:** Fixing `requireTenantActive` in the same commit — rejected per the explicit scope boundary set for that task. Recorded here as the next required fix, tracked in `docs/MASTER_TODO.md`.

---

### 2026-07-23 — Documentation audit files (`.cleanup-audit/`, `.audit-readonly/`, `.drive-audit-readonly/`, `.project-baseline/`) kept untracked and out of the Stalwart zero-tolerance sweep

**Decision:** Treat this session's own audit-trail scratch directories as out of scope for the Stalwart-reference sweep, even though they contain historical mentions of "Stalwart" in their own narrative text.

**Reason:** These directories are meta-audit output describing the investigation itself (e.g. "confirmed zero Stalwart references remain"), not product documentation. They are untracked and were never part of the shipped codebase.

**Impact:** None on production code or shipped documentation. Noted for transparency in every relevant report produced this session.

**Alternatives rejected:** Scrubbing these too — considered unnecessary since they're not tracked, not shipped, and removing "Stalwart" from a sentence like "confirmed zero Stalwart references remain" would make the audit trail itself unreadable.

---

*Companion documents: `docs/PROJECT_MAP.md`, `docs/CODEBASE_INDEX.md`, `docs/MASTER_TODO.md`, `docs/ROADMAP.md`.*
