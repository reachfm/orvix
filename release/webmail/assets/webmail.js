// Orvix Webmail — real mailbox client (replaces demo bundle).
//
// Loaded by release/webmail/index.html via
//   <script src="/webmail/assets/webmail.js" defer></script>
// AFTER auth-gate.js so the gate can confirm a valid session
// before the React-style render kicks in. The auth gate hides
// #root until /api/v1/me returns 200; this file then reads
// the user's inbox and renders it.
//
// Backend contract (defined in internal/api/handlers/webmail_user.go):
//
//   GET  /api/v1/webmail/me                       -> {user, mailbox}
//   GET  /api/v1/webmail/folders                  -> {folders}
//   GET  /api/v1/webmail/messages?folder=INBOX    -> {messages, folder, folder_id}
//   GET  /api/v1/webmail/messages/:id             -> {id, subject, from, to, rfc822, ...}
//   POST /api/v1/webmail/send                     -> {id, message_id}
//   POST /api/v1/webmail/messages/:id/delete      -> {status}
//
// All requests use credentials:include so the existing
// admin session cookie is sent. No /api/v1/queue usage.

(function () {
  'use strict';

  // Resolve relative to /webmail so it works under any path.
  var API = {
    me: '/api/v1/webmail/me',
    folders: '/api/v1/webmail/folders',
    messages: '/api/v1/webmail/messages',
    message: function (id) { return '/api/v1/webmail/messages/' + encodeURIComponent(id); },
    send: '/api/v1/webmail/send',
    del: function (id) { return '/api/v1/webmail/messages/' + encodeURIComponent(id) + '/delete'; }
  };

  var state = {
    user: null,
    mailbox: null,
    folders: [],
    messages: [],
    currentFolder: 'INBOX',
    currentMessage: null
  };

  function $(id) { return document.getElementById(id); }

  function el(tag, opts, text) {
    var node = document.createElement(tag);
    if (opts) {
      if (opts.className) node.className = opts.className;
      if (opts.href) node.setAttribute('href', opts.href);
      if (opts.type) node.setAttribute('type', opts.type);
    }
    if (text != null) node.textContent = text;
    return node;
  }

  function api(path, opts) {
    opts = opts || {};
    opts.credentials = 'include';
    opts.headers = Object.assign({ 'Accept': 'application/json' }, opts.headers || {});
    return fetch(path, opts).then(function (resp) {
      if (!resp.ok) {
        return resp.text().then(function (body) {
          var msg = 'HTTP ' + resp.status;
          try { var j = JSON.parse(body); if (j && j.error) msg = j.error; } catch (e) {}
          throw new Error(msg);
        });
      }
      var ct = resp.headers.get('content-type') || '';
      if (ct.indexOf('application/json') >= 0) return resp.json();
      return resp.text();
    });
  }

  function apiPost(path, body) {
    return api(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {})
    });
  }

  // ── render: top-level layout ────────────────────────────────
  function renderLayout() {
    var root = $('webmail-root');
    if (!root) {
      root = el('div', { id: 'webmail-root', className: 'webmail' });
      var mountPoint = document.getElementById('root');
      // The auth gate hides #root via inline style once
      // authed, but we attach our root as a sibling so we
      // do not conflict with React-style mount points in
      // case the gate ever lifts.
      if (mountPoint && mountPoint.parentNode) {
        mountPoint.parentNode.appendChild(root);
      } else {
        document.body.appendChild(root);
      }
    }
    while (root.firstChild) root.removeChild(root.firstChild);

    if (!state.user || !state.mailbox) {
      renderEmptyState(root);
      return;
    }

    root.appendChild(renderHeader());
    root.appendChild(renderSidebar());
    root.appendChild(renderMain());
  }

  function renderHeader() {
    var h = el('header', { className: 'wm-header' });
    h.appendChild(el('h1', { className: 'wm-title' }, 'Orvix Webmail'));
    var meta = el('div', { className: 'wm-meta' });
    meta.appendChild(el('span', { className: 'wm-meta-email' }, state.mailbox.email));
    meta.appendChild(document.createTextNode(' '));
    meta.appendChild(el('a', { className: 'wm-meta-admin', href: '/admin' }, 'Admin'));
    h.appendChild(meta);
    return h;
  }

  function renderSidebar() {
    var s = el('aside', { className: 'wm-sidebar' });
    s.appendChild(el('button', { className: 'wm-compose', type: 'button' }, 'Compose'));
    s.querySelector('.wm-compose').addEventListener('click', openCompose);
    var ftitle = el('h2', { className: 'wm-section' }, 'Folders');
    s.appendChild(ftitle);
    var list = el('ul', { className: 'wm-folders' });
    state.folders.forEach(function (f) {
      var li = el('li', { className: 'wm-folder' });
      var btn = el('button', {
        className: 'wm-folder-btn' + (f.path === state.currentFolder ? ' wm-active' : ''),
        type: 'button'
      }, f.name + (f.unread_count > 0 ? ' (' + f.unread_count + ')' : ''));
      btn.dataset.folderPath = f.path;
      btn.addEventListener('click', function () { selectFolder(f.path); });
      li.appendChild(btn);
      list.appendChild(li);
    });
    s.appendChild(list);
    return s;
  }

  function renderMain() {
    var main = el('main', { className: 'wm-main' });
    var toolbar = el('div', { className: 'wm-toolbar' });
    toolbar.appendChild(el('h2', { className: 'wm-toolbar-title' }, state.currentFolder));
    main.appendChild(toolbar);

    if (state.currentMessage) {
      main.appendChild(renderMessageView(state.currentMessage));
    } else {
      main.appendChild(renderMessageList());
    }
    return main;
  }

  function renderEmptyState(root) {
    var card = el('div', { className: 'wm-empty' });
    card.appendChild(el('h2', null, 'No mailbox configured'));
    card.appendChild(el('p', null,
      'The signed-in account does not have a coremail_mailboxes row. ' +
      'Run the installer or contact the administrator.'));
    root.appendChild(card);
  }

  function renderMessageList() {
    if (!state.messages || state.messages.length === 0) {
      var empty = el('div', { className: 'wm-list-empty' });
      empty.appendChild(el('p', null, 'No messages in ' + state.currentFolder + '.'));
      return empty;
    }
    var list = el('ul', { className: 'wm-list' });
    state.messages.forEach(function (m) {
      var li = el('li', { className: 'wm-list-item' + (m.seen ? '' : ' wm-unread') });
      var btn = el('button', { className: 'wm-list-btn', type: 'button' });
      btn.appendChild(el('span', { className: 'wm-list-from' }, m.from || '(unknown)'));
      btn.appendChild(el('span', { className: 'wm-list-subject' }, m.subject || '(no subject)'));
      btn.appendChild(el('span', { className: 'wm-list-date' }, formatDate(m.received_date)));
      btn.addEventListener('click', function () { selectMessage(m.id); });
      li.appendChild(btn);
      list.appendChild(li);
    });
    return list;
  }

  function renderMessageView(msg) {
    var wrap = el('article', { className: 'wm-msg' });
    var toolbar = el('div', { className: 'wm-msg-toolbar' });
    var back = el('button', { className: 'wm-btn', type: 'button' }, 'Back');
    back.addEventListener('click', function () { state.currentMessage = null; renderLayout(); });
    var del = el('button', { className: 'wm-btn wm-btn-danger', type: 'button' }, 'Delete');
    del.addEventListener('click', function () { deleteMessage(msg.id); });
    toolbar.appendChild(back);
    toolbar.appendChild(el('span', { className: 'wm-msg-spacer' }));
    toolbar.appendChild(del);
    wrap.appendChild(toolbar);

    wrap.appendChild(el('h2', { className: 'wm-msg-subject' }, msg.subject || '(no subject)'));
    var meta = el('div', { className: 'wm-msg-meta' });
    meta.appendChild(el('div', null, 'From: ' + (msg.from || '')));
    meta.appendChild(el('div', null, 'To: ' + (msg.to || '')));
    if (msg.cc) meta.appendChild(el('div', null, 'Cc: ' + msg.cc));
    meta.appendChild(el('div', null, 'Date: ' + formatDate(msg.received_date || msg.message_date)));
    wrap.appendChild(meta);

    var body = el('pre', { className: 'wm-msg-body' });
    body.textContent = msg.rfc822 || '(no body)';
    wrap.appendChild(body);
    return wrap;
  }

  function openCompose() {
    if (state.currentMessage) state.currentMessage = null;
    var main = document.querySelector('.wm-main');
    if (!main) { renderLayout(); main = document.querySelector('.wm-main'); }
    while (main.firstChild) main.removeChild(main.firstChild);
    var wrap = el('section', { className: 'wm-compose-form' });
    wrap.appendChild(el('h2', null, 'Compose'));
    var toInput = el('input', { className: 'wm-input', type: 'text' });
    toInput.placeholder = 'To: name@example.com';
    var subInput = el('input', { className: 'wm-input', type: 'text' });
    subInput.placeholder = 'Subject';
    var bodyArea = el('textarea', { className: 'wm-textarea' });
    bodyArea.placeholder = 'Message body';
    bodyArea.rows = 18;
    var actions = el('div', { className: 'wm-compose-actions' });
    var send = el('button', { className: 'wm-btn wm-btn-primary', type: 'button' }, 'Send');
    var cancel = el('button', { className: 'wm-btn', type: 'button' }, 'Cancel');
    cancel.addEventListener('click', function () { renderLayout(); });
    send.addEventListener('click', function () { sendMessage(toInput.value, subInput.value, bodyArea.value); });
    actions.appendChild(cancel);
    actions.appendChild(send);
    wrap.appendChild(el('label', null, 'To'));
    wrap.appendChild(toInput);
    wrap.appendChild(el('label', null, 'Subject'));
    wrap.appendChild(subInput);
    wrap.appendChild(el('label', null, 'Body'));
    wrap.appendChild(bodyArea);
    wrap.appendChild(actions);
    main.appendChild(wrap);
    toInput.focus();
  }

  // ── actions ────────────────────────────────────────────────
  function selectFolder(path) {
    state.currentFolder = path;
    state.currentMessage = null;
    reloadMessages();
  }

  function selectMessage(id) {
    api(API.message(id)).then(function (msg) {
      state.currentMessage = msg;
      renderLayout();
    }).catch(showError);
  }

  function deleteMessage(id) {
    if (!confirm('Delete this message?')) return;
    apiPost(API.del(id)).then(function () {
      state.currentMessage = null;
      reloadMessages();
      reloadFolders();
    }).catch(showError);
  }

  function sendMessage(to, subject, body) {
    if (!to) { showError(new Error('To is required')); return; }
    apiPost(API.send, { to: to, subject: subject, body: body })
      .then(function () {
        renderLayout();
        reloadFolders();
      })
      .catch(showError);
  }

  function reloadMessages() {
    api(API.messages + '?folder=' + encodeURIComponent(state.currentFolder))
      .then(function (data) {
        state.messages = (data && data.messages) || [];
        renderLayout();
      })
      .catch(showError);
  }

  function reloadFolders() {
    api(API.folders).then(function (data) {
      state.folders = (data && data.folders) || [];
      renderLayout();
    }).catch(function () { /* keep last folder list */ });
  }

  function showError(err) {
    var banner = document.querySelector('.wm-error');
    if (!banner) {
      banner = el('div', { className: 'wm-error' });
      banner.setAttribute('role', 'alert');
      var root = $('webmail-root');
      if (root) root.insertBefore(banner, root.firstChild);
    }
    banner.textContent = 'Error: ' + (err && err.message ? err.message : String(err));
    setTimeout(function () { if (banner.parentNode) banner.parentNode.removeChild(banner); }, 6000);
  }

  function formatDate(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var now = new Date();
    var sameDay = d.toDateString() === now.toDateString();
    if (sameDay) {
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }
    return d.toLocaleDateString();
  }

  // ── boot ───────────────────────────────────────────────────
  // The auth gate has already verified /api/v1/me. We
  // re-fetch via the webmail endpoint to also get the
  // mailbox row in one round-trip. If that endpoint
  // returns null mailbox (no coremail_mailboxes row),
  // we render the empty state — the user is signed in
  // but has no mailbox.
  api(API.me).then(function (data) {
    state.user = data && data.user;
    state.mailbox = data && data.mailbox;
    if (state.mailbox) {
      return api(API.folders).then(function (fdata) {
        state.folders = (fdata && fdata.folders) || [];
        renderLayout();
        reloadMessages();
      });
    }
    renderLayout();
  }).catch(showError);
})();
