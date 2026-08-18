# Webmail UI Polish 3 — Changelog

Branch: `feature/webmail-ui-polish-3`
Base: `08e380b` ("Merge Webmail Enterprise Feature Pack 2")

# Webmail Live Stabilization 3A — Changelog

Branch: `fix/webmail-live-stabilization-3a`
Base: `58c2e1b` ("WEBMAIL-UI-POLISH-3: premium enterprise webmail UI")

# Webmail Release 1 — Changelog

Branch: `feature/webmail-release-v1`
Base: `main` (post-`da53ddb fix: route mail autodiscover through coremail API`)

## Operator fix — R1 placeholder/coming-soon cleanup (cut on `feature/webmail-r1-password-security-fix`)

### Removed from production UI

The R1 acceptance bar forbade placeholder / coming-soon copy
in production. The main branch still shipped a **Settings →
Coming later** tab with an "Available in a future release"
panel that listed TOTP, app-passwords, per-device sessions,
reading-pane position, conversation view, and external image
preference as "future features". The Settings → Security tab
also carried a "TOTP / app-passwords UI is not enabled in
this build" notice. The compose forward banner advertised
"Attachment forwarding is coming soon."

Cut `feature/webmail-r1-password-security-fix` ships:

- **Settings → "Coming later" tab REMOVED.** No 10-tab
  Settings modal — the released tab count is 10
  (Profile / Appearance / Compose / Mail / Filters /
  Vacation / Forwarding / Mail Client Setup / Notifications
  / Security).
- **"Available in a future release" panel REMOVED.**
  Gated by `smoke-webmail-ui.sh`, which now FAILS the
  build if any of `Coming later | Available in a future
  release | coming soon | is not enabled |
  settings-deferred-list` survives in the production
  bundle.
- **TOTP / app-passwords / per-device sessions placeholder
  REMOVED from Security.** Security now ships real controls
  only.
- **"Attachment forwarding is coming soon." rewritten** to
  an honest limitation note: attachments are not yet
  composable from the webmail compose path; reply to the
  original to keep the files.
- **"Reading pane" fake select REMOVED.** The reading pane
  section now ships a single short notice ("rendered next to
  the message list. The mobile layout stacks the panes
  vertically.") — no fake control that pretends to save.

### What replaced the placeholder: real employee Change Password

- **Backend endpoint.** `POST /api/v1/webmail/password/change`,
  mounted on the `authCSRF` group in `router.go`. Rules:
  - Auth middleware first (no cookie → 401).
  - CSRF middleware (X-CSRF-Token matches the `csrf_token`
    cookie).
  - Body parsed: `{ current_password, new_password,
    confirm_password? }`. Per-field validation server-side:
    missing → 400 "current password required" / "new password
    required"; mismatched → 400 "do not match"; new
    `< 8` chars → 400 "at least 8 characters".
  - Current password verified against the canonical
    `coremail_mailboxes.password_hash` column via
    `verifyMailboxPassword` (handles both `$argon2id$` and
    legacy bcrypt mailboxes in production today).
  - On success: write fresh `$argon2id$v=19$m=…,t=…,p=…$…$…`
    to `coremail_mailboxes.password_hash`, set
    `auth_scheme='$argon2id$'`, bump `updated_at`. The
    mailbox id is taken from `resolveWebmailUserContext`
    ONLY — the body is read for `current_password`,
    `new_password`, `confirm_password`. Any id-shaped body
    field is ignored, and the body never reaches the
    UPDATE clause.
  - Wrong current password → 401 generic "invalid
    credentials". No enumeration. The same body is
    returned for "no such mailbox" and "wrong password" so
    a forged JWT cannot enumerate foreign mailboxes.
  - Logs an `webmail.password_change` audit entry. NO
    password / hash / token fields in the audit fields.
  - Response: `200 {"status":"changed"}`. No hash, no token,
    no password. Set-Cookie is NOT touched — the existing
    short-lived access_token keeps working until it
    naturally expires; the production model is "change
    password, keep using the session", not "change password,
    force re-login".

- **UI surface.** Settings → Security renders a Change
  Password section with three password inputs (current /
  new / confirm), a "Change password" button, and a
  `role="status"` region for success / error feedback.
  Client-side validation mirrors the server (matching
  message wording); client validation is UX, server is
  truth. On success: inputs are cleared, the green status
  flips on, a non-sensitive toast fires. On error: the
  server's `error` field is rendered in the role=status
  region; no password / hash / token ever reaches the
  user-visible tree.
  - `type="password"` on all three inputs.
  - `autocomplete` set so a borrowed browser does not cache
    the value across sessions (`current-password`,
    `new-password`, `new-password`).
  - `spellcheck="false"`, `autocapitalize="off"`,
    `autocorrect="off"` to defeat mobile keyboards'
    autocorrect, which would silently change the password.
  - NO localStorage / sessionStorage writes anywhere.
  - The form is its own DOM tree (does not collide with
    the catch-all `Save / Cancel` footer in the modal — the
    Security section marks itself as having its own footer
    so the catch-all saves its `Cancel` button into the
    right place).

- **Smoke surface.**
  - `smoke-webmail-ui.sh` gained two new gates:
    - **§13**: forbids the placeholder tokens
      `Coming later | Available in a future | coming
      soon | is not enabled | settings-deferred-list` in
      the production bundle. Builds fail if any survives.
    - **§14**: pins the Change Password surface (`/api/
      v1/webmail/password/change`, `current_password`,
      `new_password`, `confirm_password`, `renderSecurityTab`).
  - `smoke-webmail-functional-browser.{sh,mjs}` gained two
    real phases plus mock backend coverage:
    - **Phase 8**: Settings → Security renders three
      password inputs (autocomplete = current-password,
      new-password, new-password) + a "Change password"
      button + 10 tabs total (Profile, Appearance,
      Compose, Mail, Filters, Vacation, Forwarding, Mail
      Client Setup, Notifications, Security) — no
      "Coming later" tab present; no `settings-deferred-list`
      element on the active Security tab; no copy
      containing the banned tokens.
    - **Phase 9**: mismatch submit surfaces the inline
      error "New password and confirmation do not match."
    - **Phase 10**: valid submit clears all three inputs and
      flips the status region to the `.success` class with
      "Password changed. Your new password is now in
      effect." The mock backend now answers
      `POST /api/v1/webmail/password/change` with a faithful
      shape (`{"status":"changed"}` on success, generic
      `"invalid credentials"` on a wrong current password).

- **Go regression tests.** Added
  `internal/api/handlers/webmail_change_password_test.go`
  with **10 tests** that exercise the production handler
  directly:
  1. `TestWebmailChangePasswordUnauthenticatedReturns401`
  2. `TestWebmailChangePasswordWrongCurrentPasswordRejected`
  3. `TestWebmailChangePasswordRejectsMissingFields`
  4. `TestWebmailChangePasswordRejectsMismatchedConfirmationOrWeakPassword`
  5. `TestWebmailChangePasswordIgnoresExtraFieldsSafely`
  6. `TestWebmailChangePasswordSuccessUpdatesHash`
  7. `TestWebmailChangePasswordOldPasswordNoLongerWorks`
  8. `TestWebmailChangePasswordNewPasswordWorks`
  9. `TestWebmailChangePasswordResponseCarriesNoHash`
  10. `TestWebmailChangePasswordCrossMailboxImpossible`

### What is NOT in R1 (kept honest, off the product surface)

- Calendar, Contacts, Tasks — see `docs/WEBMAIL_RELEASE_1_AUDIT.md`
  §10.
- Exchange / ActiveSync (EAS) — see audit §10.
- Mobile native app — see audit §10.
- Offline webmail / Service worker push only — see audit §10.
- Full-text body search index — see audit §10.
- Pasted image into compose body — see audit §10.
- JMAP-native webmail — see audit §10.

These items live in `docs/WEBMAIL_RELEASE_1_AUDIT.md`
"Known limitations" only — NEVER in shipped product UI.

## What shipped in the original R1 cut

**User-visible**

- **Mail Client Setup tab inside the webmail Settings modal.**
  `release/webmail/assets/webmail.js` (`renderClientSetupTab`
  in the settings-modal dispatch) renders the IMAP / SMTP /
  Outlook Autodiscover / Thunderbird Autoconfig info the
  user hands to Outlook, Thunderbird, Apple Mail, K-9 etc.
  Each row carries a Copy button (`navigator.clipboard.writeText`
  with a select-and-Ctrl-C fallback). Hostname is derived
  from `window.location.hostname` by stripping a leading
  `webmail.` prefix (so the SPA on `webmail.orvix.email`
  serves the mail client settings for `mail.orvix.email`).
  Username is the live mailbox address from `/me`. **No
  password is ever displayed and no client-supplied field
  writes a secret.** Section "Password" is an explicit
  reminder to enter the password directly into the mail
  client.
- **`window.OrvixWebmail.openClientSetup()`** — new public
  entry point on the existing API. Opens the Settings modal
  and switches to the Client Setup tab. Used by the smoke
  harness; also usable as a deep-link if future iterations
  decide to expose it.
- **`window.OrvixWebmail.openCompose` and `openSettingsModal`**
  re-exported through the public API (already exposed;
  re-affirmed by the smoke, with selector tightening so
  the smoke does not break on class-name evolution).
- **Mobile-responsive grid for the new tab** —
  `.settings-client-setup-row` collapses to a single column
  at `max-width: 640px` so long URLs and `host:port`
  combinations stay readable on phones.
- **`@media (max-width: 640px)` block was added**, taking
  the CSS `@media` count from 12 to 13.

**Operations**

- **`docs/WEBMAIL_RELEASE_1_AUDIT.md`** — the canonical
  "what shipped in R1" document with an honest "Not
  implemented" section (no fake Calendar / Contacts / EAS).
- **`docs/WEBMAIL_SECURITY_REVIEW.md`** — the security
  review companion to the audit. Threat-by-threat
  pin-pointing every defence against every cross-mailbox /
  XSS / impersonation attack the surface could face.
- **`release/scripts/smoke-webmail-js.sh`** — Node syntax
  check for the webmail bundle (parity with the existing
  admin equivalent).
- **`release/scripts/smoke-webmail-ui.sh`** — 12 structural
  checks: index.html load order, helper-function presence,
  dirAuto wiring, no-queue-API guard, RTL plumbing,
  responsive @media, settings modal, attachment URL auth,
  system folder constants, router-drift guard.
- **`release/scripts/smoke-webmail-browser.sh`** —
  bundle-shape + Node-syntax-check + content-marker smoke.
  Pure file system; no Chrome required.
- **`release/scripts/smoke-webmail-functional-browser.sh`**
  + **`.mjs`** — **self-contained** headless Chrome
  functional smoke via raw CDP. The .mjs spawns a local Node
  HTTP server on a free port that serves the webmail bundle
  AND mocks the few API endpoints the auth-gate + SPA shell
  probe. **No external network, no live Orvix backend, no
  port collisions.** Drives the auth-gate → login → SPA
  shell → folder sidebar → message list → reading pane →
  compose modal → Settings → **Mail Client Setup tab** →
  copy buttons → dirAuto helper → zero-console-errors
  chain. Runs in Node 22+ with a Chromium-class browser on
  disk. **Passes locally** in the R1 working tree.
- **`release/scripts/smoke-caddy-autodiscover.sh`** —
  static-analysis regression guard for the Caddy
  mail-vhost autodiscover routing. Pins the four required
  handlers (`/autodiscover/*`, `/Autodiscover/*`,
  `/.well-known/autoconfig/*`, `/mail/config-v1.1.xml`)
  inside the `$MAIL_DOMAIN` block, before the `8081`
  catch-all, and confirms the `$WEBMAIL_DOMAIN` block is
  preserved (substring-match bug regression guard).
- **`internal/api/handlers/webmail_release1_test.go`** —
  R1 cross-cutting Go tests:
  - `TestWebmailRelease1AuthRequiredForAllUserEndpoints` —
    full endpoint matrix returns 401 with no cookie.
  - `TestWebmailRelease1SearchSnippetStaysInsideOwnerMailbox` —
    search snippet pipeline never returns a foreign row.
  - `TestWebmailRelease1SendAuthoritativeFrom` — the From
    header on the resulting RFC822 is always the
    authenticated mailbox's email, never any
    client-supplied value.
  - `TestWebmailRelease1SendReturnsQueuedCountFromLiveQueue`
    — POST /send response carries `status: queued`,
    `queued_count`, `local_count`, `remote_count` from
    the live QueueEngine.

## No-claim list

The audit doc explicitly does NOT claim:

- Calendar / Contacts / Tasks — not shipped in the webmail
  UI.
- Exchange / ActiveSync (EAS) — not implemented. Outlook
  onboarding is via the autodiscover XML, not EAS.
- Mobile native apps — not shipped. The webmail UI works
  on mobile browsers via responsive CSS; no iOS / Android
  native client exists.
- Full-text body search index — `?body=1` falls back to
  per-row read; a proper inverted index is a follow-up.
- Pasted / dragged image into compose body — not shipped.
- JMAP-native client — the mail.<domain> Caddy catch-all
  routes non-autodiscover paths to the JMAP listener at
  8081, but the webmail UI itself does not speak JMAP.

## Out of scope

No backend / SMTP / IMAP / POP3 / JMAP / queue / delivery
touches. No admin UI touches. The auth / session / CSRF /
CSP / cookie-model is unchanged.

## Smoke surface delta

| Script | Pre-R1 | Post-R1 |
|---|---|---|
| `smoke-admin-js.sh`      | 51 admin JS files | unchanged |
| `smoke-admin-ui.sh`      | 12 admin UI checks | unchanged |
| `smoke-admin-browser.sh` | serves admin bundle | unchanged |
| `smoke-admin-functional-browser.{sh,mjs}` | admin functional | unchanged |
| `smoke-webmail-js.sh`    | — | 4 webmail JS files |
| `smoke-webmail-ui.sh`    | — | 12 webmail UI checks |
| `smoke-webmail-browser.sh` | — | 10 webmail bundle checks |
| `smoke-webmail-functional-browser.{sh,mjs}` | — | headless Chrome functional |
| `smoke-caddy-autodiscover.sh` | — | 9 Caddy regression checks |
| `smoke-install-bundle.sh` | unchanged | unchanged |
| `smoke-install-public.sh` | unchanged | unchanged |
| `smoke-runtime.sh`       | unchanged | unchanged |
| `smoke-upgrade.sh`       | unchanged | unchanged |
| `smoke-health.sh`        | unchanged | unchanged |
| `smoke-ports.sh`         | unchanged | unchanged |

## Why a documentation / smoke slice and not a feature slice

The webmail Enterprise v1 + Enterprise v2 + UI Polish 3 +
Live Stabilization 3A + autodiscover slices brought R1 to
its current shape. The R1 cut is the final layer that:

- **documents** what shipped in R1 honestly,
- **secures** the long-tail guarantee with a
  security-review doc,
- **extends** the smoke surface so future slices cannot
  regress the webmail shell without CI going red,
- and **covers** the previously-uncaptured test gaps
  (auth-required-for-all-endpoints matrix, send
  authoritative From, send response shape).

The cut is intentionally non-breaking: R1 introduces zero
new behavior, zero new endpoints, and zero new failure
modes. The `READY_FOR_OPENCODE_REVIEW` verdict in
`WEBMAIL_RELEASE_1_AUDIT.md` reflects this.

A small live stabilization pass on top of UI Polish 3. No
new features. No backend changes. No endpoint changes.
Scope is a single live-UI regression: the reading-pane
toolbar was rendering a duplicate "Move to…" control after
every in-pane action (star toggle, mark read / unread,
spam toggle). The teardown only matched `.action-btn` and
missed the `.move-to-wrap` wrapper around the move-to
`<select>`, so each re-render appended a fresh "Move to…"
instead of replacing it. The same teardown bug lived in
`renderReadingPaneLoading`, so stale wrappers also
survived the loading skeleton when navigating between
messages.

## Fixed

- **Duplicate "Move to…" in reading-pane toolbar.** The
  reading-pane toolbar teardown now removes both
  `.action-btn` and `.move-to-wrap` so every dynamic
  toolbar child is wiped before re-rendering. One wrapper
  in, one wrapper out — no compounding.
- **Cross-message stale wrapper.** `renderReadingPaneLoading`
  applies the same teardown so a stale `.move-to-wrap`
  from the previous message cannot leak into the loading
  skeleton of the next one.
- **Reading-pane toolbar overflow at 390 px.** The
  `.move-to-wrap` flex item now has `min-width: 0` and
  `flex-shrink: 1` and its inner `<select>` has
  `max-width: 100%` and `min-width: 0`. The toolbar is
  already `flex-wrap: wrap`, so the move-to control now
  cleanly wraps to a second row on the narrowest viewports
  instead of forcing horizontal scroll. No visual change at
  desktop / 1024 / 768.

## Tests added

- `TestWebmailReadingToolbarTeardownRemovesMoveToWrap` —
  asserts the teardown selector names both `.action-btn`
  and `.move-to-wrap` and that the wrapper is created in
  exactly one place per render. Whitespace-tolerant.
- `TestWebmailReadingToolbarMoveToSelectIsSingleSource` —
  asserts the literal `'Move to…'` placeholder string is
  defined exactly once in `webmail.js` so a future
  copy-paste cannot reintroduce the duplicate even if the
  teardown were correct.

## Out of scope

No backend / SMTP / IMAP / POP3 / JMAP / Queue / Delivery
touches. No admin UI touches. No installer touches. No
auth / session / CSRF / CSP / cookie model touches. No new
endpoints. No new attachments flow. No new keyboard
shortcuts. No new themes. Pure UI teardown correctness +
one narrow-viewport flex shrink rule.

## VPS verification checklist

1. Hard-refresh webmail (Cmd-Shift-R / Ctrl-Shift-R).
2. Open a Sent message.
3. Confirm the reading-pane toolbar shows exactly **one**
   "Move to…" control.
4. Click Star a few times. Confirm the toolbar still
   shows exactly one "Move to…" control and one star
   button.
5. Click Mark unread / Mark read a few times. Confirm
   the same.
6. Click Report spam / Not spam. Confirm the same.
7. Open the compose modal. Confirm it opens, the send
   button is wired, and closing it leaves the reading
   pane toolbar with one "Move to…" control.
8. Navigate between messages (Inbox → Sent → Drafts →
   back). Confirm the toolbar still shows one "Move to…".
9. Open browser DevTools console. Confirm zero JS errors.


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
