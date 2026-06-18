# Webmail Enterprise Feature Pack 2 — Changelog

Branch: `feature/webmail-enterprise-2`
Base: `acacfef` ("Add outbound IPv4 preference")

## What's new

### A. Message list experience
- **Pagination honoured**: `?limit=` (1..200, default 50)
  and `?offset=` query parameters are now respected by
  `/api/v1/webmail/messages`. The "Load more" button is
  wired to a real click handler with a spinner state.
- **Attachment indicator**: each list row reports
  `attachment_count` (single SQL roundtrip, batch
  counted).
- **Search snippet**: when `?q=` is supplied the
  response includes a `snippet` field for each row,
  centred on the first match in the body. The body is
  extracted from the RFC822 headers before the snippet
  is generated so it never surfaces
  `Message-ID: <…>`-style lines that look like HTML.
- **`has_more` flag**: the list response now returns
  `has_more` so the client knows whether another page
  is available.

### B. Message reading experience
- **Attachment list** in the reading pane is now
  real: every row shows filename, size, and two
  buttons — Download (the safe endpoint
  `/attachments/:id`) and Preview for safe content
  types (PNG / JPEG / GIF / WebP / text).
- **Source download**: the new "Show original" action
  downloads the raw RFC822 as `message-<id>.eml`.
- **Move to folder**: a small `<select>` in the
  reading pane toolbar moves the current message to
  any folder in the caller's mailbox.
- **Spam / Not spam**: a single-message spam toggle
  uses the new batch endpoint with `action=spam` or
  `nospam`.
- **Mark as read on open** continues to work (best
  effort).

### C. Compose experience
- **Reply-to honor**: if the original message has a
  `Reply-To` header that differs from `From`, the
  compose modal uses it. The previous client always
  replied to `From`, which is wrong for mailing lists
  and most support systems.
- **Reply-all deduplication**: cross-listed recipients
  are no longer duplicated between To and Cc; the
  computed Cc set is the symmetric difference of
  (To ∪ Cc) − {self} − To. The order is preserved and
  the Cc list never contains the original sender.
- **Send button disables while sending** and re-enables
  on success or failure.
- **Failed send keeps content** (no modal close on
  error).
- **Outgoing attachment placeholder**: the compose
  modal does NOT expose an attach button. The forward
  banner explicitly tells the user that original
  attachments are not included in the forward and that
  attachment forwarding is "coming soon".
- **No user-controlled From**: the From header is
  always `ctx.Mailbox.Email` — the authenticated
  mailbox, never anything the client supplies.

### D. Reply / Reply-all / Forward
- Reply: To = Reply-To (or From), subject = `Re: <subject>`,
  body = quoted original.
- Reply-all: To = Reply-To (or From), Cc = symmetric
  difference of (To ∪ Cc) − {self} − To.
- Forward: subject = `Fwd: <subject>`, body = forwarded
  banner + original body. The banner adds a plain-text
  note when the original message had attachments, so
  the user is not surprised that the forward arrives
  without files.

### E. Draft autosave
- 3-second debounced autosave after the last input.
- Empty drafts are NOT autosaved (would clutter
  Drafts).
- Manual "Save draft" and autosave share the same code
  path; the timer is cleared on manual save.
- The status line in the modal footer shows
  `Saving…` / `Saved Ns ago` / `Save failed: <msg>`.
- Sending a draft deletes it on the server, so the
  Drafts folder does not keep stale copies after send.

### F. Attachments foundation
- **Incoming**: real attachment list, download,
  preview. The download endpoint enforces ownership
  (404 to non-owners, never 403) and serves the file
  with `Content-Disposition: attachment`.
- **Preview**: allowlist of safe content types, 1 MB
  cap, SVG refused.
- **Outgoing**: intentionally not implemented. UI does
  not pretend sending worked. See SECURITY_MODEL §10.

### G. Folder actions
- Archive, delete, mark read/unread, star/unstar,
  move-to, spam/nospam. Folder list is unchanged.

### H. Bulk actions
- Multi-select via per-row checkbox.
- "Select" toggle in the list header to enter
  selection mode without ticking any message.
- "Select all visible" ticks every loaded row.
- Bulk action bar with: Archive, Delete, Mark read,
  Mark unread, Spam, Move to…, Clear.
- Partial failure reported as `{succeeded, failed,
  errors:[{id, error}]}` — no silent loss.
- Cross-mailbox ids are reported as per-id failures
  (404 shape), not as a 403 on the whole batch.

### I. Search and filtering
- Search input debounced 250 ms (existing).
- Subject / from / to LIKE matching (default scope).
- `?body=1` opt-in extends the match to the message
  body — the handler loads the RFC822 from disk and
  runs the substring match.
- `?total=1` opt-in returns the total count for the
  filtered query. Off by default so the default list
  request does not pay for an extra COUNT(*).

### J. Mobile / responsive
- Four explicit breakpoints: 1440 (large), 1024 (small
  laptop), 768 (tablet), 390 (compact phone).
- The compose modal becomes near-full-screen at 768
  and below so the editor is comfortable with a soft
  keyboard.
- The bulk action bar shrinks to single-row at 390.
- Font size on form inputs is 16 px at 390 to defeat
  iOS auto-zoom on focus.

### K. Arabic / RTL
- `dir="auto"` on every dynamic text node (subject,
  from, preview, body, header values).
- Logical CSS properties: `margin-inline-*`,
  `padding-inline-*`, `inset-inline-*`,
  `border-inline-*` are used in the new code paths.
- The existing `html[dir="rtl"]` rules remain in
  place; no physical-property regressions.

### L. Security hardening
- See `docs/SECURITY_MODEL.md` for the full posture.
  Highlights:
  - No `localStorage` / `sessionStorage` use anywhere
    in the webmail client.
  - No `/api/v1/queue` references in the webmail
    client.
  - Cross-mailbox IDs return 404 (not 403) to avoid
    leaking existence.
  - CRLF-stripped subject and To/Cc/Bcc.
  - Strict CSP (script-src 'self', frame-src 'none',
    object-src 'none') on every response.
  - SameSite=None+Secure cookies as the primary CSRF
    defence for the webmail state-changing endpoints;
    explicit CSRF tokens only on the auth-state
    endpoints.

### M. Tests and release verification
- New Go tests for every new endpoint, every new
  ownership check, every ownership violation case.
- New storage tests for the new
  `MessageFilter.Search*` fields and the new
  `AttachmentRepository.CountByMessages` /
  `GetByMessageAndID` methods.
- New frontend static-analysis tests for:
  - `localStorage` / `sessionStorage` absence
  - `prefers-reduced-motion` media query in CSS
  - 1440 / 1024 / 768 / 390 breakpoints
  - logical-property sweep
  - keyboard shortcut key bindings
  - new endpoint references

## Files changed

### Backend (Go)
- `internal/coremail/storage/types.go` — added
  `SearchSubject / SearchFrom / SearchTo / SearchCc /
  SearchBcc / SearchBody` to `MessageFilter` and the
  `MatchScopeForSearch()` helper.
- `internal/coremail/storage/message.go` — extended
  the SQL `WHERE` clause to honour the per-field
  scope flags.
- `internal/coremail/storage/attachments.go` — added
  `CountByMessages` and `GetByMessageAndID` to the
  repository interface.
- `internal/api/handlers/webmail_user.go` — rewrote
  `WebmailMessages` to honor pagination / snippet /
  attachment count; updated `WebmailMessage` to
  return the attachments list; added
  `WebmailMessageSource`, `WebmailMoveMessage`, and
  `WebmailMessageBatch`.
- `internal/api/handlers/webmail_attachment.go`
  (new) — `WebmailAttachmentDownload` and
  `WebmailAttachmentPreview` with strict content-type
  allowlist and ownership check.
- `internal/api/router.go` — wired the new routes on
  the existing `protected` group.

### Frontend (vanilla JS / CSS)
- `release/webmail/assets/webmail.js` — bulk select
  model, draft autosave (3s debounce), keyboard
  shortcuts (`j`/`k`/`s`/`e`/`#`/`Delete`/g-prefix
  for `g i` / `g s` / `g d` / `g a` / `g t` / `g j` /
  `?`), reply-to honor, reply-all dedup, show-original
  anchor, move-to inline select, attachment download
  / preview anchors, keyboard shortcuts overlay.
- `release/webmail/assets/webmail.css` — bulk action
  bar, list header, row checkbox column, load-more
  button, attachment download / preview buttons, move
  to select styling, autosave status line, skeleton
  loaders, shortcuts overlay, full responsive
  breakpoint sweep (1440 / 1024 / 768 / 390),
  `prefers-reduced-motion` rule.

### Tests
- `internal/coremail/storage/webmail_enterprise2_test.go`
  (new) — 5 new tests for the storage extensions.
- `internal/api/handlers/webmail_enterprise2_test.go`
  (new) — 19 new tests for the new handler
  endpoints.
- `internal/api/handlers/webmail_frontend_test.go` —
  6 new static-analysis tests for the client-side
  rules.

### Docs
- `docs/SECURITY_MODEL.md` (new) — the canonical
  reference for the webmail security posture.
- `release/webmail/CHANGELOG.md` (this file, new) —
  release notes for the feature pack.

## Known limitations

- **Outgoing attachments** are not implemented. The
  compose modal does not show an "Attach" button and
  the forward banner is honest about that.
- **HTML compose** is not implemented; the body is
  plain text only.
- **No real-time push**. The list refreshes on user
  action.
- **No thread / conversation view** yet.
- **No "Always show images from this sender"
  toggle** — external images in HTML email load on
  the user's network and are subject to the CSP
  `img-src 'self' data: https:` rule.
- **No server-side full-text search index** — the
  `?body=1` search reads every matched file from
  disk, which is fine for a single-page UI but does
  not scale beyond a few thousand messages.
