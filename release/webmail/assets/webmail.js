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
    compose: null, // {mode:'new'|'reply'|'replyAll'|'forward'|'draft', draftID, to, cc, bcc, subject, body}
    searchQuery: '',
    sidebarOpen: false,
    readingPaneOpen: false,
  };

  var PAGE_SIZE = 50;

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
        state.messagesHasMore = list.length >= PAGE_SIZE;
        state.messagesPage++;
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
    var title = $('list-title');
    if (state.searchQuery) {
      title.textContent = 'Search: ' + state.searchQuery;
    } else {
      var f = state.folderByPath[state.currentFolderPath.toLowerCase()];
      title.textContent = f ? f.name : state.currentFolderPath;
    }

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
      var more = el('div', { class: 'empty-state', id: 'load-more' });
      more.textContent = state.messagesLoading ? 'Loading…' : 'Load more';
      msgs.appendChild(more);
    }
  }

  function messageRow(m) {
    var row = el('div', {
      class:
        'message' +
        (m.seen ? '' : ' unread') +
        (state.selectedMessageID === m.id ? ' selected' : ''),
      'data-id': m.id,
      role: 'button',
      tabindex: '0',
    });
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
    var preview = m.preview || snippetFromMessage(m);
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

    row.addEventListener('click', function () {
      openMessage(m.id);
    });
    row.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault();
        openMessage(m.id);
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

    // Attachments: present in the API response if any. v1
    // attaches a list of objects {filename, content_type,
    // size_bytes}; the endpoint already pulls them through
    // MailStore. We just render what the server returned.
    if (msg.attachments && msg.attachments.length > 0) {
      var att = el('div', { class: 'attachments' });
      att.appendChild(el('h3', {}, pluralize(msg.attachments.length, 'Attachment', 'Attachments')));
      var ul = el('ul');
      msg.attachments.forEach(function (a) {
        var li = el('li');
        li.appendChild(el('span', { class: 'att-icon' }, '📎'));
        li.appendChild(el('span', { class: 'att-name' }, a.filename || 'attachment'));
        li.appendChild(el('span', { class: 'att-size' }, formatSize(a.size_bytes)));
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
      var sender = emailOf(m.from || '');
      var myEmail = state.user && state.user.email ? state.user.email : '';
      var compose = {
        mode: opts.mode,
        to: '',
        cc: '',
        bcc: '',
        subject: '',
        body: '',
      };
      if (opts.mode === 'reply') {
        compose.to = sender;
        compose.subject = withPrefix('Re: ', m.subject || '');
        compose.body = buildQuote(m);
      } else if (opts.mode === 'replyAll') {
        compose.to = sender;
        var cc = (m.to || '').split(',').map(function (s) {
          return s.trim();
        }).filter(function (e) {
          return e && e.toLowerCase() !== myEmail.toLowerCase();
        });
        // Also include the original cc, minus self.
        if (m.cc) {
          m.cc.split(',').forEach(function (e) {
            e = e.trim();
            if (!e) return;
            if (e.toLowerCase() === myEmail.toLowerCase()) return;
            if (cc.indexOf(e) < 0) cc.push(e);
          });
        }
        compose.cc = cc.join(', ');
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
      }
      state.compose = compose;
    } else if (opts.mode === 'draft') {
      state.compose = opts.compose || opts;
    } else {
      state.compose = { mode: 'new', to: '', cc: '', bcc: '', subject: '', body: '' };
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

    // Fields.
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
          backdrop.remove();
          state.compose = null;
          // Refresh Sent folder count if visible, otherwise
          // refresh whatever folder the user is on.
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
      saveDraft(payload)
        .then(function (res) {
          c.draftID = res.id;
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
    document.addEventListener('keydown', function (ev) {
      var tag = (ev.target && ev.target.tagName) || '';
      if (/^(INPUT|TEXTAREA|SELECT)$/.test(tag)) return;
      if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
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
      } else if (ev.key === 'Escape') {
        var back = document.querySelector('.modal-backdrop');
        if (back) back.remove();
        else if (state.readingPaneOpen) closeReadingPane();
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
