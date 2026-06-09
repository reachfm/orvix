# RC1 Technical Debt

Audited commit:

`61fa05595c134ae3fe333f438e6989ca08ffefc4`

Remaining technical debt:

1. Frontend tests certify the current UI, not future feature ambitions.
   - Admin currently has tests for current dashboard/navigation/pages, but not a newly added release-time UI surface.
   - Webmail currently has tests for current shell, mailbox/message/compose/search/action behavior, but no new API or UI features were added for RC1.

2. Integrated load certification is opt-in.
   - Harness requires `ORVIX_RC1_INTEGRATED=1`.
   - This keeps normal test runs practical while preserving exact certification evidence.

3. Race gate requires external Windows C toolchain.
   - Portable w64devkit was used outside the repository.
   - Future reruns must provide GCC, `CGO_ENABLED=1`, `CC`, `COMPILER_PATH`, and `LIBRARY_PATH`.

4. Existing dirty worktree must be controlled before any tagged release.
   - RC1 freeze documentation records the audited state.
   - No tag or release artifact was created in this phase.

5. Production candidate is not a final enterprise readiness claim.
   - Current status is RC1 frozen / production candidate.
   - Continued deployment hardening may still be required before broad enterprise rollout.

Non-negotiable freeze rule:

No new features after RC1 freeze.
