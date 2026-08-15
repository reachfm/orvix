# orvix-webmail (Vite/React) — EXPERIMENTAL, NOT THE RELEASE SOURCE

**This tree does not ship.** It is not wired into
`release/scripts/build-release-bundle.sh`, has no production
provenance, and cannot send mail today — `ComposeModal`'s Send button
has no `onClick` handler.

The canonical, deployed webmail source is
[`web/webmail-release`](../webmail-release) (hand-authored, packaged
verbatim by the release build). See the provenance comment next to the
webmail packaging step in `release/scripts/build-release-bundle.sh`
and the guard tests in
`internal/api/handlers/webmail_source_provenance_test.go`.

This tree is built and typechecked in CI
(`.github/workflows/postgres-readiness.yml`, "Webmail frontend
typecheck and build") to keep it from bit-rotting, since it's the
intended eventual replacement — but treat anything here as a work in
progress, not a source of truth for current webmail behavior.

Before this tree can become canonical:

- wire up Send (and verify it against the CSRF contract the current
  canonical source enforces — one retry only on an unambiguous
  pre-handler CSRF 403, never on ordinary 403/429/5xx/timeout);
- reach full mutation parity: drafts, flags, move, delete, archive,
  settings, push, batch;
- update the provenance comment in `build-release-bundle.sh` and the
  guard tests in the same commit that flips the switch.
