/* =====================================================================
   Orvix Webmail — production-grade enterprise client.
   Vanilla JS, no external dependencies. Sends credentials with every
   request to the webmail API only; it does not use the admin queue API or
   browser-managed token storage (cookies are HttpOnly).

   Layout: 3-pane shell rendered by init().
     - Topbar    : brand, menu toggle, search, user chip
     - Sidebar   : folder list with unread counts + Compose button
     - List      : message list, mark-all-read, refresh
     - Reading   : selected message, action bar, body, attachments
     - Modal     : compose / reply / reply-all / forward
     - Toasts    : transient feedback

   Routing: hash-based so back/forward works.
     #/folder/INBOX
     #/folder/INBOX/message/42
     #/search?q=hello
     #/compose
     #/compose?replyTo=42
     #/compose?replyAll=42
     #/compose?forward=42
     #/compose?draft=12

   RTL: dirAuto(text) returns "rtl"|"ltr"|"auto" based on the first
   strong-direction character. dir="auto" is set on every piece of
   dynamic text (subject, from, body, preview, compose body).

   XSS: every dynamic string that ends up as HTML is escaped via
   escapeHTML() before composition; the body is rendered through
   renderBody() which sanitises HTML if needed (no <script>,
   no on* attributes, no javascript: URLs).
   ===================================================================== */

(function () {
  'use strict';

  // ──────────────────────────────────────────────────────────
  // Utilities
  // ──────────────────────────────────────────────────────────

  // escapeHTML escapes & < > " ' so user-controlled text cannot
  // inject HTML. All innerHTML assignments in this file MUST run
  // dynamic text through this function (or through renderBody).
  function escapeHTML(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // dirAuto returns "rtl" if the first strong-direction character is
  // Arabic/Hebrew, "ltr" if it's Latin/Common digits, "auto" if the
  // string is empty or starts with neutral punctuation.
  //
  // Range coverage (Unicode BiDi):
  //   Arabic  : U+0600..U+06FF, U+0750..U+077F, U+08A0..U+08FF,
  //             U+FB50..U+FDFF, U+FE70..U+FEFF
  //   Hebrew  : U+0590..U+05FF, U+FB1D..U+FB4F
  //   Latin   : Basic Latin + Latin-1 Supplement + Latin Extended
  //             (we treat anything outside the Arabic/Hebrew ranges
  //             as LTR by default; the browser's "auto" handles the
  //             ambiguous cases at render time).
  //
  // The function skips common neutral reply / forward prefixes
  // ("Re:", "Fwd:", "RE:", "FWD:") before deciding direction, and
  // also skips leading whitespace + punctuation. This avoids the
  // common bug where a subject like
  //   "Re: 5 things to know"
  // gets tagged "rtl" because of a stray digit, or where an Arabic
  // subject prefixed with "Re:" gets tagged "ltr" because the
  // prefix letters happen to be Latin.
  function dirAuto(s) {
    if (!s) return 'auto';
    // Strip leading reply/forward prefixes. We compare on a
    // lower-cased prefix; the actual letters are skipped later
    // by the same loop that skips whitespace + punctuation.
    var t = s.replace(/^(\s*(re|fwd|fw)\s*:\s*)+/i, '');
    var n = t.length;
    for (var i = 0; i < n; i++) {
      var c = t.charCodeAt(i);
      // Skip whitespace.
      if (c === 0x20 || c === 0x09 || c === 0x0a || c === 0x0d) continue;
      // Skip common punctuation: ! " # $ % & ' ( ) * + , - . /
      if (
        c === 0x21 ||
        c === 0x22 ||
        c === 0x23 ||
        c === 0x24 ||
        c === 0x25 ||
        c === 0x26 ||
        c === 0x27 ||
        c === 0x28 ||
        c === 0x29 ||
        c === 0x2a ||
        c === 0x2b ||
        c === 0x2c ||
        c === 0x2d ||
        c === 0x2e ||
        c === 0x2f ||
        // digits + : ; < = > ?
        (c >= 0x30 && c <= 0x3f)
      )
        continue;
      // Square brackets / quotes sometimes used as
      // quoting prefixes.
      if (c === 0x5b || c === 0x5d || c === 0xab || c === 0xbb)
        continue;
      // Arabic.
      if (
        (c >= 0x0600 && c <= 0x06ff) ||
        (c >= 0x0750 && c <= 0x077f) ||
        (c >= 0x08a0 && c <= 0x08ff) ||
        (c >= 0xfb50 && c <= 0xfdff) ||
        (c >= 0xfe70 && c <= 0xfeff)
      )
        return 'rtl';
      // Hebrew.
      if (
        (c >= 0x0590 && c <= 0x05ff) ||
        (c >= 0xfb1d && c <= 0xfb4f)
      )
        return 'rtl';
      // Otherwise LTR (Latin, CJK, Cyrillic, etc.).
      return 'ltr';
    }
    return 'auto';
  }

  // linkifyURLs converts bare URLs in plain text into clickable
  // anchor tags. Defensive: only matches http(s):// and mailto:,
  // never javascript:.
  function linkifyURLs(escaped) {
    var re = /\bhttps?:\/\/[^\s<>"']+|\bmailto:[^\s<>"']+/g;
    return escaped.replace(re, function (m) {
      var safeHref = m.replace(/"/g, '%22');
      return '<a href="' + safeHref + '" target="_blank" rel="noopener noreferrer">' + m + '</a>';
    });
  }

  // renderBody takes the message body. For v1 the storage layer
  // stores everything as plain text/rfc822. We detect a "Content-Type:
  // text/html" header to decide whether to render as HTML (sanitised)
  // or as plain text. Falls back to plain text.
  //
  // Sanitisation (light, in-house — no DOMPurify dependency):
  //   - strip <script>...</script>
  //   - strip <style>...</style>
  //   - strip <iframe>, <object>, <embed>, <form>
  //   - strip on* event-handler attributes (onclick, onerror, ...)
  //   - strip javascript: URLs in href / src
  //   - leave <a href>, <b>, <i>, <u>, <p>, <br>, <blockquote>,
  //     <ul>, <ol>, <li>, <strong>, <em>, <pre>, <div>, <span>,
  //     <table>/<tr>/<td>/<th>, <h1..h6>, <img src="https?..." alt>
  //
  // This is NOT a full HTML sanitiser — anything beyond a polite
  // defence is out of scope for the v1 webmail client. The body
  // still flows through escapeHTML on the plain-text path, so XSS
  // from raw content is not possible there.
  function renderBody(rfc822Text) {
    if (!rfc822Text) return '<pre class="empty-body">(empty message)</pre>';

    var headersEnd = rfc822Text.indexOf('\r\n\r\n');
    var headerBlock = '';
    var bodyText = rfc822Text;
    if (headersEnd >= 0) {
      headerBlock = rfc822Text.slice(0, headersEnd);
      bodyText = rfc822Text.slice(headersEnd + 4);
    } else {
      var nl = rfc822Text.indexOf('\n\n');
      if (nl >= 0) {
        headerBlock = rfc822Text.slice(0, nl);
        bodyText = rfc822Text.slice(nl + 2);
      }
    }

    var contentType = '';
    var ctypeMatch = headerBlock.match(/^Content-Type:\s*(.+)$/im);
    if (ctypeMatch) contentType = ctypeMatch[1].trim().toLowerCase();

    if (contentType.indexOf('text/html') === 0) {
      return sanitiseHTML(bodyText);
    }
    // Default: plain text. Escape + linkify + respect
    // dir="auto" via the pre wrapper (the browser will
    // detect per-glyph direction).
    var safe = escapeHTML(bodyText);
    safe = linkifyURLs(safe);
    return '<pre class="plain-body">' + safe + '</pre>';
  }

  function sanitiseHTML(html) {
    if (!html) return '';
    // Order matters: strip <script>/<style> blocks BEFORE attribute
    // stripping so we don't accidentally strip the inside of a
    // nested element.
    html = html.replace(/<script\b[\s\S]*?<\/script>/gi, '');
    html = html.replace(/<style\b[\s\S]*?<\/style>/gi, '');
    html = html.replace(/<iframe\b[\s\S]*?<\/iframe>/gi, '');
    html = html.replace(/<object\b[\s\S]*?<\/object>/gi, '');
    html = html.replace(/<embed\b[^>]*>/gi, '');
    html = html.replace(/<form\b[\s\S]*?<\/form>/gi, '');
    // Strip on* event handler attributes.
    html = html.replace(/\s+on[a-z]+\s*=\s*"[^"]*"/gi, '');
    html = html.replace(/\s+on[a-z]+\s*=\s*'[^']*'/gi, '');
    html = html.replace(/\s+on[a-z]+\s*=\s*[^\s>]+/gi, '');
    // Strip javascript: URLs.
    html = html.replace(/(\s(?:href|src)\s*=\s*")\s*javascript:[^"]*"/gi, '$1#"');
    html = html.replace(/(\s(?:href|src)\s*=\s*')\s*javascript:[^']*'/gi, "$1#'");
    // Strip data: URLs except safe image types.
    html = html.replace(/(\s(?:href|src)\s*=\s*")\s*data:(?!image\/(?:png|jpe?g|gif|webp);)[^"]*"/gi, '$1#"');
    // Disallow <meta http-equiv="refresh"> (silent redirect).
    html = html.replace(/<meta\b[^>]*http-equiv\s*=\s*["']?refresh["']?[^>]*>/gi, '');
    return html;
  }

  function formatDate(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var now = new Date();
    var sameDay =
      d.getFullYear() === now.getFullYear() &&
      d.getMonth() === now.getMonth() &&
      d.getDate() === now.getDate();
    if (sameDay) {
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }
    var sameYear = d.getFullYear() === now.getFullYear();
    if (sameYear) {
      return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    }
    return d.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
  }

  function formatFullDate(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleString([], {
      weekday: 'short',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  }

  function initials(name, email) {
    var src = (name && name.trim()) || (email && email.split('@')[0]) || '?';
    var parts = src.split(/[\s._-]+/).filter(Boolean);
    if (parts.length === 0) return '?';
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }

  function displayName(fromHeader) {
    // fromHeader looks like:  "Alice <alice@x>"  or  "alice@x"
    if (!fromHeader) return '';
    var m = fromHeader.match(/^\s*"?([^"<]+?)"?\s*<[^>]+>\s*$/);
    if (m) return m[1].trim();
    return fromHeader.trim();
  }

  function emailOf(fromHeader) {
    if (!fromHeader) return '';
    var m = fromHeader.match(/<([^>]+)>/);
    if (m) return m[1].trim();
    return fromHeader.trim();
  }

  // formatSender formats the sender string for the message list /
  // reading pane according to the user-configured `sender_display`
  // setting. Allowed values: 'name' | 'email' | 'name_email'.
  // Anything else falls back to 'name'. Empty / missing inputs are
  // rendered as '(unknown)' so the UI never shows a blank cell.
  function formatSender(fromHeader) {
    var pref = (state.settings && state.settings.sender_display) || 'name';
    var name = displayName(fromHeader);
    var email = emailOf(fromHeader);
    if (pref === 'email') {
      return email || name || '(unknown)';
    }
    if (pref === 'name_email') {
      if (name && email && email.toLowerCase() !== name.toLowerCase()) {
        return name + ' <' + email + '>';
      }
      return name || email || '(unknown)';
    }
    // Default 'name'.
    return name || email || '(unknown)';
  }

  function pluralize(n, singular, plural) {
    return n + ' ' + (n === 1 ? singular : plural || singular + 's');
  }

  function debounce(fn, ms) {
    var t;
    return function () {
      var args = arguments;
      var ctx = this;
      clearTimeout(t);
      t = setTimeout(function () {
        fn.apply(ctx, args);
      }, ms);
    };
  }

  // ──────────────────────────────────────────────────────────
  // Toasts
  // ──────────────────────────────────────────────────────────
  var toastContainer;
  function ensureToastContainer() {
    if (toastContainer) return toastContainer;
    toastContainer = document.createElement('div');
    toastContainer.className = 'toasts';
    document.body.appendChild(toastContainer);
    return toastContainer;
  }
  function toast(msg, type, ms) {
    ensureToastContainer();
    var el = document.createElement('div');
    el.className = 'toast ' + (type || '');
    el.textContent = msg;
    var close = document.createElement('button');
    close.className = 'close';
    close.type = 'button';
    close.textContent = '×';
    close.setAttribute('aria-label', 'Dismiss');
    close.addEventListener('click', function () {
      el.remove();
    });
    el.appendChild(close);
    toastContainer.appendChild(el);
    setTimeout(function () {
      if (el.parentNode) el.remove();
    }, ms || 3500);
  }

  // ──────────────────────────────────────────────────────────
  // State + API
  // ──────────────────────────────────────────────────────────
  var state = {
    user: null,
    folders: [],
    folderByPath: {},
    folderByID: {},
    currentFolderPath: 'INBOX',
    messages: [],
    messagesPage: 0,
    messagesHasMore: false,
    messagesLoading: false,
    selectedMessageID: null,
    selectedMessage: null,
    // Bulk-select model. selectionMode becomes true the
    // first time the user ticks a checkbox or presses the
    // "Select" affordance; it stays on until "Clear
    // selection" is pressed. The selection is a Set<id>
    // of messages currently ticked. The UI shows a
    // floating action bar when selection.size > 0.
    selectionMode: false,
    selection: {},
    lastSelectedID: null,
    compose: null, // {mode:'new'|'reply'|'replyAll'|'forward'|'draft', draftID, to, cc, bcc, subject, body, dirty, lastSavedAt}
    searchQuery: '',
    sidebarOpen: false,
    readingPaneOpen: false,
    // Per-mailbox settings (profile / appearance / compose / mail
    // behavior / notifications). Loaded once on app boot. The PUT
    // endpoint returns the post-update row, which we replace in
    // place. Null means "not loaded yet" — the UI gracefully shows
    // defaults until loadSettings() resolves.
    settings: null,
    settingsLoaded: false,
    // settingsLoadFailed is set when the initial GET returned an
    // error. The UI keeps working with the in-memory defaults, but
    // surfaces a clear non-fatal warning so the user is not misled
    // into thinking their saved values were loaded from the server.
    settingsLoadFailed: false,
    // settingsLoadFailureMessage carries the human-readable reason
    // for the failed load; rendered inside the Settings modal.
    settingsLoadFailureMessage: '',
    pushStatus: null, // {available, permission, enabled, reason}
    // Server-side filters / rules. Each rule is the raw
    // shape returned by GET /api/v1/webmail/rules:
    // { id, mailbox_id, name, enabled, sort_order,
    //   stop_processing, conditions_json, actions_json,
    //   created_at, updated_at }. conditions_json /
    // actions_json are stringified JSON blobs the rules
    // engine validates on every PUT/POST.
    rules: [],
    rulesLoaded: false,
    rulesLoadFailed: false,
    rulesLoadFailureMessage: '',
    // Vacation / out-of-office configuration. See
    // GET /api/v1/webmail/vacation — the row may not
    // exist yet, in which case the backend returns
    // { enabled: false, subject: '', body: '', … } via
    // GetOrCreate. The values the user sees in the form
    // are always the latest PUT response, so the UI is
    // guaranteed to match the server after a successful
    // save.
    vacation: null,
    vacationLoaded: false,
    vacationLoadFailed: false,
    vacationLoadFailureMessage: '',
    // Forwarding configuration. Same GetOrCreate
    // semantics as vacation. keep_copy defaults true on
    // the backend when a new row is created.
    forwarding: null,
    forwardingLoaded: false,
    forwardingLoadFailed: false,
    forwardingLoadFailureMessage: '',
    // "g" prefix for Gmail-style navigation. After
    // pressing "g" the next keypress is interpreted as a
    // folder shortcut. We use a single keypress window
    // (~1s) before resetting.
    gPrefixActive: false,
    gPrefixTimer: null,
  };

  var PAGE_SIZE = 50;
  var G_PREFIX_TIMEOUT_MS = 1000;

  function api(method, path, body) {
    var opts = {
      method: method,
      credentials: 'include',
      headers: { Accept: 'application/json' },
    };
    if (body !== undefined && body !== null) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (res) {
      var ct = res.headers.get('content-type') || '';
      var p = ct.indexOf('application/json') >= 0
        ? res.json()
        : res.text().then(function (t) {
            return { __raw: t };
          });
      return p.then(function (data) {
        if (!res.ok) {
          var err = new Error(
            (data && (data.error || data.message)) || res.statusText || ('HTTP ' + res.status)
          );
          err.status = res.status;
          err.body = data;
          throw err;
        }
        return data;
      });
    });
  }

  // csrfFetch fetches the CSRF token (cached) and attaches it to a
  // state-changing request as the X-CSRF-Token header. The backend
  // enforces CSRF on a small set of high-trust auth routes
  // (/api/v1/auth/logout, /api/v1/auth/logout-all,
  // /api/v1/auth/change-password, /api/v1/webmail/logout). Other
  // webmail endpoints (send, drafts, settings, push) sit on the
  // auth-protected group WITHOUT CSRF, so the api() helper does
  // not need to fetch the token on every call. csrfFetch is the
  // single source of truth for CSRF-aware requests in the SPA.
  var csrfTokenCache = null;
  function getCsrfToken() {
    if (csrfTokenCache) return Promise.resolve(csrfTokenCache);
    return api('GET', '/api/v1/csrf-token').then(function (r) {
      var tok = r && r.csrf_token;
      if (!tok) {
        throw new Error('CSRF token endpoint returned no token');
      }
      csrfTokenCache = tok;
      return tok;
    });
  }
  function csrfFetch(url, opts) {
    opts = opts || {};
    opts.method = opts.method || 'POST';
    opts.credentials = 'include';
    opts.headers = Object.assign({}, opts.headers || {}, { Accept: 'application/json' });
    return getCsrfToken().then(function (token) {
      opts.headers['X-CSRF-Token'] = token;
      return fetch(url, opts);
    });
  }

  function loadMe() {
    return api('GET', '/api/v1/webmail/me').then(function (data) {
      state.user = data.user;
      return data;
    });
  }

  function loadFolders() {
    return api('GET', '/api/v1/webmail/folders').then(function (data) {
      state.folders = (data && data.folders) || [];
      state.folderByPath = {};
      state.folderByID = {};
      state.folders.forEach(function (f) {
        state.folderByPath[f.path.toLowerCase()] = f;
        state.folderByID[f.id] = f;
      });
      return state.folders;
    });
  }

  function loadMessages(opts) {
    opts = opts || {};
    if (state.messagesLoading) return Promise.resolve();
    state.messagesLoading = true;
    state.messagesHasMore = false;
    state.messagesPage = 0;
    state.messages = [];

    var folder = opts.folder != null ? opts.folder : state.currentFolderPath;
    var q = opts.q != null ? opts.q : state.searchQuery;

    var qs = 'folder=' + encodeURIComponent(folder) + '&limit=' + PAGE_SIZE + '&offset=0';
    if (q) qs += '&q=' + encodeURIComponent(q);

    return api('GET', '/api/v1/webmail/messages?' + qs)
      .then(function (data) {
        var list = (data && data.messages) || [];
        state.messages = list;
        state.messagesHasMore = list.length >= PAGE_SIZE;
        state.messagesPage = 1;
        // Refresh folder unread counts (cheap; the server already
        // gave them to us in /folders; we just trust that and the
        // next /folders call will refresh).
        return data;
      })
      .finally(function () {
        state.messagesLoading = false;
      });
  }

  function loadMoreMessages() {
    if (state.messagesLoading || !state.messagesHasMore) return Promise.resolve();
    state.messagesLoading = true;
    var folder = state.currentFolderPath;
    var q = state.searchQuery;
    var qs =
      'folder=' +
      encodeURIComponent(folder) +
      '&limit=' +
      PAGE_SIZE +
      '&offset=' +
      (state.messagesPage * PAGE_SIZE);
    if (q) qs += '&q=' + encodeURIComponent(q);
    return api('GET', '/api/v1/webmail/messages?' + qs)
      .then(function (data) {
        var list = (data && data.messages) || [];
        state.messages = state.messages.concat(list);
        // Re-apply selection state — newly loaded rows
        // are unchecked, but if the user had IDs in the
        // selection that happen to be in this page they
        // show as ticked.
        for (var i = 0; i < list.length; i++) {
          if (state.selection[list[i].id]) list[i].__selected = true;
        }
        state.messagesHasMore = list.length >= PAGE_SIZE;
        state.messagesPage++;
        renderMessageList();
      })
      .finally(function () {
        state.messagesLoading = false;
      });
  }

  // pendingMarkReadTimer holds the setTimeout id of the next
  // mark-as-read PATCH so a quick succession of opens (or a
  // navigation away before the delay elapses) cancels the
  // previous attempt. Single-flight invariant: at most one
  // pending mark-read PATCH at a time.
  var pendingMarkReadTimer = null;
  var pendingMarkReadID = null;

  function cancelPendingMarkRead() {
    if (pendingMarkReadTimer) {
      clearTimeout(pendingMarkReadTimer);
      pendingMarkReadTimer = null;
      pendingMarkReadID = null;
    }
  }

  function scheduleMarkRead(id) {
    cancelPendingMarkRead();
    var s = state.settings || {};
    var delayMs = (typeof s.mark_read_delay_seconds === 'number')
      ? Math.max(0, Math.min(60, s.mark_read_delay_seconds)) * 1000
      : 0;
    // delay == 0 means "mark immediately" — matches the documented
    // default. We do NOT use setTimeout(0) because that fires after
    // the current microtask queue and the user might still navigate
    // away; a synchronous fire is the closest match to the legacy
    // behaviour for delay=0.
    var fire = function () {
      pendingMarkReadTimer = null;
      pendingMarkReadID = null;
      updateMessageFlags(id, { seen: true }).catch(function () {
        /* non-fatal */
      });
    };
    if (delayMs <= 0) {
      fire();
    } else {
      pendingMarkReadID = id;
      pendingMarkReadTimer = setTimeout(fire, delayMs);
    }
  }

  function loadMessage(id) {
    // The backend opt-out query param ?auto_seen=0 is only sent when
    // the user's setting is positive. delay=0 keeps the legacy
    // behaviour (server marks as seen on read AND the client also
    // patches — idempotent, no harm). delay>0 forces the client
    // into the driver seat so the backend does NOT mark-as-read on
    // the GET and the JS can honour the configured delay.
    cancelPendingMarkRead();
    var s0 = state.settings || {};
    var delay0 = (typeof s0.mark_read_delay_seconds === 'number')
      ? Math.max(0, Math.min(60, s0.mark_read_delay_seconds))
      : 0;
    var url = '/api/v1/webmail/messages/' + id;
    if (delay0 > 0) {
      url += '?auto_seen=0';
    }
    return api('GET', url).then(function (msg) {
      state.selectedMessage = msg;
      state.selectedMessageID = msg.id;
      // Mark as seen if not already.
      if (!msg.seen) {
        if (delay0 > 0) {
          scheduleMarkRead(id);
        } else {
          // delay=0 path: server already marked-as-read on the GET,
          // so no client-side action is needed. We still patch to
          // defend against a future backend change that drops the
          // implicit mark; the patch is idempotent.
          updateMessageFlags(id, { seen: true }).catch(function () {
            /* non-fatal */
          });
        }
      }
      return msg;
    });
  }

  // ──────────────────────────────────────────────────────────
  // Bulk select
  // ──────────────────────────────────────────────────────────

  // selectionCount returns the number of currently
  // selected messages. A simple helper because the
  // selection is a plain object keyed by id (a Set would
  // be cleaner but Set literal support in older browsers
  // is patchy; the count is computed on demand).
  function selectionCount() {
    return Object.keys(state.selection).length;
  }

  // isSelected reports whether the given message id is
  // currently in the selection.
  function isSelected(id) {
    return !!state.selection[id];
  }

  // toggleSelection adds or removes a single message id
  // from the selection. If this is the first toggle the
  // selectionMode flag flips on so the checkbox column
  // becomes visible on every row. shiftKey + click
  // selects a contiguous range, like the desktop
  // convention.
  function toggleSelection(id, ev) {
    var ids = state.messages.map(function (m) { return m.id; });
    if (ev && ev.shiftKey && state.lastSelectedID != null) {
      // Range select between the last clicked id and
      // this one, in list order. The starting id is
      // included.
      var from = ids.indexOf(state.lastSelectedID);
      var to = ids.indexOf(id);
      if (from >= 0 && to >= 0) {
        if (from > to) { var t = from; from = to; to = t; }
        for (var i = from; i <= to; i++) {
          state.selection[ids[i]] = true;
        }
      }
    } else {
      if (state.selection[id]) {
        delete state.selection[id];
      } else {
        state.selection[id] = true;
      }
    }
    state.lastSelectedID = id;
    state.selectionMode = selectionCount() > 0;
    renderMessageList();
    renderBulkActionBar();
  }

  // selectAllVisible checks every message currently in
  // the loaded page. The action does NOT load more pages
  // — if the user wants every message in the folder they
  // load more pages first, then select all. The
  // affordance is in the bulk action bar.
  function selectAllVisible() {
    state.messages.forEach(function (m) {
      state.selection[m.id] = true;
    });
    state.selectionMode = true;
    renderMessageList();
    renderBulkActionBar();
  }

  // clearSelection removes every selected id and exits
  // selection mode.
  function clearSelection() {
    state.selection = {};
    state.lastSelectedID = null;
    state.selectionMode = false;
    renderMessageList();
    renderBulkActionBar();
  }

  // runBatchAction issues POST /api/v1/webmail/messages/batch
  // with the current selection and the supplied action.
  // The response is partial-failure aware: per-id errors
  // are surfaced in a toast with the failed ids, and the
  // successfully-processed messages are dropped from the
  // current view so the list immediately reflects the
  // outcome. Any selection of ids that survived is
  // cleared.
  function runBatchAction(action, targetFolderID) {
    var ids = Object.keys(state.selection).map(function (k) { return parseInt(k, 10); });
    if (ids.length === 0) return Promise.resolve();
    var payload = { ids: ids, action: action };
    if (targetFolderID) payload.target_folder_id = targetFolderID;
    return api('POST', '/api/v1/webmail/messages/batch', payload).then(function (res) {
      var failed = (res && res.errors) || [];
      var failedIDs = {};
      failed.forEach(function (e) { failedIDs[e.id] = e.error; });
      // Drop successfully-processed messages from the
      // current list. Failed messages stay so the user
      // can retry.
      state.messages = state.messages.filter(function (m) {
        if (failedIDs[m.id]) return true;
        return !state.selection[m.id];
      });
      clearSelection();
      renderMessageList();
      // Surface the outcome.
      var actionLabel = ({
        archive: 'archived',
        delete: 'moved to Trash',
        move: 'moved',
        markRead: 'marked as read',
        markUnread: 'marked as unread',
        flag: 'flagged',
        unflag: 'unflagged',
        spam: 'reported as spam',
        nospam: 'marked as not spam',
      })[action] || action;
      if (failed.length === 0) {
        toast(ids.length + ' ' + (ids.length === 1 ? 'message' : 'messages') + ' ' + actionLabel, 'success');
      } else {
        toast(
          (res.succeeded || 0) + ' ' + actionLabel + '; ' + failed.length + ' failed',
          'error',
          5000
        );
      }
    }).catch(function (e) {
      toast('Batch ' + action + ' failed: ' + (e.message || 'unknown'), 'error');
    });
  }

  function updateMessageFlags(id, flags) {
    return api('PATCH', '/api/v1/webmail/messages/' + id, flags).then(function (res) {
      // Patch the local cache so the list + reading pane reflect
      // the new state immediately.
      for (var i = 0; i < state.messages.length; i++) {
        if (state.messages[i].id === id) {
          if ('seen' in flags) state.messages[i].seen = flags.seen;
          if ('flagged' in flags) state.messages[i].flagged = flags.flagged;
          if ('deleted' in flags) state.messages[i].deleted = flags.deleted;
          if ('junk' in flags) state.messages[i].junk = flags.junk;
          break;
        }
      }
      if (state.selectedMessage && state.selectedMessage.id === id) {
        if ('seen' in flags) state.selectedMessage.seen = flags.seen;
        if ('flagged' in flags) state.selectedMessage.flagged = flags.flagged;
        if ('deleted' in flags) state.selectedMessage.deleted = flags.deleted;
      }
      return res;
    });
  }

  function deleteMessage(id) {
    return api('POST', '/api/v1/webmail/messages/' + id + '/delete').then(function () {
      // Remove from current view immediately.
      state.messages = state.messages.filter(function (m) {
        return m.id !== id;
      });
      if (state.selectedMessageID === id) {
        state.selectedMessageID = null;
        state.selectedMessage = null;
        closeReadingPane();
      }
    });
  }

  function archiveMessage(id) {
    return api('POST', '/api/v1/webmail/messages/' + id + '/archive').then(function () {
      state.messages = state.messages.filter(function (m) {
        return m.id !== id;
      });
      if (state.selectedMessageID === id) {
        state.selectedMessageID = null;
        state.selectedMessage = null;
        closeReadingPane();
      }
    });
  }

  function markFolderRead(folderID) {
    return api('POST', '/api/v1/webmail/folders/' + folderID + '/read-all').then(function (res) {
      // Local update: every message in this folder is now seen.
      state.messages.forEach(function (m) {
        if (m.folder_id === folderID) m.seen = true;
      });
      // Refresh folder unread counts.
      return loadFolders().then(function () {
        return res;
      });
    });
  }

  function sendMessage(payload) {
    var body = {
      to: payload.to || '',
      cc: payload.cc || '',
      bcc: payload.bcc || '',
      subject: payload.subject || '',
      body: payload.body || '',
    };
    return api('POST', '/api/v1/webmail/send', body).then(function (res) {
      if (res.status !== 'queued') {
        throw new Error('Send returned status=' + (res.status || 'unknown'));
      }
      return res;
    });
  }

  function sendMessageWithAttachments(payload, files) {
    var fd = new FormData();
    fd.append('to', payload.to || '');
    fd.append('cc', payload.cc || '');
    fd.append('bcc', payload.bcc || '');
    fd.append('subject', payload.subject || '');
    fd.append('body', payload.body || '');
    files.forEach(function (f, i) {
      fd.append('attachment', f.file || f, f.name);
    });
    return fetch('/api/v1/webmail/send', {
      method: 'POST',
      credentials: 'include',
      headers: { Accept: 'application/json' },
      body: fd,
    }).then(function (res) {
      return res.json().then(function (data) {
        if (!res.ok) {
          var err = new Error((data && data.error) || ('HTTP ' + res.status));
          err.status = res.status;
          err.body = data;
          throw err;
        }
        return data;
      });
    }).then(function (res) {
      if (res.status !== 'queued') {
        throw new Error('Send returned status=' + (res.status || 'unknown'));
      }
      return res;
    });
  }

  function saveDraft(payload) {
    var body = {
      to: payload.to || '',
      cc: payload.cc || '',
      bcc: payload.bcc || '',
      subject: payload.subject || '',
      body: payload.body || '',
    };
    if (payload.draftID) body.id = payload.draftID;
    var method = payload.draftID ? 'PUT' : 'POST';
    var path = payload.draftID
      ? '/api/v1/webmail/drafts/' + payload.draftID
      : '/api/v1/webmail/drafts';
    return api(method, path, body);
  }

  function deleteDraft(id) {
    return api('DELETE', '/api/v1/webmail/drafts/' + id);
  }

  // ──────────────────────────────────────────────────────────
  // DOM render
  // ──────────────────────────────────────────────────────────

  var els = {};
  function $(id) {
    return document.getElementById(id);
  }

  function renderShell() {
    document.body.innerHTML = '';

    // Topbar.
    var top = document.createElement('header');
    top.className = 'topbar';
    top.innerHTML = '';
    top.appendChild(el('button', {
      class: 'menu-btn',
      id: 'menu-btn',
      'aria-label': 'Toggle folder sidebar',
      type: 'button',
      title: 'Toggle folders',
    }, '☰'));
    var brand = el('div', { class: 'brand' });
    var logo = el('span', { class: 'logo' }, 'O');
    brand.appendChild(logo);
    var brandText = el('span', {}, 'Orvix Webmail');
    brand.appendChild(brandText);
    top.appendChild(brand);

    var search = el('div', { class: 'search' });
    var searchIcon = el('span', { class: 'icon', 'aria-hidden': 'true' }, '🔍');
    search.appendChild(searchIcon);
    var searchInput = el('input', {
      id: 'search-input',
      type: 'search',
      placeholder: 'Search subject, sender, recipient…',
      autocomplete: 'off',
      spellcheck: 'false',
    });
    search.appendChild(searchInput);
    var searchClear = el('button', {
      class: 'clear',
      id: 'search-clear',
      type: 'button',
      'aria-label': 'Clear search',
      title: 'Clear',
    }, '×');
    searchClear.hidden = true;
    search.appendChild(searchClear);
    top.appendChild(search);

    var actions = el('div', { class: 'actions' });
    var settingsBtn = el('button', {
      class: 'icon-btn',
      id: 'settings-btn',
      type: 'button',
      title: 'Settings',
      'aria-label': 'Open settings',
    }, '⚙');
    actions.appendChild(settingsBtn);
    var userChip = el('div', { class: 'user-chip', id: 'user-chip', tabindex: '0', role: 'button', 'aria-label': 'Open settings menu' });
    userChip.appendChild(el('span', { class: 'avatar', id: 'user-avatar' }, '?'));
    userChip.appendChild(el('span', { id: 'user-email' }, ''));
    actions.appendChild(userChip);
    top.appendChild(actions);

    document.body.appendChild(top);

    // App shell.
    var app = el('div', { class: 'app', id: 'app' });

    // Sidebar.
    var sidebar = el('aside', { class: 'sidebar', id: 'sidebar' });
    var composeWrap = el('div', { class: 'compose-btn-wrap' });
    var composeBtn = el('button', {
      class: 'btn primary compose-btn',
      id: 'compose-btn',
      type: 'button',
    }, '✏  New Message');
    composeWrap.appendChild(composeBtn);
    sidebar.appendChild(composeWrap);
    var nav = el('nav', { id: 'folders-nav' });
    sidebar.appendChild(nav);
    var footer = el('div', { class: 'footer' });
    footer.appendChild(el('span', {}, 'Orvix Mail v1'));
    sidebar.appendChild(footer);
    app.appendChild(sidebar);

    // List pane.
    var list = el('section', { class: 'list-pane', id: 'list-pane' });
    var listHeader = el('div', { class: 'list-header' });
    listHeader.appendChild(el('div', { class: 'title', id: 'list-title' }, ''));
    var listToolbar = el('div', { class: 'list-toolbar' });
    listToolbar.appendChild(el('button', {
      class: 'icon-btn',
      id: 'refresh-btn',
      type: 'button',
      title: 'Refresh',
      'aria-label': 'Refresh',
    }, '⟳'));
    listToolbar.appendChild(el('button', {
      class: 'icon-btn',
      id: 'mark-all-read-btn',
      type: 'button',
      title: 'Mark all as read',
      'aria-label': 'Mark all as read',
    }, '✓✓'));
    var spacer = el('div', { class: 'spacer' });
    listToolbar.appendChild(spacer);
    list.appendChild(listHeader);
    list.appendChild(listToolbar);
    var msgs = el('div', { class: 'messages', id: 'messages' });
    list.appendChild(msgs);
    app.appendChild(list);

    // Reading pane.
    var reading = el('section', { class: 'reading-pane', id: 'reading-pane' });
    var rtoolbar = el('div', { class: 'toolbar' });
    var backBtn = el('button', {
      class: 'icon-btn back-btn',
      id: 'back-btn',
      type: 'button',
      title: 'Back to list',
      'aria-label': 'Back to list',
    }, '←');
    rtoolbar.appendChild(backBtn);
    var rspacer = el('div', { class: 'spacer' });
    rtoolbar.appendChild(rspacer);
    reading.appendChild(rtoolbar);
    var msgView = el('div', { class: 'message-view', id: 'message-view' });
    reading.appendChild(msgView);
    app.appendChild(reading);

    document.body.appendChild(app);

    // Toasts container.
    ensureToastContainer();

    // Bind events.
    $('menu-btn').addEventListener('click', toggleSidebar);
    $('compose-btn').addEventListener('click', function () {
      openCompose({ mode: 'new' });
    });
    $('settings-btn').addEventListener('click', openSettingsModal);
    $('user-chip').addEventListener('click', openSettingsModal);
    $('user-chip').addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openSettingsModal();
      }
    });
    $('refresh-btn').addEventListener('click', function () {
      var folder = state.currentFolderPath;
      var q = state.searchQuery;
      loadMessages({ folder: folder, q: q })
        .then(function () {
          renderMessageList();
          toast('Refreshed', 'success', 1500);
        })
        .catch(function (e) {
          toast('Refresh failed: ' + e.message, 'error');
        });
    });
    $('mark-all-read-btn').addEventListener('click', function () {
      var folder = state.folderByPath[state.currentFolderPath.toLowerCase()];
      if (!folder) return;
      markFolderRead(folder.id)
        .then(function () {
          state.messages.forEach(function (m) {
            m.seen = true;
          });
          renderMessageList();
          toast('Marked ' + state.messages.length + ' messages as read', 'success', 1500);
        })
        .catch(function (e) {
          toast('Mark all read failed: ' + e.message, 'error');
        });
    });
    $('back-btn').addEventListener('click', closeReadingPane);
    searchInput.addEventListener('input', onSearchInput);
    searchClear.addEventListener('click', function () {
      searchInput.value = '';
      searchClear.hidden = true;
      state.searchQuery = '';
      renderFolderSidebar();
      routeFromHash();
    });

    window.addEventListener('hashchange', routeFromHash);
  }

  function el(tag, attrs, text) {
    var e = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        if (k === 'class') e.className = attrs[k];
        else if (k === 'html') e.innerHTML = attrs[k];
        else e.setAttribute(k, attrs[k]);
      });
    }
    if (text != null) e.appendChild(document.createTextNode(text));
    return e;
  }

  function renderFolderSidebar() {
    var nav = $('folders-nav');
    if (!nav) return;
    nav.innerHTML = '';
    var label = el('div', { class: 'group-label' }, 'Folders');
    nav.appendChild(label);

    // System folders first, in a fixed order; then any
    // user-created folders at the end.
    var order = ['inbox', 'sent', 'drafts', 'archive', 'junk', 'trash'];
    var byPath = state.folderByPath;
    order.forEach(function (key) {
      var f = byPath[key];
      if (!f) return;
      nav.appendChild(folderRow(f));
    });
    var seen = {};
    order.forEach(function (k) {
      seen[k] = true;
    });
    state.folders.forEach(function (f) {
      var key = (f.path || f.name || '').toLowerCase();
      if (seen[key]) return;
      seen[key] = true;
      nav.appendChild(folderRow(f));
    });

    if (state.searchQuery) {
      var searchLabel = el('div', { class: 'group-label' }, 'Search results');
      nav.appendChild(searchLabel);
      var searchFolder = el('div', { class: 'folder active' });
      searchFolder.innerHTML =
        '<span class="icon">🔍</span>' +
        '<span class="label">"' +
        escapeHTML(state.searchQuery) +
        '"</span>';
      nav.appendChild(searchFolder);
    }
  }

  function folderRow(f) {
    var path = f.path || f.name || '';
    var active = state.searchQuery
      ? false
      : path.toLowerCase() === (state.currentFolderPath || '').toLowerCase();
    var row = el('div', {
      class: 'folder' + (active ? ' active' : ''),
      'data-folder': path,
      role: 'button',
      tabindex: '0',
    });
    var icon = el('span', { class: 'icon' }, folderIcon(path));
    row.appendChild(icon);
    var label = el('span', { class: 'label' }, f.name || path);
    row.appendChild(label);
    var unread = (f.unread_count != null ? f.unread_count : 0) | 0;
    if (unread > 0) {
      row.appendChild(el('span', { class: 'count' }, String(unread)));
    }
    row.addEventListener('click', function () {
      navigateToFolder(path);
    });
    row.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault();
        navigateToFolder(path);
      }
    });
    return row;
  }

  function folderIcon(path) {
    var p = (path || '').toLowerCase();
    if (p === 'inbox') return '📥';
    if (p === 'sent') return '📤';
    if (p === 'drafts') return '📝';
    if (p === 'archive') return '🗄';
    if (p === 'junk' || p === 'spam') return '🚫';
    if (p === 'trash') return '🗑';
    return '📁';
  }

  function renderMessageList() {
    var msgs = $('messages');
    if (!msgs) return;
    msgs.innerHTML = '';
    // The selection-mode class on the message list
    // reserves a checkbox column on every row. The
    // body class is the same signal at the document
    // level for CSS that styles the layout outside
    // the list (bulk bar, etc.). Both must move
    // together.
    if (state.selectionMode) {
      msgs.classList.add('selection-mode');
      document.body.classList.add('selection-mode-on');
    } else {
      msgs.classList.remove('selection-mode');
      document.body.classList.remove('selection-mode-on');
    }
    var title = $('list-title');
    if (state.searchQuery) {
      title.textContent = 'Search: ' + state.searchQuery;
    } else {
      var f = state.folderByPath[state.currentFolderPath.toLowerCase()];
      title.textContent = f ? f.name : state.currentFolderPath;
    }

    // The list header carries the "Select" toggle and
    // the "select all visible" affordance when the user
    // is in selection mode. The action bar (rendered
    // separately) sits below this header.
    renderListHeader(msgs);

    if (state.messagesLoading && state.messages.length === 0) {
      // Render a skeleton list — three ghost rows so
      // the pane has visible structure during load. The
      // skeleton-pulse CSS handles the animation; the
      // reduced-motion media query disables it.
      for (var i = 0; i < 6; i++) {
        msgs.appendChild(skeletonMessageRow());
      }
      return;
    }
    if (state.messages.length === 0) {
      msgs.appendChild(renderEmptyState());
      return;
    }

    state.messages.forEach(function (m) {
      msgs.appendChild(messageRow(m));
    });

    if (state.messagesHasMore) {
      // The load-more affordance is a real button now,
      // not a static div. The click handler triggers
      // loadMoreMessages(); while the request is in
      // flight the button is disabled and shows a
      // spinner. This is the wire-up the previous
      // version was missing.
      var more = el('button', {
        class: 'load-more-btn',
        type: 'button',
        id: 'load-more',
        disabled: state.messagesLoading,
      });
      if (state.messagesLoading) {
        more.appendChild(el('span', { class: 'spinner-inline' }));
        more.appendChild(document.createTextNode(' Loading…'));
      } else {
        more.textContent = 'Load more';
      }
      more.addEventListener('click', function (ev) {
        ev.preventDefault();
        loadMoreMessages();
      });
      msgs.appendChild(more);
    }
  }

  // renderListHeader writes the "Select" toggle and the
  // "Select all visible" affordance at the top of the
  // message list. Hidden when the user has not entered
  // selection mode (no messages ticked). Toggling the
  // header's "Select" button puts the user in selection
  // mode without ticking any message; clicking again
  // leaves selection mode.
  function renderListHeader(msgs) {
    var header = el('div', { class: 'list-header' });
    var left = el('div', { class: 'lh-left' });
    if (state.selectionMode) {
      var allBtn = el('button', {
        class: 'link-btn',
        type: 'button',
        title: 'Tick every message in this page',
      }, 'Select all');
      allBtn.addEventListener('click', function () { selectAllVisible(); });
      left.appendChild(allBtn);
      var n = selectionCount();
      var lbl = el('span', { class: 'lh-count' }, n + ' selected');
      left.appendChild(lbl);
    } else {
      var selectBtn = el('button', {
        class: 'link-btn',
        type: 'button',
        title: 'Enter selection mode',
      }, 'Select');
      selectBtn.addEventListener('click', function () {
        state.selectionMode = true;
        renderMessageList();
        renderBulkActionBar();
      });
      left.appendChild(selectBtn);
    }
    header.appendChild(left);
    msgs.appendChild(header);
  }

  // renderBulkActionBar draws the floating bottom action
  // bar when at least one message is selected. The bar
  // exposes archive / delete / mark read / mark unread /
  // report spam / not spam / move to… and a Clear
  // selection chip. The bar is fixed to the bottom of
  // the viewport; the inline class is set so it can be
  // styled for mobile.
  function renderBulkActionBar() {
    var bar = $('bulk-action-bar');
    if (!bar) {
      bar = el('div', { id: 'bulk-action-bar', class: 'bulk-bar', role: 'toolbar' });
      document.body.appendChild(bar);
    }
    if (!state.selectionMode || selectionCount() === 0) {
      bar.classList.remove('visible');
      return;
    }
    bar.innerHTML = '';
    var count = selectionCount();
    bar.appendChild(
      el('span', { class: 'bulk-count' }, count + ' selected')
    );
    function btn(label, title, action, targetFolderID) {
      var b = el('button', {
        class: 'btn',
        type: 'button',
        title: title,
      }, label);
      b.addEventListener('click', function () { runBatchAction(action, targetFolderID); });
      return b;
    }
    bar.appendChild(btn('Archive', 'Archive selected', 'archive'));
    bar.appendChild(btn('Delete', 'Move to Trash', 'delete'));
    bar.appendChild(btn('Read', 'Mark as read', 'markRead'));
    bar.appendChild(btn('Unread', 'Mark as unread', 'markUnread'));
    bar.appendChild(btn('Spam', 'Report as spam', 'spam'));
    // Move to… — picker. We render a small inline
    // <select> so the user can pick the destination
    // folder without leaving the bar.
    var moveWrap = el('div', { class: 'bulk-move' });
    var moveSelect = el('select', { 'aria-label': 'Move to folder' });
    state.folders.forEach(function (f) {
      var opt = el('option', { value: f.id }, f.name || f.path);
      moveSelect.appendChild(opt);
    });
    moveSelect.addEventListener('change', function () {
      if (!moveSelect.value) return;
      runBatchAction('move', parseInt(moveSelect.value, 10));
      moveSelect.value = '';
    });
    moveWrap.appendChild(moveSelect);
    bar.appendChild(moveWrap);
    var clearBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Clear');
    clearBtn.addEventListener('click', function () { clearSelection(); });
    bar.appendChild(clearBtn);
    bar.classList.add('visible');
  }

  function messageRow(m) {
    var row = el('div', {
      class:
        'message' +
        (m.seen ? '' : ' unread') +
        (state.selectedMessageID === m.id ? ' selected' : '') +
        (isSelected(m.id) ? ' checked' : ''),
      'data-id': m.id,
      role: 'button',
      tabindex: '0',
    });
    // Checkbox column. Always rendered (visibility
    // controlled by CSS so the column reserves space
    // only when selectionMode is on). The checkbox
    // toggles selection without opening the message.
    if (state.selectionMode) {
      var cb = el('input', {
        type: 'checkbox',
        class: 'row-check',
        'aria-label': 'Select message',
        tabindex: '-1',
      });
      cb.checked = isSelected(m.id);
      cb.addEventListener('click', function (ev) {
        ev.stopPropagation();
        toggleSelection(m.id, ev);
      });
      cb.addEventListener('change', function (ev) {
        // change is fired in addition to click on some
        // browsers; toggleSelection already handled it.
        ev.stopPropagation();
      });
      row.appendChild(cb);
    }
    var star = el('button', {
      class: 'star' + (m.flagged ? ' on' : ''),
      type: 'button',
      title: m.flagged ? 'Unstar' : 'Star',
      'aria-label': m.flagged ? 'Unstar message' : 'Star message',
    }, m.flagged ? '★' : '☆');
    star.addEventListener('click', function (ev) {
      ev.stopPropagation();
      updateMessageFlags(m.id, { flagged: !m.flagged }).then(function () {
        renderMessageList();
        if (state.selectedMessageID === m.id) renderReadingPane();
      });
    });
    row.appendChild(star);

    var body = el('div', { class: 'body' });
    var fromName = displayName(m.from);
    var fromEmail = emailOf(m.from);
    // sender_display is the user-configurable preference for how
    // the sender cell is rendered in the list. formatSender
    // encapsulates the name / email / name_email logic so the
    // list row, the reading pane, and the reply-quote header can
    // all stay consistent. The .badge email appendage is kept for
    // 'name' mode (the historical layout) and suppressed for
    // 'email' / 'name_email' because the email is already in the
    // primary cell.
    var senderPref = (state.settings && state.settings.sender_display) || 'name';
    var fromRow = el('div', { class: 'from', dir: dirAuto(formatSender(m.from)) });
    if (senderPref === 'name') {
      fromRow.appendChild(el('span', { class: 'name' }, fromName || fromEmail || '(unknown)'));
      if (fromEmail && fromName && fromEmail.indexOf(fromName) < 0) {
        fromRow.appendChild(
          el('span', { class: 'badge', dir: 'ltr' }, fromEmail)
        );
      }
    } else {
      fromRow.appendChild(el('span', { class: 'name' }, formatSender(m.from)));
    }
    body.appendChild(fromRow);
    var subj = m.subject || '(no subject)';
    body.appendChild(el('div', { class: 'subject', dir: dirAuto(subj) }, subj));
    var preview = m.preview || (m.snippet || snippetFromMessage(m));
    body.appendChild(
      el('div', { class: 'preview', dir: dirAuto(preview) }, preview || ' ')
    );
    row.appendChild(body);

    var meta = el('div', { class: 'meta' });
    meta.appendChild(
      el('div', {}, formatDate(m.received_date || m.message_date))
    );
    var icons = el('div', { class: 'icons' });
    if (m.attachment_count && m.attachment_count > 0) {
      icons.appendChild(el('span', { class: 'att', title: 'Has attachment' }, '📎'));
    }
    if (m.flagged) icons.appendChild(el('span', { title: 'Starred' }, '☆'));
    meta.appendChild(icons);
    row.appendChild(meta);

    row.addEventListener('click', function (ev) {
      // If the user clicked the checkbox, selection
      // already handled it. If they're in selection
      // mode, a row click toggles instead of opening
      // the message (the standard mail-client pattern).
      if (ev.target && ev.target.classList && ev.target.classList.contains('row-check')) return;
      if (state.selectionMode) {
        toggleSelection(m.id, ev);
        return;
      }
      openMessage(m.id);
    });
    row.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault();
        if (state.selectionMode) {
          toggleSelection(m.id, ev);
        } else {
          openMessage(m.id);
        }
      }
    });
    return row;
  }

  function snippetFromMessage(m) {
    // Server doesn't always ship a preview field; build a
    // minimal one from subject + sender so the row is never
    // empty.
    var s = m.subject || '';
    return s;
  }

  // skeletonMessageRow returns a placeholder row that
  // mimics the shape of a real message row — checkbox
  // column (reserved), star, from, subject, preview,
  // meta. Every visible element is a .skeleton bar
  // animated by the skeleton-pulse keyframes.
  function skeletonMessageRow() {
    var row = el('div', { class: 'message', 'aria-hidden': 'true' });
    var cbSpacer = el('div', { class: 'skeleton', style: 'width:16px;height:16px;border-radius:3px;margin-top:4px;display:none;' });
    row.appendChild(cbSpacer);
    var star = el('div', { class: 'skeleton', style: 'width:18px;height:18px;border-radius:50%;margin-top:2px;' });
    row.appendChild(star);
    var body = el('div', { class: 'body' });
    body.appendChild(el('div', { class: 'skeleton skeleton-line medium', style: 'margin-top:0;' }));
    body.appendChild(el('div', { class: 'skeleton skeleton-line long' }));
    body.appendChild(el('div', { class: 'skeleton skeleton-line long' }));
    body.appendChild(el('div', { class: 'skeleton skeleton-line short' }));
    row.appendChild(body);
    var meta = el('div', { class: 'meta' });
    meta.appendChild(el('div', { class: 'skeleton skeleton-line', style: 'width:48px;height:10px;' }));
    meta.appendChild(el('div', { class: 'skeleton skeleton-line', style: 'width:36px;height:10px;' }));
    row.appendChild(meta);
    return row;
  }

  // renderEmptyState returns a richer empty-state node
  // with a circular illustration, a short title, and a
  // helpful subtitle. The copy varies by folder so the
  // user is not left guessing.
  function renderEmptyState() {
    var wrap = el('div', { class: 'empty-state' });
    wrap.appendChild(el('div', { class: 'empty-illustration', 'aria-hidden': 'true' }, emptyIcon()));
    var f = state.searchQuery
      ? null
      : state.folderByPath[(state.currentFolderPath || '').toLowerCase()];
    var folderKey = f ? (f.path || f.name || '').toLowerCase() : '';
    var title, subtitle;
    if (state.searchQuery) {
      title = 'No matches';
      subtitle = 'No messages match "' + state.searchQuery + '".';
    } else if (folderKey === 'inbox') {
      title = 'Inbox zero';
      subtitle = "You're all caught up. New mail will land here.";
    } else if (folderKey === 'sent') {
      title = 'Nothing sent yet';
      subtitle = 'Messages you send will appear here.';
    } else if (folderKey === 'drafts') {
      title = 'No drafts';
      subtitle = 'Drafts you save will appear here.';
    } else if (folderKey === 'trash') {
      title = 'Trash is empty';
      subtitle = 'Deleted messages stay here until you empty it.';
    } else if (folderKey === 'archive') {
      title = 'No archived messages';
      subtitle = 'Messages you archive will appear here.';
    } else if (folderKey === 'junk' || folderKey === 'spam') {
      title = 'No junk mail';
      subtitle = "Good news — nothing in junk right now.";
    } else {
      title = 'Nothing here';
      subtitle = 'This folder is empty.';
    }
    wrap.appendChild(el('div', { class: 'empty-title' }, title));
    wrap.appendChild(el('div', {}, subtitle));
    return wrap;
  }

  // emptyIcon picks a glyph that matches the current
  // context — search magnifier, folder, trash — so the
  // illustration is meaningful rather than decorative.
  function emptyIcon() {
    if (state.searchQuery) return '🔍';
    var k = (state.currentFolderPath || '').toLowerCase();
    if (k === 'inbox') return '📥';
    if (k === 'sent') return '📤';
    if (k === 'drafts') return '📝';
    if (k === 'trash') return '🗑';
    if (k === 'archive') return '🗄';
    if (k === 'junk' || k === 'spam') return '🚫';
    return '📭';
  }

  // renderReadingPaneEmpty returns the polished empty
  // state for the reading pane — replaces the bare
  // "Select a message to read." copy with an illustration
  // + inviting title + helpful subtitle.
  function renderReadingPaneEmpty() {
    var wrap = el('div', { class: 'empty-state' });
    wrap.appendChild(el('div', { class: 'empty-illustration', 'aria-hidden': 'true' }, '📨'));
    wrap.appendChild(el('div', { class: 'empty-title' }, 'Select a message'));
    wrap.appendChild(el(
      'div',
      {},
      'Choose a message from the list to read it here. Reply, forward, and search are just a keystroke away.'
    ));
    return wrap;
  }

  // moveSelection walks the current list up or down
  // by `delta` and opens the resulting message. The
  // current message id is taken from
  // state.selectedMessageID; if nothing is selected we
  // start at the top of the list (delta = 1) or the
  // bottom (delta = -1).
  function moveSelection(delta) {
    if (!state.messages || state.messages.length === 0) return;
    var idx = -1;
    if (state.selectedMessageID) {
      for (var i = 0; i < state.messages.length; i++) {
        if (state.messages[i].id === state.selectedMessageID) {
          idx = i;
          break;
        }
      }
    }
    var next = idx + delta;
    if (next < 0) next = 0;
    if (next >= state.messages.length) next = state.messages.length - 1;
    var m = state.messages[next];
    if (!m) return;
    openMessage(m.id);
  }

  // showShortcutsOverlay renders a small modal listing
  // every keyboard shortcut the client supports. The
  // overlay is a one-off element that is rebuilt each
  // time the user presses "?" — the cost is trivial
  // and the contents are static.
  function showShortcutsOverlay() {
    var existing = document.querySelector('.shortcuts-overlay');
    if (existing) {
      existing.remove();
      return;
    }
    var overlay = el('div', { class: 'shortcuts-overlay' });
    var panel = el('div', { class: 'shortcuts-panel', role: 'dialog' });
    panel.appendChild(el('h2', {}, 'Keyboard shortcuts'));
    var rows = [
      ['c', 'Compose new message'],
      ['/', 'Focus search'],
      ['r', 'Reply'],
      ['a', 'Reply all'],
      ['f', 'Forward'],
      ['j / Down', 'Next message'],
      ['k / Up', 'Previous message'],
      ['e', 'Archive current message'],
      ['# / Delete', 'Move to Trash'],
      ['s', 'Star / unstar current message'],
      ['x', 'Toggle selection (selection mode only)'],
      ['g i', 'Go to Inbox'],
      ['g s', 'Go to Sent'],
      ['g d', 'Go to Drafts'],
      ['g a', 'Go to Archive'],
      ['g t', 'Go to Trash'],
      ['g j', 'Go to Junk'],
      ['?', 'Show / hide this overlay'],
      ['Esc', 'Close modal, cancel selection, or close reading pane'],
    ];
    var table = el('table', { class: 'shortcuts-table' });
    rows.forEach(function (r) {
      var tr = el('tr');
      tr.appendChild(el('td', { class: 'key' }, r[0]));
      tr.appendChild(el('td', {}, r[1]));
      table.appendChild(tr);
    });
    panel.appendChild(table);
    panel.appendChild(el('div', { class: 'shortcuts-hint' },
      'Press Esc or ? again to close this overlay.'));
    overlay.appendChild(panel);
    overlay.addEventListener('click', function (ev) {
      if (ev.target === overlay) overlay.remove();
    });
    document.body.appendChild(overlay);
  }

  function openMessage(id) {
    state.selectedMessageID = id;
    state.readingPaneOpen = true;
    if (els.readingPane) els.readingPane.classList.add('open');
    renderMessageList(); // refresh selected highlight
    renderReadingPaneLoading();
    loadMessage(id)
      .then(function () {
        renderReadingPane();
      })
      .catch(function (e) {
        renderReadingPaneError(e);
      });
  }

  function renderReadingPaneLoading() {
    var view = $('message-view');
    if (!view) return;
    view.innerHTML = '';
    // Skeleton header + body so the pane keeps its
    // proportions while the request is in flight. The
    // existing toolbar action buttons are still torn
    // down because they would reference an unloaded
    // message.
    var wrap = el('div', { class: 'reading-skeleton', 'aria-hidden': 'true' });
    var hdr = el('div', { class: 'header' });
    hdr.appendChild(el('div', { class: 'skeleton skeleton-line', style: 'height:22px;width:60%;margin-bottom:18px;' }));
    var meta = el('div', { class: 'meta-row' });
    meta.appendChild(el('div', { class: 'skeleton skeleton-circle' }));
    var fromBlock = el('div', { class: 'from-block' });
    fromBlock.appendChild(el('div', { class: 'skeleton skeleton-line medium', style: 'margin-top:0;' }));
    fromBlock.appendChild(el('div', { class: 'skeleton skeleton-line short' }));
    meta.appendChild(fromBlock);
    meta.appendChild(el('div', { class: 'skeleton skeleton-line', style: 'width:90px;height:10px;align-self:center;' }));
    hdr.appendChild(meta);
    wrap.appendChild(hdr);
    var body = el('div', { class: 'body' });
    for (var i = 0; i < 8; i++) {
      body.appendChild(el('div', { class: 'skeleton skeleton-line long' }));
    }
    body.appendChild(el('div', { class: 'skeleton skeleton-line medium' }));
    wrap.appendChild(body);
    view.appendChild(wrap);
    var tb = view.parentNode.querySelector('.toolbar');
    if (tb) tb.querySelectorAll('.action-btn, .move-to-wrap').forEach(function (b) {
      b.remove();
    });
  }

  function renderReadingPaneError(err) {
    var view = $('message-view');
    if (!view) return;
    view.innerHTML = '';
    var wrap = el('div', { class: 'error-state' });
    wrap.appendChild(el('div', { class: 'empty-illustration', 'aria-hidden': 'true' }, '⚠'));
    wrap.appendChild(el('div', { class: 'empty-title' }, "Couldn't load message"));
    wrap.appendChild(el('div', {}, (err && err.message) || 'Unknown error'));
    // The retry button re-issues the same request so
    // transient network failures do not force a full
    // reload.
    var retry = el('button', { class: 'btn sm', type: 'button' }, 'Retry');
    retry.addEventListener('click', function () {
      if (state.selectedMessageID) {
        renderReadingPaneLoading();
        loadMessage(state.selectedMessageID)
          .then(renderReadingPane)
          .catch(renderReadingPaneError);
      }
    });
    wrap.appendChild(retry);
    view.appendChild(wrap);
  }

  function renderReadingPane() {
    var view = $('message-view');
    if (!view) return;
    var msg = state.selectedMessage;
    if (!msg) {
      view.innerHTML = '';
      view.appendChild(renderReadingPaneEmpty());
      return;
    }

    // Toolbar actions.
    var readingPane = view.parentNode;
    var tb = readingPane.querySelector('.toolbar');
    // Teardown targets every dynamic toolbar child so a
    // re-render never compounds the prior one. The
    // .move-to-wrap div is the easy thing to forget — it
    // is not an .action-btn, so omitting it from the
    // selector leaks a fresh "Move to…" <select> into the
    // toolbar on every in-pane action (star toggle, mark
    // read/unread, spam toggle) and on every cross-message
    // navigation that goes through the loading skeleton.
    tb.querySelectorAll('.action-btn, .move-to-wrap').forEach(function (b) {
      b.remove();
    });
    function actBtn(label, title, fn) {
      var b = el('button', {
        class: 'icon-btn action-btn',
        type: 'button',
        title: title,
      }, label);
      b.addEventListener('click', fn);
      tb.appendChild(b);
    }
    actBtn('↩', 'Reply', function () {
      openCompose({ mode: 'reply', replyTo: msg });
    });
    actBtn('⇆', 'Reply all', function () {
      openCompose({ mode: 'replyAll', replyTo: msg });
    });
    actBtn('→', 'Forward', function () {
      openCompose({ mode: 'forward', replyTo: msg });
    });
    actBtn('🗑', 'Delete', function () {
      if (!confirm('Move this message to Trash?')) return;
      deleteMessage(msg.id)
        .then(function () {
          toast('Moved to Trash', 'success');
          renderMessageList();
          closeReadingPane();
        })
        .catch(function (e) {
          toast('Delete failed: ' + e.message, 'error');
        });
    });
    actBtn('🗄', 'Archive', function () {
      archiveMessage(msg.id)
        .then(function () {
          toast('Archived', 'success');
          renderMessageList();
          closeReadingPane();
        })
        .catch(function (e) {
          toast('Archive failed: ' + e.message, 'error');
        });
    });
    // "Show original" downloads the raw RFC822 as a
    // .eml file. The endpoint is the same source
    // download used by the admin queue debug page; the
    // browser saves the file because the response sets
    // Content-Disposition: attachment.
    actBtn('⤓', 'Show original', function () {
      var a = document.createElement('a');
      a.href = '/api/v1/webmail/messages/' + msg.id + '/source';
      a.rel = 'noopener';
      a.download = 'message-' + msg.id + '.eml';
      document.body.appendChild(a);
      a.click();
      setTimeout(function () { a.remove(); }, 0);
    });
    // "Move to…" lets the user pick a destination
    // folder other than Archive / Trash. Renders an
    // inline <select> so the move can be issued from
    // the toolbar without a second modal.
    var moveWrap = el('div', { class: 'move-to-wrap' });
    var moveSelect = el('select', { 'aria-label': 'Move to folder', title: 'Move to folder' });
    moveSelect.appendChild(el('option', { value: '' }, 'Move to…'));
    state.folders.forEach(function (f) {
      var opt = el('option', { value: f.id }, f.name || f.path);
      moveSelect.appendChild(opt);
    });
    moveSelect.addEventListener('change', function () {
      if (!moveSelect.value) return;
      var targetID = parseInt(moveSelect.value, 10);
      api('POST', '/api/v1/webmail/messages/' + msg.id + '/move',
        { target_folder_id: targetID })
        .then(function (res) {
          toast('Moved to ' + (res.moved_to || 'folder'), 'success');
          renderMessageList();
          closeReadingPane();
        })
        .catch(function (e) {
          toast('Move failed: ' + e.message, 'error');
        });
      moveSelect.value = '';
    });
    moveWrap.appendChild(moveSelect);
    tb.appendChild(moveWrap);
    // "Spam" / "Not spam" toggle depending on the
    // current junk flag. Both call the same
    // /messages/batch endpoint with action=spam /
    // nospam so the same code path the bulk action
    // bar uses also powers this single-message action.
    if (msg.junk) {
      actBtn('✓', 'Not spam', function () {
        runBatchAction('nospam').then(function () {
          if (state.selectedMessage) state.selectedMessage.junk = false;
          renderReadingPane();
        });
      });
    } else {
      actBtn('⚑', 'Report spam', function () {
        runBatchAction('spam').then(function () {
          if (state.selectedMessage) state.selectedMessage.junk = true;
          closeReadingPane();
        });
      });
    }
    if (msg.seen) {
      actBtn('●', 'Mark unread', function () {
        updateMessageFlags(msg.id, { seen: false }).then(function () {
          msg.seen = false;
          toast('Marked unread', 'success', 1200);
          renderMessageList();
          renderReadingPane();
        });
      });
    } else {
      actBtn('○', 'Mark read', function () {
        updateMessageFlags(msg.id, { seen: true }).then(function () {
          msg.seen = true;
          renderMessageList();
          renderReadingPane();
        });
      });
    }
    actBtn(msg.flagged ? '★' : '☆', msg.flagged ? 'Unstar' : 'Star', function () {
      updateMessageFlags(msg.id, { flagged: !msg.flagged }).then(function () {
        msg.flagged = !msg.flagged;
        renderMessageList();
        renderReadingPane();
      });
    });

    view.innerHTML = '';

    var header = el('div', { class: 'header' });
    var h1 = el('h1', { dir: dirAuto(msg.subject || '') }, msg.subject || '(no subject)');
    header.appendChild(h1);

    var meta = el('div', { class: 'meta-row' });
    var avatar = el('div', { class: 'avatar-lg' }, initials(displayName(msg.from), emailOf(msg.from)));
    meta.appendChild(avatar);
    var fromBlock = el('div', { class: 'from-block' });
    // sender_display drives the reading pane header in the same
    // way as the list row. 'name' shows the display name + the
    // email address as a smaller line below; 'email' shows just
    // the email; 'name_email' shows "Name <email>" in one line.
    var pref = (state.settings && state.settings.sender_display) || 'name';
    var fromNameText = displayName(msg.from) || emailOf(msg.from) || '(unknown sender)';
    var fromEmailText = emailOf(msg.from);
    if (pref === 'email') {
      fromBlock.appendChild(
        el('div', { class: 'from-name', dir: dirAuto(fromEmailText || '(unknown)') },
          fromEmailText || '(unknown)')
      );
    } else if (pref === 'name_email') {
      fromBlock.appendChild(
        el('div', { class: 'from-name', dir: dirAuto(formatSender(msg.from)) },
          formatSender(msg.from))
      );
    } else {
      fromBlock.appendChild(
        el('div', { class: 'from-name', dir: dirAuto(fromNameText) }, fromNameText)
      );
      if (fromEmailText && fromEmailText !== fromNameText) {
        fromBlock.appendChild(
          el('div', { class: 'from-email', dir: 'ltr' }, fromEmailText)
        );
      }
    }
    meta.appendChild(fromBlock);
    var dateText = formatFullDate(msg.received_date || msg.message_date);
    meta.appendChild(el('div', { class: 'date' }, dateText));
    header.appendChild(meta);
    view.appendChild(header);

    var details = el('div', { class: 'details' });
    function detailRow(label, value, dir) {
      if (!value) return;
      var row = el('div', { class: 'row' });
      row.appendChild(el('div', { class: 'label' }, label));
      var v = el('div', { class: 'value' });
      v.setAttribute('dir', dir || dirAuto(value));
      v.textContent = value;
      row.appendChild(v);
      details.appendChild(row);
    }
    detailRow('To', msg.to);
    if (msg.cc) detailRow('Cc', msg.cc);
    if (msg.bcc) detailRow('Bcc', msg.bcc);
    detailRow('Date', formatFullDate(msg.message_date || msg.received_date), 'ltr');
    if (msg.internet_id) detailRow('Message-ID', msg.internet_id, 'ltr');
    view.appendChild(details);

    var body = el('div', { class: 'body' });
    body.setAttribute('dir', 'auto');
    body.innerHTML = renderBody(msg.rfc822 || '');
    view.appendChild(body);

    // Attachments: present in the API response if any. The
    // server returns {id, filename, content_type,
    // size_bytes}; the download endpoint requires the id
    // (parsed as uint) and is the only path that opens the
    // file. A safe content-type allowlist (PNG/JPEG/GIF/
    // WebP/text) renders an inline "Preview" link;
    // everything else is download-only.
    if (msg.attachments && msg.attachments.length > 0) {
      var att = el('div', { class: 'attachments' });
      att.appendChild(el('h3', {}, pluralize(msg.attachments.length, 'Attachment', 'Attachments')));
      var ul = el('ul');
      // Same allowlist the server enforces in its
      // /preview endpoint. The client mirrors it so the
      // "Preview" link is only shown for content the
      // server will actually serve inline. SVG is
      // excluded — the server refuses to preview it.
      var previewable = {
        'image/png': true, 'image/jpeg': true,
        'image/gif': true, 'image/webp': true,
        'text/plain': true,
      };
      msg.attachments.forEach(function (a) {
        var li = el('li');
        li.appendChild(el('span', { class: 'att-icon' }, '📎'));
        var name = el('span', { class: 'att-name' }, a.filename || 'attachment');
        li.appendChild(name);
        li.appendChild(el('span', { class: 'att-size' }, formatSize(a.size_bytes)));
        // Download button — always available. Anchor
        // tag with download attribute; the response
        // sets Content-Disposition: attachment so the
        // browser honours the save-as action.
        var dl = el('a', {
          class: 'att-download',
          href: '/api/v1/webmail/attachments/' + a.id,
          'aria-label': 'Download ' + (a.filename || 'attachment'),
          title: 'Download',
          rel: 'noopener',
        }, 'Download');
        li.appendChild(dl);
        if (a.content_type && previewable[a.content_type.toLowerCase()]) {
          var pv = el('a', {
            class: 'att-preview',
            href: '/api/v1/webmail/attachments/' + a.id + '/preview',
            'aria-label': 'Preview ' + (a.filename || 'attachment'),
            title: 'Preview',
            target: '_blank',
            rel: 'noopener',
          }, 'Preview');
          li.appendChild(pv);
        }
        ul.appendChild(li);
      });
      att.appendChild(ul);
      view.appendChild(att);
    }
  }

  function formatSize(bytes) {
    if (!bytes) return '';
    var n = Number(bytes);
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    return (n / 1024 / 1024).toFixed(1) + ' MB';
  }

  function closeReadingPane() {
    state.readingPaneOpen = false;
    if (els.readingPane) els.readingPane.classList.remove('open');
    state.selectedMessageID = null;
    state.selectedMessage = null;
    renderMessageList();
    var view = $('message-view');
    if (view) view.innerHTML = '';
  }

  function toggleSidebar() {
    var sb = $('sidebar');
    if (!sb) return;
    sb.classList.toggle('open');
    state.sidebarOpen = sb.classList.contains('open');
  }

  function closeSidebar() {
    var sb = $('sidebar');
    if (!sb) return;
    sb.classList.remove('open');
    state.sidebarOpen = false;
  }

  // ──────────────────────────────────────────────────────────
  // Routing
  // ──────────────────────────────────────────────────────────

  function navigateToFolder(path) {
    state.searchQuery = '';
    state.selectedMessageID = null;
    state.selectedMessage = null;
    closeSidebar();
    closeReadingPane();
    location.hash = '#/folder/' + encodeURIComponent(path);
  }

  function routeFromHash() {
    var h = location.hash || '#/folder/INBOX';
    if (h.indexOf('#/folder/') === 0) {
      var folder = decodeURIComponent(h.slice('#/folder/'.length));
      state.currentFolderPath = folder || 'INBOX';
      renderFolderSidebar();
      renderMessageList();
      loadMessages().then(function () {
        renderFolderSidebar();
        renderMessageList();
      });
      return;
    }
    if (h.indexOf('#/search') === 0) {
      var q = (h.split('?')[1] || '').match(/q=([^&]+)/);
      var query = q ? decodeURIComponent(q[1]) : '';
      state.searchQuery = query;
      var searchInput = $('search-input');
      if (searchInput) {
        searchInput.value = query;
        var sc = $('search-clear');
        if (sc) sc.hidden = !query;
      }
      renderFolderSidebar();
      renderMessageList();
      loadMessages({ q: query }).then(function () {
        renderFolderSidebar();
        renderMessageList();
      });
      return;
    }
    if (h.indexOf('#/compose') === 0) {
      var params = parseHashParams(h);
      openComposeFromHash(params);
      return;
    }
    // Default.
    location.hash = '#/folder/' + encodeURIComponent(state.currentFolderPath || 'INBOX');
  }

  function parseHashParams(h) {
    var idx = h.indexOf('?');
    if (idx < 0) return {};
    var qs = h.slice(idx + 1);
    var out = {};
    qs.split('&').forEach(function (kv) {
      if (!kv) return;
      var p = kv.split('=');
      out[decodeURIComponent(p[0])] = decodeURIComponent(p[1] || '');
    });
    return out;
  }

  function openComposeFromHash(params) {
    var mode = params.mode || 'new';
    var draftID = params.draft ? parseInt(params.draft, 10) : null;
    if (draftID) {
      // Load the draft and open.
      api('GET', '/api/v1/webmail/drafts/' + draftID)
        .then(function (d) {
          state.compose = {
            mode: 'draft',
            draftID: d.id,
            to: d.to || '',
            cc: d.cc || '',
            bcc: d.bcc || '',
            subject: d.subject || '',
            body: d.body || '',
          };
          renderComposeModal();
        })
        .catch(function (e) {
          toast('Cannot open draft: ' + e.message, 'error');
        });
      return;
    }
    var replyID = params.replyTo || params.replyAll || params.forward;
    if (!replyID) {
      openCompose({ mode: 'new' });
      return;
    }
    api('GET', '/api/v1/webmail/messages/' + replyID)
      .then(function (msg) {
        if (params.replyAll) openCompose({ mode: 'replyAll', replyTo: msg });
        else if (params.forward) openCompose({ mode: 'forward', replyTo: msg });
        else openCompose({ mode: 'reply', replyTo: msg });
      })
      .catch(function (e) {
        toast('Cannot open message: ' + e.message, 'error');
      });
  }

  // ──────────────────────────────────────────────────────────
  // Search
  // ──────────────────────────────────────────────────────────

  var onSearchInput = debounce(function (ev) {
    var q = (ev.target.value || '').trim();
    var sc = $('search-clear');
    if (sc) sc.hidden = !q;
    if (q) {
      location.hash = '#/search?q=' + encodeURIComponent(q);
    } else {
      location.hash = '#/folder/' + encodeURIComponent(state.currentFolderPath || 'INBOX');
    }
  }, 250);

  // ──────────────────────────────────────────────────────────
  // Settings
  // ──────────────────────────────────────────────────────────

  // applyAppearanceSettings writes theme + density + text-direction
  // onto the document immediately. Called on initial load (after
  // loadSettings resolves) and after every successful PUT. Theme
  // values: 'dark' | 'light' | 'system'. The 'system' choice reads
  // prefers-color-scheme at apply time and does NOT install a media
  // listener — a full system-listener hookup is a follow-up patch.
  function applyAppearanceSettings(s) {
    if (!s) return;
    var html = document.documentElement;
    var theme = s.theme || 'dark';
    if (theme === 'light') {
      html.classList.add('theme-light');
      html.classList.remove('theme-dark');
    } else if (theme === 'dark') {
      html.classList.add('theme-dark');
      html.classList.remove('theme-light');
    } else {
      // 'system' — match OS preference once at boot; do not listen for
      // OS changes (not a registered feature; UI surfaces the choice).
      var sysDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
      if (sysDark) {
        html.classList.add('theme-dark');
        html.classList.remove('theme-light');
      } else {
        html.classList.add('theme-light');
        html.classList.remove('theme-dark');
      }
    }
    var density = s.density || 'comfortable';
    html.classList.toggle('density-compact', density === 'compact');
    var dir = s.text_direction || 'auto';
    html.setAttribute('dir', dir === 'auto' ? 'auto' : dir);
    html.setAttribute('lang', s.language || 'en');
    // preview_lines controls the message list preview-line clamp.
    // 0 hides the preview entirely (display:none). 1..6 sets a CSS
    // class that the stylesheet maps to -webkit-line-clamp.
    var previewLines = (typeof s.preview_lines === 'number')
      ? Math.max(0, Math.min(6, s.preview_lines))
      : 2;
    var msgs = document.getElementById('messages');
    if (msgs) {
      // Strip any prior preview-lines-N class so re-applying does
      // not accumulate stale classes.
      var cls = msgs.className.split(/\s+/).filter(function (c) {
        return c && c.indexOf('preview-lines-') !== 0;
      });
      cls.push('preview-lines-' + previewLines);
      msgs.className = cls.join(' ');
    }
  }

  function loadSettings() {
    return api('GET', '/api/v1/webmail/settings').then(function (s) {
      state.settings = s;
      state.settingsLoaded = true;
      state.settingsLoadFailed = false;
      state.settingsLoadFailureMessage = '';
      applyAppearanceSettings(s);
      return s;
    }).catch(function (err) {
      // Settings are non-essential for the shell to work, but the
      // user must NOT be misled into thinking their saved values
      // were loaded from the server. We:
      //   1. Apply hard-coded fallbacks so the UI keeps working.
      //   2. Set settingsLoadFailed = true so the Settings modal
      //      surfaces a clear "failed to load" notice.
      //   3. Toast a non-fatal warning so the user sees the
      //      problem even if they never open Settings.
      //   4. Record the reason so the in-modal banner can quote
      //      it (useful when the cause is a transient 5xx).
      var reason = (err && err.message) || 'Unknown error';
      state.settings = state.settings || {
        theme: 'dark', density: 'comfortable', text_direction: 'auto',
        language: 'en', preview_lines: 2, reading_pane: 'right',
        signature_enabled: false, signature_text: '',
        signature_in_replies: true, default_reply_mode: 'reply',
        autosave_seconds: 3, confirm_before_discard: true,
        warn_on_empty_subject: false, default_folder: 'INBOX',
        mark_read_delay_seconds: 0, sender_display: 'name',
        notify_inapp: true, notify_push: true,
      };
      state.settingsLoaded = true;
      state.settingsLoadFailed = true;
      state.settingsLoadFailureMessage = reason;
      applyAppearanceSettings(state.settings);
      toast(
        'Settings failed to load — using temporary defaults. ' +
          'Open Settings for details or refresh to retry.',
        'error',
        6000
      );
    });
  }

  function loadPushStatusBestEffort() {
    // The push endpoint may 404 if push is disabled at the server.
    // We treat that as "not configured" and keep the UI honest.
    return fetch('/api/v1/webmail/push/status', { credentials: 'include', headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return null; }); })
      .then(function (data) { state.pushStatus = data; })
      .catch(function () { state.pushStatus = null; });
  }

  // ──────────────────────────────────────────────────────────
  // Rules / vacation / forwarding loaders
  // ──────────────────────────────────────────────────────────
  //
  // Each loader mirrors loadSettings' shape:
  //   - On success: state.X = response, state.XLoadFailed = false.
  //   - On failure: state.XLoadFailed = true with a
  //     human-readable reason the in-tab banner can quote.
  //     State.X is left untouched (rules: keep last known
  //     list; vacation / forwarding: keep null so the UI
  //     can show a fresh "no data yet" state).
  //
  // The list endpoint, vacation GET and forwarding GET all
  // sit on the auth-protected group. They do NOT need
  // CSRF — only the auth/logout + change-password routes
  // do. api() handles the credentials and JSON parsing.

  function loadRules() {
    return api('GET', '/api/v1/webmail/rules').then(function (resp) {
      // Backend wraps the list under {rules: [...]} — keep
      // a defensive fallback for an alternative shape so a
      // server change does not silently leave the list empty.
      var list = (resp && Array.isArray(resp.rules)) ? resp.rules :
                 (Array.isArray(resp) ? resp : []);
      state.rules = list;
      state.rulesLoaded = true;
      state.rulesLoadFailed = false;
      state.rulesLoadFailureMessage = '';
      return list;
    }).catch(function (err) {
      state.rules = state.rules || [];
      state.rulesLoaded = true;
      state.rulesLoadFailed = true;
      state.rulesLoadFailureMessage = (err && err.message) || 'Unknown error';
      toast('Filters failed to load: ' + state.rulesLoadFailureMessage, 'error', 4500);
    });
  }

  function loadVacation() {
    return api('GET', '/api/v1/webmail/vacation').then(function (resp) {
      state.vacation = resp || {
        enabled: false, subject: '', body: '',
        reply_interval_seconds: 86400,
      };
      state.vacationLoaded = true;
      state.vacationLoadFailed = false;
      state.vacationLoadFailureMessage = '';
      return state.vacation;
    }).catch(function (err) {
      state.vacation = state.vacation || {
        enabled: false, subject: '', body: '',
        reply_interval_seconds: 86400,
      };
      state.vacationLoaded = true;
      state.vacationLoadFailed = true;
      state.vacationLoadFailureMessage = (err && err.message) || 'Unknown error';
      toast('Vacation failed to load: ' + state.vacationLoadFailureMessage, 'error', 4500);
    });
  }

  function loadForwarding() {
    return api('GET', '/api/v1/webmail/forwarding').then(function (resp) {
      state.forwarding = resp || {
        enabled: false, forward_to: '', keep_copy: true,
      };
      state.forwardingLoaded = true;
      state.forwardingLoadFailed = false;
      state.forwardingLoadFailureMessage = '';
      return state.forwarding;
    }).catch(function (err) {
      state.forwarding = state.forwarding || {
        enabled: false, forward_to: '', keep_copy: true,
      };
      state.forwardingLoaded = true;
      state.forwardingLoadFailed = true;
      state.forwardingLoadFailureMessage = (err && err.message) || 'Unknown error';
      toast('Forwarding failed to load: ' + state.forwardingLoadFailureMessage, 'error', 4500);
    });
  }

  // ──────────────────────────────────────────────────────────
  // Filters / Vacation / Forwarding renderers
  // ──────────────────────────────────────────────────────────
  //
  // These are kept at module scope so the Settings modal
  // can re-render after async loads. Each renderer is
  // idempotent: it wipes `host` is NOT its job (the caller
  // already cleared it via activateTab). The renderer just
  // appends children.
  //
  // The supported match types and action types are hard-coded
  // to mirror rules.ValidateConditionsJSON /
  // rules.ValidateActionsJSON in the Go backend. Adding a new
  // type requires a backend change first; the UI must never
  // claim to support a type that the server will reject.

  var FILTER_MATCH_TYPES = [
    { value: 'from_equals',       label: 'From equals (exact)',     needsValue: true },
    { value: 'from_contains',     label: 'From contains',           needsValue: true },
    { value: 'to_contains',       label: 'To contains',             needsValue: true },
    { value: 'subject_contains',  label: 'Subject contains',        needsValue: true },
    { value: 'body_contains',     label: 'Body contains',           needsValue: true },
    { value: 'has_attachment',    label: 'Has attachment',          needsValue: true, valueKind: 'boolean' },
  ];

  var FILTER_ACTION_TYPES = [
    { value: 'move_to_folder', label: 'Move to folder',  needsFolder: true },
    { value: 'copy_to_folder', label: 'Copy to folder',  needsFolder: true },
    { value: 'set_flag',       label: 'Set flag',        needsFlag: true },
    { value: 'forward',        label: 'Forward to',      needsForward: true },
  ];

  function safeParseJSON(raw, fallback) {
    if (raw == null || raw === '') {
      return { ok: true, value: fallback };
    }
    try {
      return { ok: true, value: JSON.parse(raw) };
    } catch (e) {
      return { ok: false, error: e && e.message ? e.message : 'invalid JSON' };
    }
  }

  function describeConditions(conds) {
    if (!conds || !conds.length) return 'no conditions';
    return conds.map(function (c) {
      var def = FILTER_MATCH_TYPES.find(function (t) { return t.value === c.type; });
      var label = def ? def.label : ('(unknown: ' + c.type + ')');
      if (c.type === 'has_attachment') {
        return label + ' = ' + (String(c.value).toLowerCase() === 'true' ? 'yes' : 'no');
      }
      return label + ' "' + (c.value || '') + '"';
    }).join(' AND ');
  }

  function describeActions(acts) {
    if (!acts || !acts.length) return 'no actions';
    return acts.map(function (a) {
      switch (a.type) {
        case 'move_to_folder': return 'move → ' + (a.folder_path || '?');
        case 'copy_to_folder': return 'copy → ' + (a.folder_path || '?');
        case 'set_flag': {
          var flags = [];
          if (a.set_flag && a.set_flag.seen === true) flags.push('seen');
          if (a.set_flag && a.set_flag.seen === false) flags.push('unseen');
          if (a.set_flag && a.set_flag.flagged === true) flags.push('flagged');
          if (a.set_flag && a.set_flag.flagged === false) flags.push('unflagged');
          return 'flag (' + (flags.join(', ') || '?') + ')';
        }
        case 'forward': return 'forward → ' + (a.forward_to || '?');
        default: return '(unknown: ' + a.type + ')';
      }
    }).join(' + ');
  }

  // ── Filters tab ─────────────────────────────────────────────

  function renderFiltersTab(host) {
    if (state.rulesLoadFailed) {
      var banner = el('div', { class: 'settings-load-failed-banner', role: 'alert' });
      banner.appendChild(el('div', { class: 'settings-load-failed-title' },
        '⚠ Filters failed to load from the server'));
      banner.appendChild(el('div', { class: 'settings-load-failed-body' },
        'Reason: ' + state.rulesLoadFailureMessage + '. The list below may be stale or empty. Retry loading, or close and reopen this tab.'));
      var retryBtn = el('button', { class: 'btn sm', type: 'button' }, 'Retry');
      retryBtn.addEventListener('click', function () {
        loadRules().then(function () { rerenderActiveTab('filters'); });
      });
      var actions = el('div', { class: 'settings-load-failed-actions' });
      actions.appendChild(retryBtn);
      banner.appendChild(actions);
      host.appendChild(banner);
    }

    host.appendChild(settingsSection('How filters work', [
      el('div', { class: 'settings-notice' },
        'Filters run server-side on inbound mail in the order shown below. Rules with the highest priority (lowest order number) run first. A rule with "stop processing" checked will skip every later rule when it matches. The original message is always stored — a filter that fails does not lose mail.'),
    ]));

    var list = state.rules || [];
    var listWrap = el('div', { class: 'rules-list' });

    if (list.length === 0) {
      listWrap.appendChild(el('div', { class: 'rules-empty' },
        state.rulesLoadFailed
          ? 'No rules could be loaded — see the error above.'
          : 'No filters yet. Click "New rule" below to add one.'));
    } else {
      list.forEach(function (rule, idx) {
        listWrap.appendChild(renderRuleRow(rule, idx));
      });
    }
    host.appendChild(listWrap);

    var actions = el('div', { class: 'settings-row settings-actions' });
    var newBtn = el('button', { class: 'btn primary', type: 'button' }, '+ New rule');
    newBtn.addEventListener('click', function () {
      openRuleEditor(null);
    });
    actions.appendChild(newBtn);
    var refreshBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Reload list');
    refreshBtn.addEventListener('click', function () {
      loadRules().then(function () { rerenderActiveTab('filters'); });
    });
    actions.appendChild(refreshBtn);
    host.appendChild(actions);

    // Replace the default settings Save/Cancel footer with one
    // that only has Close — filter rules are persisted via the
    // row toggle, edit modal, and delete button. The default
    // footer would PUT /api/v1/webmail/settings with an empty
    // patch, which is a no-op but misleading.
    var footer = el('div', { class: 'modal-footer settings-footer' });
    var closeBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Close');
    closeBtn.addEventListener('click', closeSettingsModal);
    footer.appendChild(closeBtn);
    host.appendChild(footer);
  }

  function renderRuleRow(rule, idx) {
    var condsParsed = safeParseJSON(rule.conditions_json, []);
    var actsParsed  = safeParseJSON(rule.actions_json, []);
    var row = el('div', { class: 'rule-row' });

    var head = el('div', { class: 'rule-row-head' });
    var enabledWrap = el('label', { class: 'settings-toggle rule-enabled-toggle' });
    var enabledBox = el('input', { type: 'checkbox' });
    if (rule.enabled) enabledBox.setAttribute('checked', 'checked');
    enabledBox.addEventListener('change', function () {
      var want = !!enabledBox.checked;
      var patch = { enabled: want };
      api('PUT', '/api/v1/webmail/rules/' + rule.id, patch).then(function (updated) {
        // Splice the updated rule back into state.rules
        for (var i = 0; i < state.rules.length; i++) {
          if (state.rules[i].id === rule.id) { state.rules[i] = updated; break; }
        }
        toast('Rule ' + (want ? 'enabled' : 'disabled'), 'success', 1800);
      }).catch(function (err) {
        enabledBox.checked = !!rule.enabled; // revert
        toast('Toggle failed: ' + (err && err.message ? err.message : 'Unknown error'), 'error', 4500);
      });
    });
    enabledWrap.appendChild(enabledBox);
    enabledWrap.appendChild(el('span', { class: 'settings-toggle-box' }));
    enabledWrap.appendChild(el('span', { class: 'settings-toggle-text' },
      rule.name && rule.name.trim() ? rule.name : ('(unnamed rule #' + rule.id + ')')));
    head.appendChild(enabledWrap);

    var meta = el('div', { class: 'rule-row-meta' });
    meta.appendChild(el('span', { class: 'rule-order' }, '#' + (idx + 1)));
    if (rule.stop_processing) {
      meta.appendChild(el('span', { class: 'rule-tag' }, 'stops further rules'));
    }
    if (!condsParsed.ok) {
      meta.appendChild(el('span', { class: 'rule-tag rule-tag-error' },
        'conditions JSON invalid: ' + condsParsed.error));
    }
    if (!actsParsed.ok) {
      meta.appendChild(el('span', { class: 'rule-tag rule-tag-error' },
        'actions JSON invalid: ' + actsParsed.error));
    }
    head.appendChild(meta);

    var rowActions = el('div', { class: 'rule-row-actions' });
    var editBtn = el('button', { class: 'btn ghost sm', type: 'button' }, 'Edit');
    editBtn.addEventListener('click', function () {
      openRuleEditor(rule);
    });
    rowActions.appendChild(editBtn);
    var delBtn = el('button', { class: 'btn ghost sm rule-delete-btn', type: 'button' }, 'Delete');
    delBtn.addEventListener('click', function () {
      var label = rule.name && rule.name.trim() ? rule.name : ('rule #' + rule.id);
      if (!confirm('Delete ' + label + '? This cannot be undone.')) return;
      api('DELETE', '/api/v1/webmail/rules/' + rule.id).then(function () {
        toast('Rule deleted', 'success', 1800);
        return loadRules();
      }).then(function () { rerenderActiveTab('filters'); })
        .catch(function (err) {
          toast('Delete failed: ' + (err && err.message ? err.message : 'Unknown error'), 'error', 4500);
        });
    });
    rowActions.appendChild(delBtn);
    head.appendChild(rowActions);
    row.appendChild(head);

    var desc = el('div', { class: 'rule-row-desc' });
    desc.appendChild(el('div', {}, 'When: ' + (condsParsed.ok ? describeConditions(condsParsed.value) : '—')));
    desc.appendChild(el('div', {}, 'Then: ' + (actsParsed.ok ? describeActions(actsParsed.value) : '—')));
    row.appendChild(desc);

    return row;
  }

  function rerenderActiveTab(key) {
    var host = document.querySelector('.settings-modal .settings-content');
    if (!host) return;
    while (host.firstChild) host.removeChild(host.firstChild);
    renderSettingsTab(key, host, state.settings || {});
  }

  function openRuleEditor(rule) {
    // rule === null → create new
    var isNew = !rule;
    var backdrop = el('div', { class: 'modal-backdrop rule-editor-modal' });
    var modal = el('div', { class: 'modal rule-editor-card', role: 'dialog', 'aria-label': isNew ? 'New rule' : 'Edit rule' });
    var header = el('div', { class: 'modal-header' });
    header.appendChild(el('div', { class: 'title' }, isNew ? 'New rule' : 'Edit rule'));
    var closeBtn = el('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close', title: 'Close' }, '×');
    closeBtn.addEventListener('click', function () { backdrop.remove(); });
    header.appendChild(closeBtn);
    modal.appendChild(header);

    var body = el('div', { class: 'modal-body' });

    var nameInput = el('input', { type: 'text', class: 'settings-input', value: rule && rule.name ? rule.name : '', maxlength: '200', placeholder: 'Optional label (e.g. "Move invoices to Receipts")' });
    body.appendChild(settingsSection('Name', [settingsRow(null, nameInput, 'Helps you remember why this rule exists.')]));

    var stopInput = el('input', { type: 'checkbox', class: 'settings-toggle-input' });
    if (rule && rule.stop_processing) stopInput.setAttribute('checked', 'checked');
    var stopWrap = el('label', { class: 'settings-toggle' });
    stopWrap.appendChild(stopInput);
    stopWrap.appendChild(el('span', { class: 'settings-toggle-box' }));
    stopWrap.appendChild(el('span', { class: 'settings-toggle-text' }, 'Stop processing further rules when this one matches'));
    stopWrap.appendChild(el('span', { class: 'settings-hint' },
      'When checked, rules below this one are skipped for matching mail.'));
    body.appendChild(settingsSection('Run order', [stopWrap]));

    // Conditions
    var conditionsHost = el('div', { class: 'rule-conditions' });
    var condsInit = [];
    if (rule && rule.conditions_json) {
      var p = safeParseJSON(rule.conditions_json, []);
      if (p.ok && Array.isArray(p.value)) condsInit = p.value;
    }
    var conditions = condsInit.length
      ? condsInit.map(function (c) { return { type: c.type || 'from_contains', value: c.value == null ? '' : String(c.value) }; })
      : [{ type: 'from_contains', value: '' }];
    function renderConditionRow(c, idx) {
      var row = el('div', { class: 'rule-condition-row' });
      var typeSel = el('select', { class: 'settings-input rule-condition-type' });
      FILTER_MATCH_TYPES.forEach(function (t) {
        var o = el('option', { value: t.value }, t.label);
        if (t.value === c.type) o.setAttribute('selected', 'selected');
        typeSel.appendChild(o);
      });
      typeSel.addEventListener('change', function () {
        c.type = typeSel.value;
        rebuildValueField();
      });
      row.appendChild(typeSel);

      var valWrap = el('div', { class: 'rule-condition-value' });
      function rebuildValueField() {
        while (valWrap.firstChild) valWrap.removeChild(valWrap.firstChild);
        var def = FILTER_MATCH_TYPES.find(function (t) { return t.value === c.type; });
        if (def && def.valueKind === 'boolean') {
          var bs = el('select', { class: 'settings-input' });
          [['true', 'Yes'], ['false', 'No']].forEach(function (kv) {
            var o = el('option', { value: kv[0] }, kv[1]);
            if (String(c.value).toLowerCase() === kv[0]) o.setAttribute('selected', 'selected');
            bs.appendChild(o);
          });
          bs.addEventListener('change', function () { c.value = bs.value; });
          valWrap.appendChild(bs);
        } else {
          var ti = el('input', { type: 'text', class: 'settings-input', value: c.value || '', placeholder: 'Match text' });
          ti.addEventListener('input', function () { c.value = ti.value; });
          valWrap.appendChild(ti);
        }
      }
      rebuildValueField();
      row.appendChild(valWrap);

      var delBtn = el('button', { class: 'btn ghost sm', type: 'button' }, 'Remove');
      delBtn.addEventListener('click', function () {
        conditions.splice(idx, 1);
        rerenderConditions();
      });
      row.appendChild(delBtn);
      return row;
    }
    function rerenderConditions() {
      while (conditionsHost.firstChild) conditionsHost.removeChild(conditionsHost.firstChild);
      conditions.forEach(function (c, i) { conditionsHost.appendChild(renderConditionRow(c, i)); });
      var addBtn = el('button', { class: 'btn ghost sm', type: 'button' }, '+ Add condition (AND)');
      addBtn.addEventListener('click', function () {
        conditions.push({ type: 'from_contains', value: '' });
        rerenderConditions();
      });
      conditionsHost.appendChild(addBtn);
    }
    rerenderConditions();
    body.appendChild(settingsSection('Conditions (all must match)', [conditionsHost]));

    // Actions
    var actionsHost = el('div', { class: 'rule-actions' });
    var actsInit = [];
    if (rule && rule.actions_json) {
      var ap = safeParseJSON(rule.actions_json, []);
      if (ap.ok && Array.isArray(ap.value)) actsInit = ap.value;
    }
    var actions = actsInit.length ? actsInit.map(plainAction) : [plainAction({ type: 'move_to_folder' })];
    function plainAction(a) {
      // Deep-copy so editing doesn't mutate the original.
      return JSON.parse(JSON.stringify(a));
    }
    function renderActionRow(a, idx) {
      var row = el('div', { class: 'rule-action-row' });
      var typeSel = el('select', { class: 'settings-input rule-action-type' });
      FILTER_ACTION_TYPES.forEach(function (t) {
        var o = el('option', { value: t.value }, t.label);
        if (t.value === a.type) o.setAttribute('selected', 'selected');
        typeSel.appendChild(o);
      });
      typeSel.addEventListener('change', function () {
        // Switch type — reset the type-specific payload to safe defaults.
        var prev = JSON.parse(JSON.stringify(a));
        a.type = typeSel.value;
        a.folder_path = a.folder_path || '';
        a.forward_to = a.forward_to || '';
        if (!a.set_flag) a.set_flag = {};
        // When switching away from a type, drop the irrelevant field so
        // the payload stays clean for the backend.
        if (a.type !== 'move_to_folder' && a.type !== 'copy_to_folder') a.folder_path = '';
        if (a.type !== 'forward') a.forward_to = '';
        if (a.type !== 'set_flag') a.set_flag = undefined;
        rerenderActions();
      });
      row.appendChild(typeSel);

      var payloadWrap = el('div', { class: 'rule-action-payload' });
      function rebuildPayload() {
        while (payloadWrap.firstChild) payloadWrap.removeChild(payloadWrap.firstChild);
        if (a.type === 'move_to_folder' || a.type === 'copy_to_folder') {
          var fi = el('input', { type: 'text', class: 'settings-input', value: a.folder_path || '', placeholder: 'Folder name (e.g. Receipts)' });
          fi.addEventListener('input', function () { a.folder_path = fi.value; });
          payloadWrap.appendChild(fi);
        } else if (a.type === 'set_flag') {
          if (!a.set_flag) a.set_flag = {};
          var wrap = el('div', { class: 'rule-flag-grid' });
          function flagCell(label, key) {
            var tri = el('select', { class: 'settings-input' });
            [['leave', 'Leave unchanged'], ['true', 'On'], ['false', 'Off']].forEach(function (kv) {
              var o = el('option', { value: kv[0] }, kv[1]);
              var cur = a.set_flag && a.set_flag[key] === true ? 'true' :
                        a.set_flag && a.set_flag[key] === false ? 'false' : 'leave';
              if (cur === kv[0]) o.setAttribute('selected', 'selected');
              tri.appendChild(o);
            });
            tri.addEventListener('change', function () {
              if (!a.set_flag) a.set_flag = {};
              if (tri.value === 'leave') {
                delete a.set_flag[key];
              } else if (tri.value === 'true') {
                a.set_flag[key] = true;
              } else {
                a.set_flag[key] = false;
              }
            });
            var cell = el('label', { class: 'rule-flag-cell' });
            cell.appendChild(el('span', {}, label));
            cell.appendChild(tri);
            return cell;
          }
          wrap.appendChild(flagCell('Seen', 'seen'));
          wrap.appendChild(flagCell('Flagged', 'flagged'));
          payloadWrap.appendChild(wrap);
        } else if (a.type === 'forward') {
          var ei = el('input', { type: 'email', class: 'settings-input', value: a.forward_to || '', placeholder: 'someone@example.com' });
          ei.addEventListener('input', function () { a.forward_to = ei.value; });
          payloadWrap.appendChild(ei);
        }
      }
      rebuildPayload();
      row.appendChild(payloadWrap);

      var delBtn = el('button', { class: 'btn ghost sm', type: 'button' }, 'Remove');
      delBtn.addEventListener('click', function () {
        actions.splice(idx, 1);
        rerenderActions();
      });
      row.appendChild(delBtn);
      return row;
    }
    function rerenderActions() {
      while (actionsHost.firstChild) actionsHost.removeChild(actionsHost.firstChild);
      actions.forEach(function (a, i) { actionsHost.appendChild(renderActionRow(a, i)); });
      var addBtn = el('button', { class: 'btn ghost sm', type: 'button' }, '+ Add action');
      addBtn.addEventListener('click', function () {
        actions.push({ type: 'move_to_folder', folder_path: '' });
        rerenderActions();
      });
      actionsHost.appendChild(addBtn);
    }
    rerenderActions();
    body.appendChild(settingsSection('Actions (all run in order)', [actionsHost]));

    var errBox = el('div', { class: 'settings-notice settings-notice-error', role: 'alert', style: 'display:none;' });
    body.appendChild(errBox);

    var footer = el('div', { class: 'modal-footer settings-footer' });
    var cancelBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Cancel');
    cancelBtn.addEventListener('click', function () { backdrop.remove(); });
    var saveBtn = el('button', { class: 'btn primary', type: 'button' }, isNew ? 'Create rule' : 'Save changes');
    saveBtn.addEventListener('click', function () {
      saveBtn.disabled = true;
      saveBtn.textContent = 'Saving…';
      errBox.style.display = 'none';
      // Strip empty values that the backend would reject.
      var cleanedConds = conditions
        .filter(function (c) { return c && c.type; })
        .map(function (c) {
          var out = { type: c.type };
          if (c.type === 'has_attachment') {
            out.value = (String(c.value).toLowerCase() === 'true') ? 'true' : 'false';
          } else {
            out.value = (c.value || '').trim();
          }
          return out;
        });
      var cleanedActs = actions
        .filter(function (a) { return a && a.type; })
        .map(function (a) {
          var out = { type: a.type };
          if (a.type === 'move_to_folder' || a.type === 'copy_to_folder') {
            out.folder_path = (a.folder_path || '').trim();
          } else if (a.type === 'set_flag') {
            var sf = a.set_flag || {};
            var sfClean = {};
            if (sf.seen === true || sf.seen === false) sfClean.seen = sf.seen;
            if (sf.flagged === true || sf.flagged === false) sfClean.flagged = sf.flagged;
            out.set_flag = sfClean;
          } else if (a.type === 'forward') {
            out.forward_to = (a.forward_to || '').trim();
          }
          return out;
        });
      var body = {
        name: (nameInput.value || '').trim(),
        enabled: true,
        sort_order: rule ? (rule.sort_order || 0) : (state.rules ? state.rules.length : 0),
        stop_processing: !!stopInput.checked,
        conditions_json: JSON.stringify(cleanedConds),
        actions_json: JSON.stringify(cleanedActs),
      };
      var p = isNew
        ? api('POST', '/api/v1/webmail/rules', body)
        : api('PUT', '/api/v1/webmail/rules/' + rule.id, body);
      p.then(function () {
        toast(isNew ? 'Rule created' : 'Rule saved', 'success', 1800);
        backdrop.remove();
        return loadRules();
      }).then(function () { rerenderActiveTab('filters'); })
        .catch(function (err) {
          var msg = (err && err.body && err.body.error) || (err && err.message) || 'Unknown error';
          errBox.textContent = 'Save failed: ' + msg;
          errBox.style.display = '';
        })
        .then(function () {
          saveBtn.disabled = false;
          saveBtn.textContent = isNew ? 'Create rule' : 'Save changes';
        });
    });
    footer.appendChild(cancelBtn);
    footer.appendChild(saveBtn);
    modal.appendChild(body);
    modal.appendChild(footer);
    backdrop.appendChild(modal);
    backdrop.addEventListener('click', function (e) {
      if (e.target === backdrop) backdrop.remove();
    });
    document.body.appendChild(backdrop);
    setTimeout(function () { nameInput.focus(); }, 30);
  }

  // ── Vacation tab ───────────────────────────────────────────

  function renderVacationTab(host) {
    if (state.vacationLoadFailed) {
      var banner = el('div', { class: 'settings-load-failed-banner', role: 'alert' });
      banner.appendChild(el('div', { class: 'settings-load-failed-title' },
        '⚠ Vacation failed to load from the server'));
      banner.appendChild(el('div', { class: 'settings-load-failed-body' },
        'Reason: ' + state.vacationLoadFailureMessage + '. Saving now would overwrite whatever the server has — click Retry first.'));
      var actions = el('div', { class: 'settings-load-failed-actions' });
      var retry = el('button', { class: 'btn sm', type: 'button' }, 'Retry');
      retry.addEventListener('click', function () { loadVacation().then(function () { rerenderActiveTab('vacation'); }); });
      actions.appendChild(retry);
      banner.appendChild(actions);
      host.appendChild(banner);
    }

    var v = state.vacation || { enabled: false, subject: '', body: '', reply_interval_seconds: 86400 };
    host.appendChild(settingsSection('Auto-reply', [
      settingsBool('vacation_enabled', 'Send automatic replies while I am away', null),
    ]));
    // The settingsBool helper reads state.settings.* — for
    // vacation we need to bind to state.vacation.* instead.
    // Re-render the row we just added so the checkbox
    // reflects state.vacation.enabled.
    var enabledRow = host.querySelector('[data-key="vacation_enabled"]');
    if (enabledRow) {
      enabledRow.checked = !!v.enabled;
    }

    host.appendChild(settingsSection('Message', [
      settingsText('vacation_subject', 'Subject', 'Up to 256 characters.', 256),
      settingsTextarea('vacation_body', 'Body', 'Up to 4096 characters. Plain text — newlines preserved.', 4096),
    ]));
    // Re-bind inputs to vacation fields.
    var subjInput = host.querySelector('[data-key="vacation_subject"]');
    if (subjInput) subjInput.value = v.subject || '';
    var bodyArea  = host.querySelector('[data-key="vacation_body"]');
    if (bodyArea)  bodyArea.value  = v.body || '';

    host.appendChild(settingsSection('Rate limit', [
      (function () {
        var row = el('div', { class: 'settings-row' });
        var label = el('label', { class: 'settings-label' }, 'Reply interval (seconds)');
        var input = el('input', { type: 'number', class: 'settings-input', min: '60', max: String(30 * 86400),
          value: String(v.reply_interval_seconds || 86400) });
        input.setAttribute('data-key', 'vacation_reply_interval');
        row.appendChild(label);
        row.appendChild(input);
        row.appendChild(el('div', { class: 'settings-hint' },
          'Each sender gets at most one auto-reply per interval. ' +
          'Minimum 60 seconds (1 minute). Maximum ' + (30 * 86400) + ' seconds (30 days). ' +
          'Default 86400 (1 day).'));
        return row;
      })(),
    ]));

    host.appendChild(settingsSection('Window (optional)', [
      (function () {
        // Backend supports start_at / end_at as RFC3339
        // strings or null. Use datetime-local input and
        // convert on save. Empty input ⇒ clear the bound.
        function datetimeLocalFor(iso) {
          if (!iso) return '';
          // The server returns RFC3339; <input type=datetime-local>
          // wants "YYYY-MM-DDTHH:MM" without zone. Strip the zone.
          var m = String(iso).match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})/);
          return m ? m[1] : '';
        }
        var startVal = datetimeLocalFor(v.start_at);
        var endVal   = datetimeLocalFor(v.end_at);
        var start = el('input', { type: 'datetime-local', class: 'settings-input', value: startVal });
        start.setAttribute('data-key', 'vacation_start_at');
        var end = el('input', { type: 'datetime-local', class: 'settings-input', value: endVal });
        end.setAttribute('data-key', 'vacation_end_at');
        var clearStart = el('button', { class: 'btn ghost sm', type: 'button' }, 'Clear start');
        clearStart.addEventListener('click', function () { start.value = ''; });
        var clearEnd = el('button', { class: 'btn ghost sm', type: 'button' }, 'Clear end');
        clearEnd.addEventListener('click', function () { end.value = ''; });
        var row = el('div', { class: 'settings-row' });
        row.appendChild(el('label', { class: 'settings-label' }, 'Start'));
        row.appendChild(start);
        row.appendChild(clearStart);
        row.appendChild(el('div', { class: 'settings-hint', style: 'width:100%' },
          'Optional. If set, auto-replies only begin after this moment.'));
        return row;
      })(),
      (function () {
        var end = el('input', { type: 'datetime-local', class: 'settings-input',
          value: (function (iso) {
            if (!iso) return '';
            var m = String(iso).match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})/);
            return m ? m[1] : '';
          })(v.end_at) });
        end.setAttribute('data-key', 'vacation_end_at');
        var clearEnd = el('button', { class: 'btn ghost sm', type: 'button' }, 'Clear end');
        clearEnd.addEventListener('click', function () { end.value = ''; });
        var row = el('div', { class: 'settings-row' });
        row.appendChild(el('label', { class: 'settings-label' }, 'End'));
        row.appendChild(end);
        row.appendChild(clearEnd);
        row.appendChild(el('div', { class: 'settings-hint', style: 'width:100%' },
          'Optional. If set, auto-replies stop after this moment.'));
        return row;
      })(),
    ]));

    host.appendChild(settingsSection('How vacation replies work', [
      el('div', { class: 'settings-notice' },
        '• Auto-replies are sent only to real human senders. Messages that carry an ' +
        '"Auto-Submitted" header (RFC 8058) or look like bulk mail (Precedence: bulk / ' +
        'Precedence: junk, List-Unsubscribe / List-Id, mailing-list headers) are skipped — ' +
        'this prevents vacation loops with autoresponders and mailing lists.'),
      el('div', { class: 'settings-notice', style: 'margin-top:var(--sp-2)' },
        '• Each sender gets at most one auto-reply per "Reply interval". The first message ' +
        'from a sender triggers a reply; further messages inside the window do not.'),
      el('div', { class: 'settings-notice', style: 'margin-top:var(--sp-2)' },
        '• The original message is always delivered to your inbox — vacation never moves, ' +
        'copies, or deletes it.'),
    ]));

    // Override the default footer so we have a single
    // Save button that reads from the vacation-specific
    // fields. Replace any footer already in the host.
    var existing = host.querySelector('.settings-footer');
    if (existing) existing.remove();
    var errBox = el('div', { class: 'settings-notice settings-notice-error', role: 'alert', style: 'display:none;' });
    host.appendChild(errBox);
    var footer = el('div', { class: 'modal-footer settings-footer' });
    var cancelBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Close');
    cancelBtn.addEventListener('click', closeSettingsModal);
    var saveBtn = el('button', { class: 'btn primary', type: 'button' }, 'Save vacation');
    saveBtn.addEventListener('click', function () {
      saveBtn.disabled = true;
      saveBtn.textContent = 'Saving…';
      errBox.style.display = 'none';

      function val(k) {
        var e = host.querySelector('[data-key="' + k + '"]');
        return e ? e.value : '';
      }
      function checked(k) {
        var e = host.querySelector('[data-key="' + k + '"]');
        return !!(e && e.checked);
      }
      function datetimeLocalToRFC3339(s) {
        if (!s) return '';
        // Browsers don't put a zone on datetime-local.
        // Treat it as the user's local zone — send an
        // explicit +00:00 suffix only if the user
        // already typed one. Most installs keep server
        // time in UTC, so we encode what the user typed
        // verbatim with a local-zone marker (no zone).
        // The Go side stores the string opaquely and the
        // engine compares it as text — a future pass
        // can replace this with a proper timezone picker.
        return s;
      }

      var intervalRaw = parseInt(val('vacation_reply_interval'), 10);
      var interval = isNaN(intervalRaw) ? 86400 : intervalRaw;
      if (interval < 60) interval = 60;
      if (interval > 30 * 86400) interval = 30 * 86400;

      var payload = {
        enabled: checked('vacation_enabled'),
        subject: val('vacation_subject').slice(0, 256),
        body: val('vacation_body').slice(0, 4096),
        reply_interval_seconds: interval,
        start_at: datetimeLocalToRFC3339(val('vacation_start_at')),
        end_at:   datetimeLocalToRFC3339(val('vacation_end_at')),
      };
      api('PUT', '/api/v1/webmail/vacation', payload).then(function (saved) {
        state.vacation = saved;
        state.vacationLoadFailed = false;
        toast('Vacation saved', 'success', 1800);
        rerenderActiveTab('vacation');
      }).catch(function (err) {
        var msg = (err && err.body && err.body.error) || (err && err.message) || 'Unknown error';
        errBox.textContent = 'Save failed: ' + msg;
        errBox.style.display = '';
      }).then(function () {
        saveBtn.disabled = false;
        saveBtn.textContent = 'Save vacation';
      });
    });
    footer.appendChild(cancelBtn);
    footer.appendChild(saveBtn);
    host.appendChild(footer);
  }

  // ── Forwarding tab ─────────────────────────────────────────

  function renderForwardingTab(host) {
    if (state.forwardingLoadFailed) {
      var banner = el('div', { class: 'settings-load-failed-banner', role: 'alert' });
      banner.appendChild(el('div', { class: 'settings-load-failed-title' },
        '⚠ Forwarding failed to load from the server'));
      banner.appendChild(el('div', { class: 'settings-load-failed-body' },
        'Reason: ' + state.forwardingLoadFailureMessage + '. Saving now would overwrite whatever the server has — click Retry first.'));
      var actions = el('div', { class: 'settings-load-failed-actions' });
      var retry = el('button', { class: 'btn sm', type: 'button' }, 'Retry');
      retry.addEventListener('click', function () { loadForwarding().then(function () { rerenderActiveTab('forwarding'); }); });
      actions.appendChild(retry);
      banner.appendChild(actions);
      host.appendChild(banner);
    }

    var f = state.forwarding || { enabled: false, forward_to: '', keep_copy: true };
    host.appendChild(settingsSection('Forwarding', [
      settingsBool('fwd_enabled', 'Forward incoming mail to another address', null),
    ]));
    var enabledBox = host.querySelector('[data-key="fwd_enabled"]');
    if (enabledBox) enabledBox.checked = !!f.enabled;

    host.appendChild(settingsSection('Destination', [
      settingsText('fwd_to', 'Forward to (email address)', 'Must be a valid RFC 5322 address. The server rejects malformed values.', 254),
    ]));
    var toInput = host.querySelector('[data-key="fwd_to"]');
    if (toInput) toInput.value = f.forward_to || '';

    host.appendChild(settingsSection('Local copy', [
      settingsBool('fwd_keep_copy', 'Also keep a copy in my inbox', null),
    ]));
    var keepBox = host.querySelector('[data-key="fwd_keep_copy"]');
    if (keepBox) keepBox.checked = !!f.keep_copy;

    host.appendChild(settingsSection('How forwarding works', [
      el('div', { class: 'settings-notice' },
        '• The server forwards a copy of every incoming message to the address above. ' +
        'The forwarded copy carries an "X-Orvix-Forwarded-For" header so replies to it ' +
        'cannot bounce back into the loop — this matches the de-facto behaviour of ' +
        'major providers and prevents the most common vacation-loop / auto-responder ' +
        'storm.'),
      el('div', { class: 'settings-notice', style: 'margin-top:var(--sp-2)' },
        '• "Keep a copy" controls whether the original message is also stored in your ' +
        'inbox. Turning it off means the only place the message lives is the forwarded ' +
        'address — if that address bounces, the message is lost from both sides.'),
      el('div', { class: 'settings-notice settings-notice-warn', style: 'margin-top:var(--sp-2)' },
        '⚠ Forwarding to an address you do not control is a privacy and security risk: ' +
        'the destination can read every message you receive. Disable forwarding before ' +
        'sharing screen, lending access, or moving jobs.'),
    ]));

    var existing = host.querySelector('.settings-footer');
    if (existing) existing.remove();
    var errBox = el('div', { class: 'settings-notice settings-notice-error', role: 'alert', style: 'display:none;' });
    host.appendChild(errBox);
    var footer = el('div', { class: 'modal-footer settings-footer' });
    var cancelBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Close');
    cancelBtn.addEventListener('click', closeSettingsModal);
    var saveBtn = el('button', { class: 'btn primary', type: 'button' }, 'Save forwarding');
    saveBtn.addEventListener('click', function () {
      saveBtn.disabled = true;
      saveBtn.textContent = 'Saving…';
      errBox.style.display = 'none';

      function val(k) {
        var e = host.querySelector('[data-key="' + k + '"]');
        return e ? e.value : '';
      }
      function checked(k) {
        var e = host.querySelector('[data-key="' + k + '"]');
        return !!(e && e.checked);
      }

      var wantEnabled = checked('fwd_enabled');
      var forwardTo = val('fwd_to').trim();

      // Client-side mirror of the Go side's mail.ParseAddress
      // check — gives instant feedback without a round trip.
      // The server still re-validates; this is purely UX.
      if (wantEnabled && forwardTo === '') {
        errBox.textContent = 'Save failed: forward_to is required when forwarding is enabled.';
        errBox.style.display = '';
        saveBtn.disabled = false;
        saveBtn.textContent = 'Save forwarding';
        return;
      }
      if (forwardTo !== '' && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(forwardTo)) {
        errBox.textContent = 'Save failed: forward_to does not look like a valid email address. The server will also reject this.';
        errBox.style.display = '';
        saveBtn.disabled = false;
        saveBtn.textContent = 'Save forwarding';
        return;
      }

      var payload = {
        enabled: wantEnabled,
        forward_to: forwardTo,
        keep_copy: checked('fwd_keep_copy'),
      };
      api('PUT', '/api/v1/webmail/forwarding', payload).then(function (saved) {
        state.forwarding = saved;
        state.forwardingLoadFailed = false;
        toast('Forwarding saved', 'success', 1800);
        rerenderActiveTab('forwarding');
      }).catch(function (err) {
        var msg = (err && err.body && err.body.error) || (err && err.message) || 'Unknown error';
        errBox.textContent = 'Save failed: ' + msg;
        errBox.style.display = '';
      }).then(function () {
        saveBtn.disabled = false;
        saveBtn.textContent = 'Save forwarding';
      });
    });
    footer.appendChild(cancelBtn);
    footer.appendChild(saveBtn);
    host.appendChild(footer);
  }

  function openSettingsModal() {
    // If the modal is already open, focus it instead of stacking.
    var existing = document.querySelector('.modal-backdrop.settings-modal');
    if (existing) {
      var firstInput = existing.querySelector('input, select, textarea, button');
      if (firstInput) firstInput.focus();
      return;
    }
    var s = state.settings || {};
    var backdrop = el('div', { class: 'modal-backdrop settings-modal' });
    var modal = el('div', { class: 'modal settings-modal-card', role: 'dialog', 'aria-label': 'Settings' });

    var header = el('div', { class: 'modal-header' });
    header.appendChild(el('div', { class: 'title' }, 'Settings'));
    var closeBtn = el('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close settings', title: 'Close' }, '×');
    closeBtn.addEventListener('click', closeSettingsModal);
    header.appendChild(closeBtn);
    modal.appendChild(header);

    var body = el('div', { class: 'modal-body settings-modal-body' });
    var tabs = el('div', { class: 'settings-tabs', role: 'tablist' });
    var content = el('div', { class: 'settings-content', role: 'tabpanel' });

    function makeTab(key, label) {
      var t = el('button', { class: 'settings-tab', type: 'button', role: 'tab', 'aria-selected': 'false', 'data-tab': key }, label);
      t.addEventListener('click', function () { activateTab(key); });
      return t;
    }
    function activateTab(key) {
      Array.prototype.forEach.call(tabs.children, function (child) {
        var on = child.getAttribute('data-tab') === key;
        child.classList.toggle('active', on);
        child.setAttribute('aria-selected', on ? 'true' : 'false');
      });
      while (content.firstChild) content.removeChild(content.firstChild);
      renderSettingsTab(key, content, s);
    }
    // For tabs whose data comes from a dedicated endpoint,
    // refresh from the server on each activation so the user
    // always sees the latest persisted state. The first
    // paint still works because the GET is awaited before
    // the tab body is rendered.
    function renderAfterRefresh(key) {
      while (content.firstChild) content.removeChild(content.firstChild);
      renderSettingsTab(key, content, s);
    }
    function activateWithLoad(key, loader) {
      activateTab(key);
      loader().then(function () { renderAfterRefresh(key); });
    }
    tabs.appendChild(makeTab('profile', 'Profile'));
    tabs.appendChild(makeTab('appearance', 'Appearance'));
    tabs.appendChild(makeTab('compose', 'Compose'));
    tabs.appendChild(makeTab('mail', 'Mail'));
    // Filters / Vacation / Forwarding each own a separate
    // endpoint and must be re-fetched on every activation
    // so the user always sees the latest persisted state
    // (a background runner may have moved a rule, the
    // forward-to address may have been edited elsewhere,
    // etc.).
    (function () {
      var f = el('button', { class: 'settings-tab', type: 'button', role: 'tab', 'aria-selected': 'false', 'data-tab': 'filters' }, 'Filters');
      f.addEventListener('click', function () { activateWithLoad('filters', loadRules); });
      tabs.appendChild(f);
    })();
    (function () {
      var v = el('button', { class: 'settings-tab', type: 'button', role: 'tab', 'aria-selected': 'false', 'data-tab': 'vacation' }, 'Vacation');
      v.addEventListener('click', function () { activateWithLoad('vacation', loadVacation); });
      tabs.appendChild(v);
    })();
    (function () {
      var fw = el('button', { class: 'settings-tab', type: 'button', role: 'tab', 'aria-selected': 'false', 'data-tab': 'forwarding' }, 'Forwarding');
      fw.addEventListener('click', function () { activateWithLoad('forwarding', loadForwarding); });
      tabs.appendChild(fw);
    })();
    tabs.appendChild(makeTab('notifications', 'Notifications'));
    tabs.appendChild(makeTab('security', 'Security'));
    tabs.appendChild(makeTab('deferred', 'Coming later'));
    body.appendChild(tabs);
    body.appendChild(content);
    modal.appendChild(body);

    backdrop.appendChild(modal);
    backdrop.addEventListener('click', function (e) {
      if (e.target === backdrop) closeSettingsModal();
    });
    document.addEventListener('keydown', escCloseSettings);
    document.body.appendChild(backdrop);
    activateTab('profile');
  }

  function escCloseSettings(e) {
    if (e.key === 'Escape') closeSettingsModal();
  }
  function closeSettingsModal() {
    var m = document.querySelector('.modal-backdrop.settings-modal');
    if (m) m.remove();
    document.removeEventListener('keydown', escCloseSettings);
  }

  // Helper: build a labeled form row.
  function settingsRow(label, control, hint) {
    var row = el('div', { class: 'settings-row' });
    if (label) row.appendChild(el('label', { class: 'settings-label' }, label));
    row.appendChild(control);
    if (hint) row.appendChild(el('div', { class: 'settings-hint' }, hint));
    return row;
  }
  function settingsText(key, label, hint, max) {
    var v = state.settings && state.settings[key] != null ? String(state.settings[key]) : '';
    var i = el('input', { type: 'text', class: 'settings-input', 'data-key': key, value: v, maxlength: max || 200 });
    return settingsRow(label, i, hint);
  }
  function settingsTextarea(key, label, hint, max) {
    var v = state.settings && state.settings[key] != null ? String(state.settings[key]) : '';
    var i = el('textarea', { class: 'settings-input settings-textarea', 'data-key': key, rows: '4' });
    if (max) i.setAttribute('maxlength', String(max));
    i.value = v;
    return settingsRow(label, i, hint);
  }
  function settingsSelect(key, label, hint, options, current) {
    var cur = current != null ? current : (state.settings && state.settings[key]);
    var sel = el('select', { class: 'settings-input', 'data-key': key });
    options.forEach(function (opt) {
      var o = el('option', { value: opt.value }, opt.label);
      if (opt.value === cur) o.setAttribute('selected', 'selected');
      sel.appendChild(o);
    });
    return settingsRow(label, sel, hint);
  }
  function settingsNumber(key, label, hint, min, max) {
    var v = state.settings && state.settings[key] != null ? Number(state.settings[key]) : 0;
    var i = el('input', { type: 'number', class: 'settings-input', 'data-key': key, min: String(min), max: String(max), value: String(v) });
    return settingsRow(label, i, hint);
  }
  function settingsBool(key, label, hint) {
    var v = !!(state.settings && state.settings[key]);
    var wrap = el('label', { class: 'settings-toggle' });
    var i = el('input', { type: 'checkbox', 'data-key': key });
    if (v) i.setAttribute('checked', 'checked');
    var box = el('span', { class: 'settings-toggle-box' });
    wrap.appendChild(i);
    wrap.appendChild(box);
    wrap.appendChild(el('span', { class: 'settings-toggle-text' }, label));
    if (hint) wrap.appendChild(el('span', { class: 'settings-hint' }, hint));
    return wrap;
  }
  function settingsSection(title, rows) {
    var s = el('div', { class: 'settings-section' });
    if (title) s.appendChild(el('h3', { class: 'settings-section-title' }, title));
    rows.forEach(function (r) { s.appendChild(r); });
    return s;
  }

  // collectSettingsPatch walks the visible settings-content children
  // and reads data-key from each input/select/textarea. Returns an
  // object suitable for PUT /api/v1/webmail/settings.
  function collectSettingsPatch() {
    var patch = {};
    var inputs = document.querySelectorAll('.settings-content [data-key]');
    Array.prototype.forEach.call(inputs, function (el) {
      var k = el.getAttribute('data-key');
      if (!k) return;
      if (el.tagName === 'INPUT' && el.type === 'checkbox') {
        patch[k] = !!el.checked;
      } else if (el.tagName === 'INPUT' && el.type === 'number') {
        var n = parseInt(el.value, 10);
        if (!isNaN(n)) patch[k] = n;
      } else {
        patch[k] = el.value;
      }
    });
    return patch;
  }

  function saveSettings(onDone) {
    var btn = document.querySelector('.settings-modal .settings-save-btn');
    if (btn) {
      btn.disabled = true;
      btn.textContent = 'Saving…';
    }
    var patch = collectSettingsPatch();
    api('PUT', '/api/v1/webmail/settings', patch)
      .then(function (s) {
        state.settings = s;
        state.settingsLoaded = true;
        applyAppearanceSettings(s);
        toast('Settings saved', 'success', 1800);
        if (typeof onDone === 'function') onDone(s);
      })
      .catch(function (e) {
        toast('Save failed: ' + e.message, 'error', 4500);
      })
      .then(function () {
        if (btn) {
          btn.disabled = false;
          btn.textContent = 'Save';
        }
      });
  }

  function renderSettingsTab(key, host, s) {
    // If the initial GET failed, surface a clear, non-fatal banner
    // at the top of every tab. The banner explains that the values
    // shown are TEMPORARY DEFAULTS (not the user's saved settings)
    // and offers an explicit retry path. Saving a partial patch on
    // top of these defaults will overwrite whatever the server has —
    // the banner calls that out so the user does not do it by
    // accident.
    if (state.settingsLoadFailed) {
      var reason = state.settingsLoadFailureMessage || 'Unknown error';
      var banner = el('div', { class: 'settings-load-failed-banner', role: 'alert' });
      banner.appendChild(el('div', { class: 'settings-load-failed-title' },
        '⚠ Settings failed to load from the server'));
      banner.appendChild(el('div', { class: 'settings-load-failed-body' },
        'Reason: ' + reason + '. The values below are temporary defaults, NOT your saved settings. Saving now will overwrite your real settings on the server.'));
      var retryBtn = el('button', { class: 'btn sm', type: 'button' }, 'Retry loading');
      retryBtn.addEventListener('click', function () {
        loadSettings().then(function () { closeSettingsModal(); openSettingsModal(); });
      });
      var reloadBtn = el('button', { class: 'btn ghost sm', type: 'button' }, 'Refresh page');
      reloadBtn.addEventListener('click', function () {
        if (typeof window !== 'undefined' && window.location) {
          window.location.reload();
        }
      });
      var actions = el('div', { class: 'settings-load-failed-actions' });
      actions.appendChild(retryBtn);
      actions.appendChild(reloadBtn);
      banner.appendChild(actions);
      host.appendChild(banner);
    }
    if (key === 'profile') {
      host.appendChild(settingsSection('Identity', [
        settingsText('display_name', 'Display name', 'Optional — shown on messages you send (depends on recipient support).', 200),
      ]));
      host.appendChild(settingsSection('Locale', [
        settingsSelect('timezone', 'Timezone', 'Currently display-only; used for date/time labels.', [
          { value: '', label: 'Browser default' },
          { value: 'UTC', label: 'UTC' },
          { value: 'Asia/Dubai', label: 'Asia/Dubai (UTC+4)' },
          { value: 'Asia/Riyadh', label: 'Asia/Riyadh (UTC+3)' },
          { value: 'Africa/Cairo', label: 'Africa/Cairo (UTC+2)' },
          { value: 'Europe/London', label: 'Europe/London' },
          { value: 'Europe/Berlin', label: 'Europe/Berlin' },
          { value: 'America/New_York', label: 'America/New York' },
          { value: 'America/Los_Angeles', label: 'America/Los Angeles' },
        ], (s.timezone || '')),
        settingsSelect('language', 'Language', 'Currently display-only — UI strings ship in English. Bundled translations land in a follow-up.', [
          { value: 'en', label: 'English' },
          { value: 'ar', label: 'العربية (Arabic)' },
          { value: 'fr', label: 'Français (French)' },
          { value: 'de', label: 'Deutsch (German)' },
          { value: 'es', label: 'Español (Spanish)' },
        ]),
        settingsSelect('date_format', 'Date format', null, [
          { value: 'locale', label: 'Match browser locale' },
          { value: 'iso', label: 'ISO 8601 — 2026-06-27' },
          { value: 'us', label: 'US — 06/27/2026' },
          { value: 'eu', label: 'European — 27/06/2026' },
        ]),
        settingsSelect('time_format', 'Time format', null, [
          { value: 'locale', label: 'Match browser locale' },
          { value: '24h', label: '24-hour' },
          { value: '12h', label: '12-hour (AM/PM)' },
        ]),
        settingsSelect('text_direction', 'Text direction', 'Sets <html dir>. "Auto" lets the browser decide per element.', [
          { value: 'auto', label: 'Auto (per element)' },
          { value: 'ltr', label: 'Left-to-right' },
          { value: 'rtl', label: 'Right-to-left' },
        ]),
      ]));
    } else if (key === 'appearance') {
      host.appendChild(settingsSection('Theme', [
        settingsSelect('theme', 'Theme', null, [
          { value: 'dark', label: 'Dark' },
          { value: 'light', label: 'Light' },
          { value: 'system', label: 'Match system' },
        ]),
        settingsSelect('density', 'Density', 'Compact mode tightens paddings and font sizes across the shell.', [
          { value: 'comfortable', label: 'Comfortable' },
          { value: 'compact', label: 'Compact' },
        ]),
      ]));
      host.appendChild(settingsSection('Message list', [
        settingsNumber('preview_lines', 'Preview lines', '0 hides the preview line in the list. 1–6.', 0, 6),
      ]));
      // Reading pane position is demoted to "Coming later" — the
      // existing right-pane layout is the only mode the shell
      // currently renders, and adding the bottom / hidden modes
      // would require non-trivial layout work that is out of
      // scope for this pass. Exposing a working-looking select
      // that does nothing on save would violate the
      // "no fake enterprise claims" rule. The storage column is
      // left in place (read-only server-side) so future work can
      // resurrect the control without a schema change.
      host.appendChild(settingsSection('Reading pane', [
        el('div', { class: 'settings-notice' },
          'Reading-pane position (right / bottom / hidden) is coming later. The shell currently renders the pane to the right of the list. Switching this setting now would have no visible effect.'),
      ]));
    } else if (key === 'compose') {
      host.appendChild(settingsSection('Signature', [
        settingsBool('signature_enabled', 'Append signature to new messages', null),
        settingsTextarea('signature_text', 'Signature text', 'Plain text. Up to 4096 characters. Newlines preserved.', 4096),
        settingsBool('signature_in_replies', 'Also append to replies and forwards', null),
      ]));
      host.appendChild(settingsSection('Reply behaviour', [
        settingsSelect('default_reply_mode', 'Default when clicking Reply', null, [
          { value: 'reply', label: 'Reply (only sender)' },
          { value: 'replyAll', label: 'Reply-all (sender + other recipients)' },
        ]),
      ]));
      host.appendChild(settingsSection('Drafts', [
        settingsNumber('autosave_seconds', 'Autosave interval (seconds)', '0 disables autosave.', 0, 60),
        settingsBool('confirm_before_discard', 'Ask before discarding a draft', null),
      ]));
      host.appendChild(settingsSection('Sending warnings', [
        settingsBool('warn_on_empty_subject', 'Warn when sending with empty subject or body', null),
      ]));
      host.appendChild(settingsSection('Draft attachments', [
        el('div', { class: 'settings-notice' },
          'Attachments are not saved in drafts yet. If you attach files and refresh the page, they must be re-selected before sending. The Sent copy of a sent message keeps its attachments.'),
      ]));
    } else if (key === 'mail') {
      host.appendChild(settingsSection('Folders', [
        settingsSelect('default_folder', 'Folder to open after login', null, [
          { value: 'INBOX', label: 'Inbox' },
          { value: 'Archive', label: 'Archive' },
          { value: 'Drafts', label: 'Drafts' },
          { value: 'Sent', label: 'Sent' },
        ]),
      ]));
      host.appendChild(settingsSection('Reading', [
        settingsNumber('mark_read_delay_seconds', 'Mark-as-read delay (seconds)', '0 marks as read immediately on open.', 0, 60),
      ]));
      host.appendChild(settingsSection('Sender display', [
        settingsSelect('sender_display', 'How senders appear in the list', null, [
          { value: 'name', label: 'Display name (e.g. "Alice Smith")' },
          { value: 'email', label: 'Email only' },
          { value: 'name_email', label: 'Name + email (e.g. "Alice Smith <alice@example.com>")' },
        ]),
      ]));
    } else if (key === 'notifications') {
      var ps = state.pushStatus || {};
      var statusText = 'Unknown';
      if (ps && ps.available) {
        statusText = ps.permission === 'denied' ? 'Permission denied' :
                     ps.enabled ? 'Enabled' :
                     'Not subscribed';
      } else if (ps && !ps.available) {
        statusText = 'Not configured by the server operator';
      }
      host.appendChild(settingsSection('In-app', [
        settingsBool('notify_inapp', 'Show desktop / tab notifications while the app is open', null),
      ]));
      host.appendChild(settingsSection('Web Push (browser)', [
        settingsBool('notify_push', 'Allow push notifications when the tab is closed', null),
        el('div', { class: 'settings-notice' }, 'Current status: ' + statusText + '. Open Settings → Notifications in the side panel to manage subscriptions.'),
      ]));
    } else if (key === 'security') {
      var me = state.user || {};
      var email = me.email || '(unknown)';
      host.appendChild(settingsSection('Signed in as', [
        el('div', { class: 'settings-notice' }, 'Mailbox: ' + email),
      ]));
      host.appendChild(settingsSection('Session', [
        (function () {
          var row = el('div', { class: 'settings-row settings-actions' });
          var logout = el('button', { class: 'btn', type: 'button' }, 'Log out of this session');
          logout.addEventListener('click', function () {
            if (!confirm('Log out of this session? You will need to log in again.')) return;
            csrfFetch('/api/v1/auth/logout', { method: 'POST' })
              .then(function (r) {
                if (r && !r.ok) {
                  throw new Error('HTTP ' + r.status);
                }
                window.location.href = '/webmail/login';
              })
              .catch(function (e) { toast('Logout failed: ' + e.message, 'error'); });
          });
          row.appendChild(logout);

          var logoutAll = el('button', { class: 'btn ghost', type: 'button' }, 'Log out of all sessions');
          logoutAll.addEventListener('click', function () {
            if (!confirm('Log out of every session for this user?')) return;
            csrfFetch('/api/v1/auth/logout-all', { method: 'POST' })
              .then(function (r) {
                if (r && !r.ok) {
                  throw new Error('HTTP ' + r.status);
                }
                window.location.href = '/webmail/login';
              })
              .catch(function (e) { toast('Logout-all failed: ' + e.message, 'error'); });
          });
          row.appendChild(logoutAll);
          return row;
        })(),
      ]));
      host.appendChild(settingsSection('Two-factor authentication', [
        el('div', { class: 'settings-notice' }, 'TOTP / app-passwords UI is not enabled in this build. Backend support ships in a follow-up release — your administrator will announce availability.'),
      ]));
    } else if (key === 'filters') {
      renderFiltersTab(host);
    } else if (key === 'vacation') {
      renderVacationTab(host);
    } else if (key === 'forwarding') {
      renderForwardingTab(host);
    } else if (key === 'deferred') {
      host.appendChild(settingsSection('Available in a future release', [
        el('div', { class: 'settings-notice' },
          'The features below are not implemented yet in this build. Filters, vacation, and forwarding have their own tabs now — anything still listed here is genuinely not shipped.'),
        el('ul', { class: 'settings-deferred-list' }, ''),
      ]));
      var list = host.querySelector('.settings-deferred-list');
      [
        'Reading-pane position (bottom / hidden modes) — shell currently only renders the right-of-list layout',
        'Conversation / threaded view',
        'External image loading preference for HTML messages',
        'Two-factor authentication (TOTP)',
        'App-specific passwords',
        'Per-device session list with selective revoke',
        'Conversation / threaded view',
        'External image loading preference for HTML messages',
        'Two-factor authentication (TOTP)',
        'App-specific passwords',
        'Per-device session list with selective revoke',
      ].forEach(function (item) {
        var li = el('li', {}, item);
        list.appendChild(li);
      });
    }

    // Footer with Save / Cancel — every tab gets one so the user
    // never has to hunt for the button after switching tabs. The
    // Filters / Vacation / Forwarding tabs render their OWN
    // footers (different save endpoint, different copy) — if
    // one is already in the host, skip the default footer so
    // we don't end up with two Save buttons.
    if (!host.querySelector('.settings-footer')) {
      var footer = el('div', { class: 'modal-footer settings-footer' });
      var cancelBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Cancel');
      cancelBtn.addEventListener('click', closeSettingsModal);
      var saveBtn = el('button', { class: 'btn primary settings-save-btn', type: 'button' }, 'Save');
      saveBtn.addEventListener('click', function () { saveSettings(); });
      footer.appendChild(cancelBtn);
      footer.appendChild(saveBtn);
      host.appendChild(footer);
    }
  }

  // applySignatureToCompose prepends/appends the user's signature to
  // the compose body if signature_enabled is true. Signature-in-replies
  // controls whether replies/forwards also get it (new messages always
  // get it when enabled).
  function applySignatureToCompose(body, mode) {
    var s = state.settings;
    if (!s || !s.signature_enabled || !s.signature_text) return body;
    if ((mode === 'reply' || mode === 'replyAll' || mode === 'forward') && !s.signature_in_replies) {
      return body;
    }
    var sig = s.signature_text;
    // If the body already contains the signature verbatim at the end,
    // do not double-append (this happens when the user opens the same
    // draft after a settings change).
    if (body && body.indexOf(sig) !== -1) return body;
    return (body || '') + '\n\n' + sig + '\n';
  }

  // ──────────────────────────────────────────────────────────
  // Compose modal
  // ──────────────────────────────────────────────────────────

  function openCompose(opts) {
    opts = opts || {};
    if (opts.mode === 'reply' || opts.mode === 'replyAll' || opts.mode === 'forward') {
      var m = opts.replyTo;
      // Honor the Reply-To header when it is set on the
      // original message. The RFC says a Reply-To that
      // differs from From MUST win; the previous client
      // always replied to From, which is a real-world
      // deliverability bug for mailing lists, support
      // systems, and any sender that uses Reply-To.
      var replyTarget = (m.reply_to && emailOf(m.reply_to)) || emailOf(m.from || '');
      var myEmail = state.user && state.user.email ? state.user.email : '';
      var compose = {
        mode: opts.mode,
        to: '',
        cc: '',
        bcc: '',
        subject: '',
        body: '',
        dirty: false,
        lastSavedAt: null,
        pendingAttachments: [],
        lastSavedSnapshot: null,
      };
      if (opts.mode === 'reply') {
        compose.to = replyTarget;
        compose.subject = withPrefix('Re: ', m.subject || '');
        compose.body = applySignatureToCompose(buildQuote(m), 'reply');
      } else if (opts.mode === 'replyAll') {
        // Reply-all recipient computation. Steps:
        //   1. To = replyTarget (Reply-To or From).
        //   2. Cc starts empty.
        //   3. From the original To and Cc lists, drop
        //      self, drop any address that already
        //      appears in the new To, and drop empty
        //      strings. The resulting set is the Cc
        //      list — order preserved, no duplicates.
        //   4. The original sender (From) is NOT
        //      duplicated into Cc — the RFC 5322 reply
        //      semantics: address From in To (already
        //      done) and do not re-add it in Cc.
        compose.to = replyTarget;
        var toLower = replyTarget.toLowerCase();
        var seen = {};
        seen[toLower] = true;
        var ccList = [];
        function pushAddr(s) {
          var v = (s || '').trim();
          if (!v) return;
          var k = v.toLowerCase();
          if (seen[k]) return;
          seen[k] = true;
          ccList.push(v);
        }
        // The header parser exposes to / cc as raw
        // strings with comma-separated addresses. We
        // split on commas and trim.
        (m.to || '').split(',').forEach(pushAddr);
        (m.cc || '').split(',').forEach(pushAddr);
        // Drop self.
        if (myEmail) {
          ccList = ccList.filter(function (e) {
            return emailOf(e).toLowerCase() !== myEmail.toLowerCase();
          });
        }
        compose.cc = ccList.join(', ');
        compose.subject = withPrefix('Re: ', m.subject || '');
        compose.body = applySignatureToCompose(buildQuote(m), 'replyAll');
      } else if (opts.mode === 'forward') {
        compose.subject = withPrefix('Fwd: ', m.subject || '');
        var fwdBody =
          '\n\n---------- Forwarded message ----------\n' +
          'From: ' +
          (m.from || '') +
          '\nDate: ' +
          (m.received_date || m.message_date || '') +
          '\nSubject: ' +
          (m.subject || '') +
          '\nTo: ' +
          (m.to || '') +
          '\n\n' +
          stripRfc822Headers(m.rfc822 || '');
        // Forwarding attachments is not yet supported by
        // the send endpoint. Surface that fact plainly
        // in the banner so the user is not surprised
        // when the forwarded message arrives without
        // its files.
        if (m.attachments && m.attachments.length > 0) {
          fwdBody =
            '\n\nNote: the original message had ' +
            m.attachments.length +
            ' attachment' +
            (m.attachments.length === 1 ? '' : 's') +
            ' that are not included in this forward. ' +
            'Attachment forwarding is coming soon.\n' +
            '---------- Forwarded message ----------\n' +
            'From: ' +
            (m.from || '') +
            '\nDate: ' +
            (m.received_date || m.message_date || '') +
            '\nSubject: ' +
            (m.subject || '') +
            '\nTo: ' +
            (m.to || '') +
            '\n\n' +
            stripRfc822Headers(m.rfc822 || '');
        }
        compose.body = applySignatureToCompose(fwdBody, 'forward');
      } else if (opts.mode === 'new') {
        // For new messages, body starts empty; signature_in_replies
        // does not apply. The signature is the seed body so the user
        // starts typing above it. applySignatureToCompose handles the
        // enabled check + the duplicate-prevention guard.
        compose.body = applySignatureToCompose('', 'new');
      }
      state.compose = compose;
    } else if (opts.mode === 'draft') {
      state.compose = Object.assign(
        { dirty: false, lastSavedAt: null, pendingAttachments: [], lastSavedSnapshot: null },
        opts.compose || opts
      );
} else {
      state.compose = {
        mode: 'new', to: '', cc: '', bcc: '', subject: '',
        body: applySignatureToCompose('', 'new'),
        dirty: false, lastSavedAt: null,
        pendingAttachments: [], lastSavedSnapshot: null,
      };
    }
    renderComposeModal();
  }

  function withPrefix(prefix, s) {
    var t = (s || '').trim();
    if (t.toLowerCase().indexOf(prefix.toLowerCase()) === 0) return t;
    return prefix + t;
  }

  function buildQuote(m) {
    var date = m.received_date || m.message_date || '';
    // The reply-quote header uses the same sender formatting as the
    // list row / reading pane so the user sees one consistent sender
    // string everywhere. If settings have not loaded yet the helper
    // falls back to the display name (the historical behaviour).
    var senderForQuote = formatSender(m.from || '');
    return (
      '\n\n' +
      'On ' +
      date +
      ', ' +
      senderForQuote +
      ' wrote:\n' +
      '> ' +
      stripRfc822Headers(m.rfc822 || '').split('\n').join('\n> ')
    );
  }

  function stripRfc822Headers(s) {
    var i = s.indexOf('\r\n\r\n');
    if (i >= 0) return s.slice(i + 4);
    var j = s.indexOf('\n\n');
    if (j >= 0) return s.slice(j + 2);
    return s;
  }

  function renderComposeModal() {
    var existing = document.querySelector('.modal-backdrop');
    if (existing) existing.remove();
    var c = state.compose;
    if (!c) return;
    // Reset autosave state for a fresh modal. The timer
    // is restarted by the input listeners (markDirty)
    // below; we keep a single setTimeout id on the
    // compose state so re-renders of the modal do not
    // spawn parallel timers.
    if (c.autosaveTimer) {
      clearTimeout(c.autosaveTimer);
      c.autosaveTimer = null;
    }
    // The status line lives in the modal footer and
    // surfaces the most recent autosave / manual save
    // state. Updated by markSaved() and the save
    // handlers below.
    var statusLine = el('div', { class: 'autosave-status' });
    function refreshStatus() {
      if (c.lastSavedAt) {
        var ago = Math.max(1, Math.round((Date.now() - c.lastSavedAt) / 1000));
        statusLine.textContent = 'Saved ' + ago + 's ago';
        statusLine.classList.add('saved');
        statusLine.classList.remove('saving', 'error');
      } else if (c.draftID) {
        statusLine.textContent = 'Draft loaded';
        statusLine.classList.add('saved');
      } else {
        statusLine.textContent = 'New draft';
        statusLine.classList.remove('saving', 'saved', 'error');
      }
    }
    refreshStatus();

    // performAutosave is invoked 3 seconds after the
    // last input event. Empty drafts are skipped so the
    // Drafts folder is not cluttered with placeholder
    // rows. Errors are surfaced in the status line and
    // leave dirty=true so the next input retries.
    function performAutosave() {
      var payload = {
        to: toField.input.value,
        cc: ccWrap.input.value,
        bcc: bccWrap.input.value,
        subject: subjField.input.value,
        body: bodyField.value,
      };
      if (!payload.to && !payload.cc && !payload.bcc &&
          !payload.subject && !payload.body) {
        c.dirty = false;
        return;
      }
      if (!state.compose || state.compose !== c) return;
      // Skip save if content has not changed since last save.
      var snapshot = getCurrentSnapshot();
      if (c.lastSavedSnapshot === snapshot) {
        c.dirty = false;
        return;
      }
      c.dirty = false;
      c.lastSavedSnapshot = snapshot;
      statusLine.textContent = 'Saving…';
      statusLine.classList.add('saving');
      statusLine.classList.remove('saved', 'error');
      var draftID = c.draftID || null;
      saveDraft(Object.assign({ draftID: draftID }, payload))
        .then(function (res) {
          c.draftID = res.id;
          c.lastSavedAt = Date.now();
          refreshStatus();
        })
        .catch(function (e) {
          c.dirty = true;
          statusLine.textContent = 'Save failed: ' + (e.message || 'unknown');
          statusLine.classList.add('error');
          statusLine.classList.remove('saving', 'saved');
        });
    }
    // markDirty is wired to every field. It restarts
    // the autosave timer. The interval is read from
    // state.settings.autosave_seconds (clamped to the
    // 0..60 range the API accepts). 0 disables autosave
    // entirely — the user must hit Save draft manually.
    // The default is 3 seconds, matching the legacy
    // hard-coded value.
    function markDirty() {
      c.dirty = true;
      if (c.autosaveTimer) {
        clearTimeout(c.autosaveTimer);
        c.autosaveTimer = null;
      }
      var s = state.settings || {};
      var seconds = (typeof s.autosave_seconds === 'number')
        ? Math.max(0, Math.min(60, s.autosave_seconds))
        : 3;
      if (seconds <= 0) {
        // Autosave disabled — keep dirty=true so the status
        // line shows "unsaved" and the user is reminded to
        // hit Save draft manually before closing.
        return;
      }
      c.autosaveTimer = setTimeout(performAutosave, seconds * 1000);
    }

    var backdrop = el('div', { class: 'modal-backdrop' });
    var modal = el('div', { class: 'modal', role: 'dialog', 'aria-label': 'Compose message' });
    backdrop.appendChild(modal);

    // Header.
    var mh = el('div', { class: 'modal-header' });
    var titleText =
      c.mode === 'reply'
        ? 'Reply'
        : c.mode === 'replyAll'
        ? 'Reply all'
        : c.mode === 'forward'
        ? 'Forward'
        : c.mode === 'draft'
        ? 'Draft'
        : 'New Message';
    mh.appendChild(el('div', { class: 'title' }, titleText));
    var minimizeBtn = el('button', {
      class: 'icon-btn',
      type: 'button',
      title: 'Minimize',
      'aria-label': 'Minimize',
    }, '—');
    minimizeBtn.addEventListener('click', function () {
      backdrop.remove();
    });
    var closeBtn = el('button', {
      class: 'icon-btn',
      type: 'button',
      title: 'Close',
      'aria-label': 'Close',
    }, '×');
    closeBtn.addEventListener('click', function () {
      if (confirmDiscardDraft()) {
        backdrop.remove();
        state.compose = null;
      }
    });
    mh.appendChild(minimizeBtn);
    mh.appendChild(closeBtn);
    modal.appendChild(mh);

    // Fields. The input listener for each field
    // BOTH updates dirAuto and marks the compose
    // state dirty, which restarts the autosave
    // timer.
    var mb = el('div', { class: 'modal-body' });
    function field(label, value, placeholder, isTextarea) {
      var f = el('div', { class: 'field' });
      f.appendChild(el('div', { class: 'label' }, label));
      var input;
      if (isTextarea) {
        input = el('textarea', { rows: '1', placeholder: placeholder || '' });
      } else {
        input = el('input', {
          type: 'text',
          autocomplete: 'off',
          spellcheck: 'false',
          placeholder: placeholder || '',
        });
      }
      input.value = value || '';
      input.setAttribute('dir', dirAuto(input.value));
      input.addEventListener('input', function () {
        input.setAttribute('dir', dirAuto(input.value));
        markDirty();
      });
      f.appendChild(input);
      return { wrap: f, input: input };
    }
    var toField = field('To', c.to, 'recipient@example.com');
    mb.appendChild(toField.wrap);
    var ccBccToggle = el('button', {
      class: 'field-expander',
      type: 'button',
      title: 'Show Cc / Bcc',
      'aria-label': 'Show Cc and Bcc fields',
      'aria-expanded': 'false',
    }, 'Cc / Bcc');
    ccBccToggle.addEventListener('click', function () {
      var open = ccWrap.classList.toggle('open');
      bccWrap.classList.toggle('open', open);
      ccBccToggle.style.display = open ? 'none' : 'inline-flex';
      ccBccToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
    mb.appendChild(ccBccToggle);
    var ccWrap = field('Cc', c.cc, '');
    ccWrap.wrap.classList.add('cc-bcc');
    mb.appendChild(ccWrap.wrap);
    var bccWrap = field('Bcc', c.bcc, '');
    bccWrap.wrap.classList.add('cc-bcc');
    mb.appendChild(bccWrap.wrap);
    var subjField = field('Subject', c.subject, 'Subject');
    mb.appendChild(subjField.wrap);

    var bodyField = el('textarea', { class: 'compose-body', placeholder: 'Write your message…' });
    bodyField.value = c.body || '';
    bodyField.setAttribute('dir', dirAuto(bodyField.value));
    bodyField.addEventListener('input', function () {
      bodyField.setAttribute('dir', dirAuto(bodyField.value));
      markDirty();
    });
    mb.appendChild(bodyField);
    modal.appendChild(mb);

    // Attachment list — rendered between the body and the footer.
    // Shows files selected for sending. Each file shows its name,
    // size, and a remove button.
    var attachmentsArea = el('div', { class: 'compose-attachments' });
    function renderComposeAttachments() {
      while (attachmentsArea.firstChild) attachmentsArea.removeChild(attachmentsArea.firstChild);
      (c.pendingAttachments || []).forEach(function (att, idx) {
        var row = el('div', { class: 'compose-attachment' });
        var icon = el('span', { class: 'att-icon' });
        icon.textContent = '📎';
        row.appendChild(icon);
        row.appendChild(el('span', { class: 'att-name' }, att.name));
        row.appendChild(el('span', { class: 'att-size' }, formatSize(att.size)));
        var removeBtn = el('button', {
          class: 'btn-remove-att',
          type: 'button',
          title: 'Remove ' + att.name,
          'aria-label': 'Remove ' + att.name,
        }, '×');
        removeBtn.addEventListener('click', function () {
          c.pendingAttachments.splice(idx, 1);
          renderComposeAttachments();
          markDirty();
        });
        row.appendChild(removeBtn);
        attachmentsArea.appendChild(row);
      });
      attachmentsArea.hidden = !c.pendingAttachments || c.pendingAttachments.length === 0;
    }
    // Notice that surfaces when at least one attachment is selected.
    // Drafts persist the body but not the file blobs (the file picker
    // hands File objects straight to the FormData sender); if the user
    // refreshes / closes the tab they will need to re-pick the files.
    var attNotice = el('div', { class: 'compose-attachment-notice', hidden: true });
    attNotice.textContent =
      'Attachments are not saved in drafts yet — if you close or refresh the page, ' +
      'you will need to re-select the files before sending.';
    attachmentsArea.appendChild(attNotice);
    var _renderOrig = renderComposeAttachments;
    renderComposeAttachments = function () {
      _renderOrig();
      attNotice.hidden = !(c.pendingAttachments && c.pendingAttachments.length > 0);
    };
    modal.appendChild(attachmentsArea);

    // Footer.
    var mf = el('div', { class: 'modal-footer' });
    var sendBtn = el('button', { class: 'btn primary', type: 'button' }, 'Send');
    var draftBtn = el('button', { class: 'btn', type: 'button' }, c.draftID ? 'Update draft' : 'Save draft');
    var discardBtn = el('button', { class: 'btn ghost', type: 'button' }, 'Discard');
    var spacer2 = el('div', { class: 'spacer', style: 'flex:1;' });
    var closeFooter = el('button', { class: 'btn ghost', type: 'button' }, 'Close');
    mf.appendChild(sendBtn);
    mf.appendChild(draftBtn);
    mf.appendChild(discardBtn);
    // Attach files button with hidden file input.
    var attachWrap = el('div', { class: 'attach-btn-wrap' });
    var attachInput = el('input', {
      type: 'file',
      id: 'orvix-attach-input',
      multiple: 'multiple',
      title: 'Attach files',
      'aria-label': 'Attach files',
    });
    attachInput.addEventListener('change', function () {
      var files = attachInput.files;
      if (!files || files.length === 0) return;
      if (!c.pendingAttachments) c.pendingAttachments = [];
      for (var i = 0; i < files.length; i++) {
        var f = files[i];
        c.pendingAttachments.push({ file: f, name: f.name, size: f.size });
      }
      renderComposeAttachments();
      markDirty();
      // Reset so the same file can be selected again.
      attachInput.value = '';
    });
    attachWrap.appendChild(attachInput);
    var attachLabel = el('button', { class: 'btn ghost', type: 'button' }, 'Attach');
    attachLabel.addEventListener('click', function () { attachInput.click(); });
    attachWrap.appendChild(attachLabel);
    mf.appendChild(attachWrap);
    mf.appendChild(spacer2);
    // The autosave status line lives in the footer
    // next to the Close button. It is updated by
    // markDirty / performAutosave as the user types.
    mf.appendChild(statusLine);
    mf.appendChild(closeFooter);
    modal.appendChild(mf);

    function gather() {
      return {
        to: toField.input.value.trim(),
        cc: ccWrap.input.value.trim(),
        bcc: bccWrap.input.value.trim(),
        subject: subjField.input.value,
        body: bodyField.value,
      };
    }
    function getCurrentSnapshot() {
      return JSON.stringify(gather());
    }
    function setSending(b) {
      sendBtn.disabled = b;
      draftBtn.disabled = b;
      sendBtn.innerHTML = b ? '<span class="spinner-inline"></span> Sending…' : 'Send';
    }

    // Default per-file / per-message limits. The server is the source of
    // truth — the values here match the API defaults (25 MB / 20 files)
    // so the user sees the same error before or after hitting Send. If
    // the operator changes coremail.max_attachment_size_mb the UI
    // surfaces the next attachment > 25 MB immediately.
    var CLIENT_MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;
    var CLIENT_MAX_ATTACHMENTS = 20;

    function preflightAttachments() {
      var files = c.pendingAttachments || [];
      if (files.length > CLIENT_MAX_ATTACHMENTS) {
        return 'You can attach at most ' + CLIENT_MAX_ATTACHMENTS +
               ' files. Remove ' + (files.length - CLIENT_MAX_ATTACHMENTS) +
               ' before sending.';
      }
      for (var i = 0; i < files.length; i++) {
        var f = files[i];
        var size = f && f.size != null ? f.size : (f && f.file ? f.file.size : 0);
        if (size > CLIENT_MAX_ATTACHMENT_BYTES) {
          var mb = Math.round(size / 1024 / 1024);
          return 'Attachment "' + (f.name || ('file ' + (i + 1))) +
                 '" is ' + mb + ' MB. The maximum is ' +
                 (CLIENT_MAX_ATTACHMENT_BYTES / 1024 / 1024) + ' MB.';
        }
      }
      return null;
    }

    function doSend(payload) {
      var files = c.pendingAttachments || [];
      if (files.length > 0) {
        return sendMessageWithAttachments(payload, files);
      }
      return sendMessage(payload);
    }

    sendBtn.addEventListener('click', function () {
      var payload = gather();
      if (!payload.to) {
        toast('At least one recipient is required', 'error');
        toField.input.focus();
        return;
      }
      // Pre-send attachment validation — surfaces a clear error before
      // the network round-trip. Server is still the source of truth;
      // these are the same limits as the API defaults.
      var attErr = preflightAttachments();
      if (attErr) {
        toast(attErr, 'error', 4500);
        return;
      }
      // Warn-on-empty-subject/body — respects the user's setting.
      var s = state.settings || {};
      if (s.warn_on_empty_subject) {
        var subjEmpty = !(payload.subject || '').trim() || payload.subject === '(no subject)';
        var bodyEmpty = !(payload.body || '').trim();
        if (subjEmpty || bodyEmpty) {
          var parts = [];
          if (subjEmpty) parts.push('empty subject');
          if (bodyEmpty) parts.push('empty body');
          if (!confirm('You are sending with ' + parts.join(' and ') + '. Send anyway?')) {
            return;
          }
        }
      }
      if (c.autosaveTimer) {
        clearTimeout(c.autosaveTimer);
        c.autosaveTimer = null;
      }
      setSending(true);
      doSend(payload)
        .then(function (res) {
          toast(
            'Sent — queued ' +
              (res.queued_count || 1) +
              ' ' +
              (res.queued_count === 1 ? 'recipient' : 'recipients'),
            'success'
          );
          if (c.draftID) {
            deleteDraft(c.draftID).catch(function () { /* non-fatal */ });
          }
          backdrop.remove();
          state.compose = null;
          return loadFolders();
        })
        .then(function () {
          renderFolderSidebar();
          if (state.currentFolderPath.toLowerCase() === 'sent') {
            return loadMessages().then(renderMessageList);
          }
        })
        .catch(function (e) {
          toast('Send failed: ' + e.message, 'error');
        })
        .then(function () {
          setSending(false);
        });
    });

    draftBtn.addEventListener('click', function () {
      var payload = gather();
      payload.draftID = c.draftID || null;
      draftBtn.disabled = true;
      draftBtn.innerHTML = '<span class="spinner-inline"></span> Saving…';
      // A manual save and the autosave share the
      // performAutosave code path semantically — both
      // persist the current state. To avoid two saves
      // firing back-to-back, clear the autosave timer
      // when the user clicks Save explicitly.
      if (c.autosaveTimer) {
        clearTimeout(c.autosaveTimer);
        c.autosaveTimer = null;
      }
      saveDraft(payload)
        .then(function (res) {
          c.draftID = res.id;
          c.lastSavedAt = Date.now();
          c.dirty = false;
          refreshStatus();
          toast('Draft saved', 'success', 1500);
        })
        .catch(function (e) {
          toast('Save draft failed: ' + e.message, 'error');
        })
        .then(function () {
          draftBtn.disabled = false;
          draftBtn.textContent = 'Update draft';
        });
    });

    discardBtn.addEventListener('click', function () {
      var s = state.settings || {};
      // Confirm before discard — respects the user setting.
      if (c.dirty || c.draftID) {
        var msg = c.draftID
          ? 'Discard this draft permanently?'
          : 'Discard this unsent message?';
        if (s.confirm_before_discard !== false && !confirm(msg)) {
          return;
        }
      }
      if (c.draftID) {
        deleteDraft(c.draftID).catch(function () {
          /* non-fatal */
        });
      }
      backdrop.remove();
      state.compose = null;
    });
    closeFooter.addEventListener('click', function () {
      backdrop.remove();
      // Don't null state.compose — user might reopen via drafts.
    });

    backdrop.addEventListener('click', function (ev) {
      if (ev.target === backdrop && confirmDiscardDraft()) {
        backdrop.remove();
        state.compose = null;
      }
    });

    document.body.appendChild(backdrop);
    // Focus the first empty field, or body.
    setTimeout(function () {
      if (!toField.input.value) toField.input.focus();
      else if (!subjField.input.value) subjField.input.focus();
      else bodyField.focus();
    }, 30);
  }

  function confirmDiscardDraft() {
    if (!state.compose || !state.compose.draftID) return true;
    if (state.compose.body && state.compose.body.length > 0) {
      return confirm('Discard this draft? Unsaved changes will be lost.');
    }
    return true;
  }

  // ──────────────────────────────────────────────────────────
  // Boot
  // ──────────────────────────────────────────────────────────

  function init() {
    renderShell();
    els.readingPane = $('reading-pane');

    // Theme: respect the user's OS-level color-scheme
    // preference. The dark theme is the Orvix default;
    // light is opt-in via `prefers-color-scheme: light`
    // OR via the `theme-light` class on <html> (set by
    // embedders that want a forced light skin).
    //
    // The Webmail UI never writes to localStorage /
    // sessionStorage, so we cannot persist a per-user
    // toggle across reloads; the OS preference is the
    // single source of truth. Embedders that want a
    // forced light / dark skin can apply the class on
    // the embedding page before this script loads.
    try {
      var root = document.documentElement;
      if (
        root.classList.contains('theme-light') ||
        (window.matchMedia &&
          window.matchMedia('(prefers-color-scheme: light)').matches &&
          !root.classList.contains('theme-dark'))
      ) {
        root.classList.add('theme-light');
      }
    } catch (e) {
      /* matchMedia not available — keep dark default */
    }

    // User chip.
    loadMe()
      .then(function () {
        var u = state.user;
        if (u) {
          var em = u.email || '';
          var av = $('user-avatar');
          if (av) av.textContent = initials('', em);
          var ue = $('user-email');
          if (ue) ue.textContent = em;
          document.documentElement.setAttribute('dir', dirAuto(em));
        }
      })
      .catch(function (e) {
        // 401 means the auth gate is still running; don't crash.
        if (e.status !== 401) {
          toast('Failed to load profile: ' + e.message, 'error');
        }
      });

    // Settings + push status — best-effort, non-blocking. loadSettings
    // resolves to defaults on failure so the rest of the app keeps
    // working without per-mailbox prefs. loadPushStatusBestEffort
    // is independent — even if push is disabled server-side, the
    // Notifications tab in Settings still renders the current status.
    //
    // The repo has no JS test runner, so the wire-up of each
    // persisted setting is verified manually (see the "Manual
    // verification" section in the branch's final report). To
    // catch obvious regressions without a JS test runner,
    // assertSettingsApplied is a runtime smoke test that runs once
    // after loadSettings resolves. It reads the DOM the wired
    // hooks are supposed to touch and console.warns if the state
    // and the DOM disagree. It never throws — the SPA stays
    // usable and the dev sees the gap in the console.
    function assertSettingsApplied() {
      try {
        var s = state.settings || {};
        var html = document.documentElement;
        var msgs = document.getElementById('messages');
        var problems = [];

        // theme: html must carry a theme-* class.
        var hasThemeClass = html.classList.contains('theme-dark') ||
                            html.classList.contains('theme-light');
        if (s.theme && !hasThemeClass) {
          problems.push('theme: no .theme-light/.theme-dark class on <html>');
        }
        // density: must match s.density.
        var wantsCompact = s.density === 'compact';
        if (wantsCompact !== html.classList.contains('density-compact')) {
          problems.push('density: html class out of sync (wants=' + wantsCompact +
                        ', has=' + html.classList.contains('density-compact') + ')');
        }
        // text_direction: html[dir] must be set.
        var wantDir = (s.text_direction === 'auto' || !s.text_direction)
                      ? 'auto' : s.text_direction;
        if (html.getAttribute('dir') !== wantDir) {
          problems.push('text_direction: html[dir] out of sync (wants=' + wantDir +
                        ', has=' + html.getAttribute('dir') + ')');
        }
        // preview_lines: #messages must carry the matching class.
        var n = (typeof s.preview_lines === 'number') ? s.preview_lines : 2;
        if (msgs && msgs.className.indexOf('preview-lines-' + n) === -1) {
          problems.push('preview_lines: #messages missing .preview-lines-' + n);
        }
        if (problems.length) {
          console.warn('[orvix] settings wire-up gaps: ' + problems.join('; '));
        }
      } catch (e) {
        // Defensive: never block boot on a probe failure.
      }
    }
    loadSettings().then(assertSettingsApplied);
    //
    // loadSettings is awaited before the folder boot so the
    // user-configured default_folder can be applied on first load.
    // If loadSettings fails (offline / 5xx / etc.) it falls back
    // to in-memory defaults AND sets state.settingsLoadFailed so
    // the Settings modal surfaces a clear "failed to load" notice.
    loadPushStatusBestEffort();
    loadSettings().then(function () {
      return loadFolders();
    }).then(function () {
      // Apply the user-configured default_folder when the hash is
      // empty (first load). Existing hash routes still win — the
      // user can deep-link to any folder. The lookup is
      // case-insensitive via state.folderByPath which is populated
      // by loadFolders().
      try {
        if (!location.hash || location.hash === '#' || location.hash === '#/') {
          var def = state.settings && state.settings.default_folder;
          if (def && state.folderByPath && state.folderByPath[def.toLowerCase()]) {
            state.currentFolderPath = def;
          }
        }
      } catch (e) {
        // Non-fatal — keep going with whatever the hash/default is.
      }
      routeFromHash();
    }).catch(function (e) {
      if (e.status === 401) {
        // Auth gate will redirect; nothing to do.
        return;
      }
      toast('Failed to load folders: ' + e.message, 'error');
    });

    // Keyboard shortcuts. We bind on document and ignore
    // events whose target is an input/textarea/contentEditable.
    // Modifier keys also disable the handler so the user's
    // own browser shortcuts (Ctrl+R, Cmd+L, etc.) keep
    // working as expected.
    //
    // Gmail-style "g" prefix: press "g" then a folder
    // initial to jump there. The window expires after
    // G_PREFIX_TIMEOUT_MS of inactivity.
    document.addEventListener('keydown', function (ev) {
      var tag = (ev.target && ev.target.tagName) || '';
      if (/^(INPUT|TEXTAREA|SELECT)$/.test(tag)) return;
      if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
      // "g" prefix: when active, the next letter
      // navigates. We start the window only on a bare
      // "g" keypress so the user can still type "g"
      // in any future contentEditable field without
      // hijacking it.
      if (state.gPrefixActive) {
        state.gPrefixActive = false;
        if (state.gPrefixTimer) clearTimeout(state.gPrefixTimer);
        state.gPrefixTimer = null;
        if (ev.key === 'i') { ev.preventDefault(); navigateToFolder('INBOX'); return; }
        if (ev.key === 's') { ev.preventDefault(); navigateToFolder('Sent'); return; }
        if (ev.key === 'd') { ev.preventDefault(); navigateToFolder('Drafts'); return; }
        if (ev.key === 'a') { ev.preventDefault(); navigateToFolder('Archive'); return; }
        if (ev.key === 't') { ev.preventDefault(); navigateToFolder('Trash'); return; }
        if (ev.key === 'j') { ev.preventDefault(); navigateToFolder('Junk'); return; }
        // Unknown key — fall through to the rest of
        // the handler so the keypress still has a
        // chance of doing something useful.
      }
      if (ev.key === 'c') {
        ev.preventDefault();
        openCompose({ mode: 'new' });
      } else if (ev.key === '/') {
        ev.preventDefault();
        var si = $('search-input');
        if (si) si.focus();
      } else if (ev.key === 'r' && state.selectedMessageID) {
        ev.preventDefault();
        openCompose({ mode: 'reply', replyTo: state.selectedMessage });
      } else if (ev.key === 'a' && state.selectedMessageID) {
        ev.preventDefault();
        openCompose({ mode: 'replyAll', replyTo: state.selectedMessage });
      } else if (ev.key === 'f' && state.selectedMessageID) {
        ev.preventDefault();
        openCompose({ mode: 'forward', replyTo: state.selectedMessage });
      } else if (ev.key === 'j' || ev.key === 'ArrowDown') {
        // Next message in the current list. Walk
        // state.messages; if nothing is selected,
        // start at the top.
        ev.preventDefault();
        moveSelection(1);
      } else if (ev.key === 'k' || ev.key === 'ArrowUp') {
        // Previous message in the current list.
        ev.preventDefault();
        moveSelection(-1);
      } else if (ev.key === 'e' && state.selectedMessageID) {
        // Archive the current message (Gmail "e").
        ev.preventDefault();
        archiveMessage(state.selectedMessageID).then(function () {
          toast('Archived', 'success');
          renderMessageList();
          closeReadingPane();
        }).catch(function (err) {
          toast('Archive failed: ' + err.message, 'error');
        });
      } else if ((ev.key === '#' || ev.key === 'Delete') && state.selectedMessageID) {
        // Move to Trash. Confirm so accidental
        // presses do not destroy state.
        ev.preventDefault();
        if (!confirm('Move this message to Trash?')) return;
        deleteMessage(state.selectedMessageID).then(function () {
          toast('Moved to Trash', 'success');
          renderMessageList();
          closeReadingPane();
        }).catch(function (err) {
          toast('Delete failed: ' + err.message, 'error');
        });
      } else if (ev.key === 's' && state.selectedMessageID) {
        // Star / unstar. The same key is also used by
        // the "g s" prefix (jump to Sent) when the g
        // prefix is active. The state.gPrefixActive
        // branch above handles that case; if we reach
        // here, the user is not in a g-prefix window
        // and wants to star.
        ev.preventDefault();
        var msg = state.selectedMessage;
        updateMessageFlags(msg.id, { flagged: !msg.flagged }).then(function () {
          msg.flagged = !msg.flagged;
          renderMessageList();
          renderReadingPane();
        });
      } else if (ev.key === 'x' && state.selectionMode) {
        // Toggle selection on the current message
        // without opening it.
        ev.preventDefault();
        if (state.selectedMessageID) {
          toggleSelection(state.selectedMessageID, ev);
        }
      } else if (ev.key === 'g') {
        // Arm the g-prefix for the next keypress.
        // The window expires after G_PREFIX_TIMEOUT_MS.
        ev.preventDefault();
        state.gPrefixActive = true;
        if (state.gPrefixTimer) clearTimeout(state.gPrefixTimer);
        state.gPrefixTimer = setTimeout(function () {
          state.gPrefixActive = false;
          state.gPrefixTimer = null;
        }, G_PREFIX_TIMEOUT_MS);
      } else if (ev.key === 'Escape') {
        var back = document.querySelector('.modal-backdrop');
        if (back) {
          // Confirm-discard logic lives on the close
          // button; the Escape key just closes the
          // modal the same way. If the user has
          // unsaved changes the close handler will
          // confirm.
          var closeBtn2 = back.querySelector('.modal-header .icon-btn:last-of-type');
          if (closeBtn2) closeBtn2.click();
        } else if (state.selectionMode) {
          clearSelection();
        } else if (state.readingPaneOpen) {
          closeReadingPane();
        }
      } else if (ev.key === '?') {
        // Show the keyboard shortcut overlay. The
        // overlay is a static DOM element added at
        // boot; we just toggle its visibility.
        ev.preventDefault();
        showShortcutsOverlay();
      }
    });

    // Push notifications: if the bundled webmail-push.js module
    // is present, give it a chance to register the service worker
    // and render its banner / settings entry. The module is a
    // no-op on browsers without Web Push support, so this is safe
    // even when the module is not loaded.
    try {
      if (window.OrvixWebmailPush && typeof window.OrvixWebmailPush.onInit === 'function') {
        window.OrvixWebmailPush.onInit();
      }
    } catch (e) {
      /* push is optional — never block the SPA on it */
    }
  }

  // Public exports (for tests and the auth gate integration).
  // The auth-gate.js calls window.OrvixWebmail.init() once
  // /api/v1/me has confirmed a live session. We do NOT
  // auto-boot on DOMContentLoaded — booting before the gate
  // runs would leak the UI to unauthenticated visitors and
  // trigger a wave of /api/v1/webmail/* calls that 401.
  window.OrvixWebmail = {
    init: init,
    utils: {
      dirAuto: dirAuto,
      linkifyURLs: linkifyURLs,
      sanitiseHTML: sanitiseHTML,
      escapeHTML: escapeHTML,
      renderBody: renderBody,
    },
  };
})();
