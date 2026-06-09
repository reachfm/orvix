# RC1 Known Limitations

Audited commit:

`61fa05595c134ae3fe333f438e6989ca08ffefc4`

Known limitations:

1. RC1 is a freeze state, not a release publication.
   - No binaries were created.
   - No assets were published.
   - No tag was created.
   - No version bump was performed.

2. Race testing on Windows requires an external GCC toolchain.
   - The successful race run used portable w64devkit outside the repo.
   - A clean machine must reproduce that setup before repeating certification.

3. Integrated certification is not run by default.
   - The exact load harness is gated behind `ORVIX_RC1_INTEGRATED=1`.
   - The certified run passed with 100 mailboxes, 10,004 messages, and 50,000 attachments.

4. Current frontend tests are bounded to current frontend behavior.
   - Phase 13B did not add UI, API, or product features.
   - Any future frontend behavior requires new tests and a new freeze manifest.

5. Worktree contains prior uncommitted/untracked artifacts.
   - This manifest records the audited commit and certification evidence.
   - Release publication should not proceed until repository hygiene is explicitly handled under a separate approved release procedure.

Freeze rule:

No new features after RC1 freeze.
