# Webmail UI Polish 3 — Changelog

Branch: `feature/webmail-ui-polish-3`
Base: `08e380b` ("Merge Webmail Enterprise Feature Pack 2")

## What's new

A premium UI polish pack layered on top of Feature
Pack 2. No backend behavior changes, no new endpoints,
no removal of existing features. Every change is
visible-only: a denser, more refined design system;
richer empty / loading / error states; a polished
compose modal; an opt-in light theme; and a
consistent focus-ring system across the entire shell.

### A. Design system

- **Design tokens** — every visible color, radius,
  shadow, spacing, and motion timing is a CSS custom
  property on `:root`. Components reference the tokens,
  not raw hex. This makes the dark and light themes a
  one-class swap.
- **Semantic background tiers** — `bg-app`,
  `bg-canvas`, `bg-surface`, `bg-elevated`,
  `bg-raised`, `bg-hover`, `bg-active`,
  `bg-selected`, `bg-selected-2`. The layered
  surfaces give the shell real depth instead of a
  flat dark slab.
- **Multi-tier shadows** — `shadow-xs`, `shadow-sm`,
  `shadow-md`, `shadow-lg`, `shadow-xl`,
  `shadow-focus`. The shadow that lifts a modal is
  distinct from the shadow on a hovered message row,
  so the elevation hierarchy reads at a glance.
- **Spacing scale** — `sp-1` … `sp-10` (4 px → 56 px).
  Every padding, margin, and gap resolves to a token
  so vertical rhythm stays consistent across the app.
- **Radius scale** — `r-sm`, `r-md`, `r-lg`, `r-xl`,
  `r-pill`. The compose modal is the largest radius;
  the chips are pill; everything else lives in
  between.
- **Motion timings** — `motion-fast` (120 ms),
  `motion-med` (200 ms), `motion-slow` (320 ms) with
  a single shared easing curve. The reduced-motion
  media query zeroes every animation in one block.

### B. Light theme (opt-in)

- `:root.theme-light` overrides the design tokens to
  a clean professional light palette: cool-white
  canvas, blue accent, proper contrast for body text.
- The webmail client detects `prefers-color-scheme:
  light` and applies the `theme-light` class on
  `<html>` automatically. Embedders that want a
  forced skin can apply the class on the embedding
  page before webmail.js loads.
- The webmail UI never writes to localStorage or
  sessionStorage — the OS preference is the single
  source of truth, so there is no per-user toggle to
  persist.
- The auth gate adopts the same theme tokens so the
  login screen transitions seamlessly into the shell.

### C. Sidebar polish

- **Subtle radial gradient** on the body backdrop
  (top-anchored accent glow) so the dark theme feels
  like a SaaS product, not a flat color slab.
- **Active folder row** now carries a left-side
  accent bar, a gradient background, and a
  pill-shaped count badge in the accent color.
- **Hover state** is more refined — proper transition
  timing, no jarring color jumps.
- **Footer "dot"** indicator for connection status
  (green pulse) replaced the bare version text.
- **Compose button** has a stronger elevation
  (shadow + inset highlight) and a more prominent
  treatment.

### D. Top bar polish

- **Search field** is now a true pill with a
  generous icon gutter, focus glow, and hover state.
- **User chip** uses the brand gradient avatar with
  inset highlight.
- **Brand mark** uses the same gradient the auth
  gate uses, so the two screens feel like one
  product.

### E. Message list polish

- **Premium row hover** — message rows have a
  subtle transition into a slightly elevated
  surface tone.
- **Stronger selected state** — the selected row
  carries an inset accent bar on the leading edge
  (logical property; mirrors correctly in RTL).
- **Star button** has a hover scale-up and warning-
  tinted background.
- **Attachment indicator** uses a paperclip glyph
  with refined spacing.
- **Checked (bulk-selected)** rows have the same
  inset accent treatment as the single-selected row.

### F. Reading pane polish

- **Larger avatar** with brand gradient + inset
  highlight.
- **Subject heading** is 22 px (24 px on > 1440 px
  displays) with negative letter-spacing for a
  typographic feel.
- **From block** uses two-line layout (name + email)
  with `direction: ltr` / `unicode-bidi: isolate` on
  the email so it never breaks inside an Arabic
  paragraph.
- **Details rows** use uppercase 11 px labels with
  tabular numeric spacing.
- **Body** uses a max-width (880 px) so long lines
  stay readable.
- **Blockquote** uses a logical border-inline-start
  with a subtle background.
- **Attachment cards** have their own row, icon
  block, name, size, and download/preview actions
  styled with the design tokens.
- **Empty state** — when no message is selected the
  pane now shows a circular illustration, an
  inviting title, and a helpful subtitle instead of
  bare "Select a message" text.
- **Error state** — a real error illustration with
  a Retry button instead of a red text string.
- **Skeleton loader** — while a message loads the
  header and body are replaced with shimmering
  placeholders that match the real proportions, so
  the layout does not jump.

### G. Compose modal polish

- **Premium header** — bold title with a refined
  icon-only close / minimize cluster.
- **Backdrop blur** — the modal backdrop uses a
  semi-transparent overlay plus a CSS
  `backdrop-filter: blur` so the shell behind feels
  present without competing.
- **Animated entrance** — the modal scales in from
  0.98 with a fade; respects reduced-motion.
- **Field expander** — the "Cc / Bcc" toggle is now
  a styled text button (not an icon-only button)
  with `aria-expanded` for screen readers.
- **Compose body** is now 14.5 px / 1.65 line-height
  (16 px on 390 px to defeat iOS auto-zoom) with a
  generous 18 px padding.
- **Autosave status line** — the indicator dot is
  animated (pulse on saving), and the success/error
  states have small badge icons for instant
  recognition.
- **Toasts** — toasts now have circular status
  badges (✓, !, etc.) and gradient-tinted
  backgrounds so success / error / warning / info
  are immediately distinguishable.

### H. Empty, loading, and error states

- **Per-folder empty states** — Inbox / Sent /
  Drafts / Trash / Archive / Junk each have their
  own illustration glyph, title, and helpful
  subtitle. Search-no-results has its own "No
  matches" copy.
- **Skeleton loaders** — the message list shows
  six skeleton rows (with checkbox, star, from,
  subject, preview, and meta placeholders) while
  the first page loads. The reading pane shows a
  skeleton header + body while a single message
  loads. The reduced-motion media query disables
  the shimmer animation.
- **Error states** — the reading pane error has a
  real illustration, an "Retry" button that
  re-issues the same request without a full reload,
  and avoids raw stack traces.

### I. Bulk action bar

- **Floating pill** — the bulk bar is now a
  floating pill, not a flat bar at the bottom of
  the viewport. Backdrop-blurred so it reads as
  elevated.
- **Animated entrance** — slides up from below on
  first appearance.
- **Centered** at large viewports; pinned to the
  edges with a smaller radius at 768 px and below.

### J. Auth gate

- **Brand mark** on every card (loading, login,
  no-mailbox, error) so the user always knows
  which product they are looking at.
- **Footer line** marks the build / version.
- **Animated card entrance** — fade + translate,
  respects reduced-motion.
- **Refined inputs** — the email and password
  fields now have focus glows that match the
  shell's accent.
- **Light theme** — the gate adopts the same
  theme tokens as the shell.

### K. Accessibility

- **Consistent focus rings** — every focusable
  element uses a 2 px accent outline + a 3 px glow
  on form inputs. Defined once via `:focus-visible`,
  extended by component rules where needed.
- **Reduced motion** — `prefers-reduced-motion:
  reduce` zeroes every transition / animation in
  one block (already present in Feature Pack 2;
  reaffirmed here).
- **Aria-label coverage** — every icon-only button
  in the new polish layer carries an `aria-label`
  (refresh, mark-all-read, toggle sidebar, back,
  minimize, close, show-cc-bcc).
- **Aria-expanded** — the Cc/Bcc toggle exposes
  its state for screen-reader users.
- **Logical properties** — `margin-inline-*`,
  `padding-inline-*`, `inset-inline-*`,
  `border-inline-*` are used in the new code paths
  so the Arabic / Hebrew experience is correct by
  construction.

### L. RTL / Arabic

- **No new physical-property regressions** —
  every new component uses logical properties.
- **Direction = auto on dynamic text** — preserved
  from Feature Pack 2.
- **Email addresses in Arabic context** —
  `direction: ltr` + `unicode-bidi: isolate` on
  email fields so they never wrap inside an
  Arabic paragraph.

### M. Tests and release verification

The existing static-analysis suite (no
localStorage / sessionStorage, no
/api/v1/queue usage, prefers-reduced-motion
media query, 1440 / 1024 / 768 / 390
breakpoints, logical-property sweep, keyboard
shortcuts) still passes. New tests added:

- `TestWebmailCSSHasLightTheme` — the stylesheet
  defines `:root.theme-light` and re-tokenizes
  the core semantic variables.
- `TestWebmailCSSHasDesignSystemTokens` —
  verifies the design token namespace
  (`--bg-canvas`, `--shadow-md`, `--motion-fast`,
  etc.) is present.
- `TestWebmailCSSHasPremiumComponents` — verifies
  the premium components (`skeleton`,
  `empty-illustration`, `field-expander`,
  `toast.warning`, gradient tokens, etc.) are
  styled.
- `TestWebmailJSSkeletonRendering` — confirms the
  `skeletonMessageRow` / `renderEmptyState` /
  `renderReadingPaneEmpty` helpers exist in
  webmail.js.
- `TestWebmailJSRichEmptyStates` — confirms the
  per-folder empty-state copy is in the JS.
- `TestWebmailJSAriaLabelPatterns` — confirms
  every icon-only button carries an aria-label.
- `TestWebmailJSRespectsColorScheme` — confirms
  the JS detects `prefers-color-scheme: light`
  and applies the `theme-light` class.

## Files changed

### Frontend
- `release/webmail/assets/webmail.css` — full
  design-system overhaul, premium polish layer,
  light-theme tokens, skeleton loaders, premium
  toasts, premium modal backdrop.
- `release/webmail/assets/webmail.js` — skeleton
  loader rendering, per-folder empty states,
  reading-pane empty / error states with retry,
  theme-light detection, brand-aware field
  expander.
- `release/webmail/assets/auth-gate.css` — premium
  theme tokens matching the shell, brand mark,
  focus glow on inputs, animated card entrance.
- `release/webmail/assets/auth-gate.js` — brand
  mark + footer on every card.

### Tests
- `internal/api/handlers/webmail_frontend_test.go`
  — 7 new static-analysis tests for the polish
  pack; existing tests still pass.

## Known limitations

- **No per-user theme toggle** — the light theme is
  opt-in via `prefers-color-scheme: light` or via
  the `theme-light` class on `<html>`. The webmail
  UI does not write to localStorage / sessionStorage
  by project rule, so we cannot persist a per-user
  preference across reloads.
- **No light-theme preview in this build** — the
  dark theme remains the Orvix default. The light
  tokens are defined and ready; flipping the theme
  is a one-class swap.

---

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
