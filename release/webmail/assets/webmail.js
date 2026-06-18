/* =====================================================================
   Orvix Webmail — production-grade enterprise client.
   Vanilla JS, no external dependencies. Sends credentials with every
   request to /api/v1/webmail/*, never to /api/v1/queue, never stores
   tokens in localStorage (cookies are HttpOnly).

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

  function loadMessage(id) {
    return api('GET', '/api/v1/webmail/messages/' + id).then(function (msg) {
      state.selectedMessage = msg;
      state.selectedMessageID = msg.id;
      // Mark as seen if not already.
      if (!msg.seen) {
        updateMessageFlags(id, { seen: true }).catch(function () {
          /* non-fatal */
        });
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
    // Strip fields the API does not accept.
    var body = {
      to: payload.to || '',
      cc: payload.cc || '',
      bcc: payload.bcc || '',
      subject: payload.subject || '',
      body: payload.body || '',
    };
    return api('POST', '/api/v1/webmail/send', body).then(function (res) {
      if (res.status !== 'queued') {
        // Defensive: WEBMAIL-3 contract says non-queued means
        // error. We've already checked HTTP status in api();
        // this is for the case where status=stored leaks through.
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
    var userChip = el('div', { class: 'user-chip', id: 'user-chip' });
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
      msgs.appendChild(
        el('div', { class: 'loading-state' }, '<span class="spinner"></span> Loading…')
      );
      return;
    }
    if (state.messages.length === 0) {
      msgs.appendChild(
        el(
          'div',
          { class: 'empty-state' },
          state.searchQuery
            ? 'No messages match your search.'
            : 'This folder is empty.'
        )
      );
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
    var fromRow = el('div', { class: 'from', dir: dirAuto(fromName + ' ' + fromEmail) });
    fromRow.appendChild(el('span', { class: 'name' }, fromName || fromEmail || '(unknown)'));
    if (fromEmail && fromName && fromEmail.indexOf(fromName) < 0) {
      fromRow.appendChild(
        el('span', { class: 'badge', dir: 'ltr' }, fromEmail)
      );
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
    view.innerHTML = '<div class="loading-state"><span class="spinner"></span> Loading…</div>';
    var tb = view.parentNode.querySelector('.toolbar');
    if (tb) tb.querySelectorAll('.action-btn').forEach(function (b) {
      b.remove();
    });
  }

  function renderReadingPaneError(err) {
    var view = $('message-view');
    if (!view) return;
    view.innerHTML = '';
    view.appendChild(el('div', { class: 'error-state' }, 'Failed to load: ' + (err.message || 'unknown')));
  }

  function renderReadingPane() {
    var view = $('message-view');
    if (!view) return;
    var msg = state.selectedMessage;
    if (!msg) {
      view.innerHTML = '';
      view.appendChild(el('div', { class: 'empty-state' }, 'Select a message to read.'));
      return;
    }

    // Toolbar actions.
    var readingPane = view.parentNode;
    var tb = readingPane.querySelector('.toolbar');
    tb.querySelectorAll('.action-btn').forEach(function (b) {
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
    var fromNameText = displayName(msg.from) || emailOf(msg.from) || '(unknown sender)';
    fromBlock.appendChild(
      el('div', { class: 'from-name', dir: dirAuto(fromNameText) }, fromNameText)
    );
    var fromEmailText = emailOf(msg.from);
    if (fromEmailText && fromEmailText !== fromNameText) {
      fromBlock.appendChild(
        el('div', { class: 'from-email', dir: 'ltr' }, fromEmailText)
      );
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
      };
      if (opts.mode === 'reply') {
        compose.to = replyTarget;
        compose.subject = withPrefix('Re: ', m.subject || '');
        compose.body = buildQuote(m);
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
        compose.body = buildQuote(m);
      } else if (opts.mode === 'forward') {
        compose.subject = withPrefix('Fwd: ', m.subject || '');
        compose.body =
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
          compose.body =
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
      }
      state.compose = compose;
    } else if (opts.mode === 'draft') {
      state.compose = Object.assign(
        { dirty: false, lastSavedAt: null },
        opts.compose || opts
      );
    } else {
      state.compose = {
        mode: 'new', to: '', cc: '', bcc: '', subject: '',
        body: '', dirty: false, lastSavedAt: null,
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
    return (
      '\n\n' +
      'On ' +
      date +
      ', ' +
      (m.from || '') +
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
      c.dirty = false;
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
    // the autosave timer. After 3 seconds of no
    // further input performAutosave fires. While the
    // modal is open, the timer is the single source of
    // truth — manual save also clears it because the
    // state is now persisted.
    function markDirty() {
      c.dirty = true;
      if (c.autosaveTimer) clearTimeout(c.autosaveTimer);
      c.autosaveTimer = setTimeout(performAutosave, 3000);
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
      class: 'icon-btn',
      type: 'button',
      title: 'Show Cc/Bcc',
      style: 'align-self:flex-start;margin:4px 14px;',
    }, 'Cc / Bcc');
    ccBccToggle.addEventListener('click', function () {
      var open = ccWrap.classList.toggle('open');
      bccWrap.classList.toggle('open', open);
      ccBccToggle.style.display = open ? 'none' : 'inline-flex';
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
    function setSending(b) {
      sendBtn.disabled = b;
      draftBtn.disabled = b;
      sendBtn.innerHTML = b ? '<span class="spinner-inline"></span> Sending…' : 'Send';
    }

    sendBtn.addEventListener('click', function () {
      var payload = gather();
      if (!payload.to) {
        toast('At least one recipient is required', 'error');
        toField.input.focus();
        return;
      }
      // If the user has an unsaved draft, save it
      // first so the Sent copy / the queue row always
      // match the in-memory state. Cancel the autosave
      // timer; the explicit save supersedes it.
      if (c.autosaveTimer) {
        clearTimeout(c.autosaveTimer);
        c.autosaveTimer = null;
      }
      setSending(true);
      sendMessage(payload)
        .then(function (res) {
          toast(
            'Sent — queued ' +
              (res.queued_count || 1) +
              ' ' +
              (res.queued_count === 1 ? 'recipient' : 'recipients'),
            'success'
          );
          // If this was a draft, delete it now — the
          // message is durable in Sent. If the server
          // can't delete the draft, the user still has
          // a clean Sent copy; the orphan draft is a
          // cosmetic problem, not a correctness one.
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
      if (c.draftID) {
        if (!confirm('Discard this draft permanently?')) return;
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

    // Folders then route.
    loadFolders()
      .then(function () {
        routeFromHash();
      })
      .catch(function (e) {
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
