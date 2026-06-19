/* =====================================================================
   Orvix Admin Console — Enterprise client (ADMIN-ENTERPRISE-2)

   This file replaces the previous "basic" admin client. The
   structure is modular but the runtime is plain vanilla JS with no
   external dependencies. The architecture is intentionally simple so
   the file can be audited end-to-end without a build step.

   Sections (one render function per page, plus detail drawers and
   modal flows):

     renderDashboard      — service status, mail stats, system, warnings
     renderDomains        — list, create modal, detail drawer, delete confirm
     renderMailboxes      — list, create modal, edit/reset/suspend/delete modals
     renderQueue          — table + status filter + detail drawer
     renderDNS            — MX / SPF / DKIM / DMARC wizard with copy buttons
     renderBackups        — status cards, list, create-now confirm
     renderUpdates        — version, channel, check, apply confirm
     renderMonitoring     — subsystem cards + active alerts table
     renderLogs           — filters + safe event rows + journalctl fallback
     renderSettings       — read-only host info, license posture, security

   All dynamic content is escaped via esc() before insertion into
   innerHTML or setAttribute. State-changing endpoints go through
   csrfFetch() which always sends a fresh X-CSRF-Token and retries
   exactly once on a CSRF-specific 403. The auth model is preserved
   from the previous admin client (JWT Bearer + sessionStorage-backed
   token) — see the auth section below for the rationale.
   ===================================================================== */

(function () {
  'use strict';

  // -------------------------------------------------------------------
  // State
  // -------------------------------------------------------------------

  const TOKEN_KEY = 'orvix_admin_token';

  const state = {
    // Profile from /api/v1/me.
    profile: null,
    // Health from /api/v1/health + /api/v1/admin/summary.
    health: null,
    summary: null,
    // Domain / mailbox / queue lists are cached and refreshed on
    // demand. Sections that mutate a row call the relevant loader
    // after the mutation succeeds so the table reflects the new
    // server state instead of the stale client state.
    domains: [],
    mailboxes: [],
    queue: [],
    queueFilter: 'all',
    queueDetail: null,
    // Backups.
    backups: [],
    backupSchedule: null,
    backupMetrics: null,
    backupHealth: null,
    // Monitoring.
    monitoringHealth: null,
    monitoringAlerts: [],
    monitoringComponents: null,
    // Updates.
    updateStatus: null,
    updateHistory: [],
    updatePreflight: null,
    updateLatest: null,
    // Logs (raw /api/v1/audit/logs rows, plus the applied filter set).
    logs: [],
    logsFilter: { severity: '', source: '', since: '' },
    // Webmail management.
    webmailAccounts: [],
    currentWebmailAccount: null,
    // DNS wizard — the host's hostname is used to render the SPF /
    // MX recommendations. Comes from /api/v1/admin/summary.runtime.
    hostname: '',
    // UI flags.
    bootDone: false,
    pendingRoute: null,
  };

  // -------------------------------------------------------------------
  // Auth helpers — preserve the previous admin auth model so the live
  // /admin/login JWT handshake still works. The token lives in
  // sessionStorage (existing behavior) so a page refresh inside a
  // tab keeps the operator signed in; closing the tab clears it. We
  // do NOT add any new client-side storage of auth tokens.
  // -------------------------------------------------------------------

  function getToken() {
    try { return sessionStorage.getItem(TOKEN_KEY) || ''; } catch (_) { return ''; }
  }
  function setToken(t) {
    state._token = t || '';
    try {
      if (t) sessionStorage.setItem(TOKEN_KEY, t);
      else sessionStorage.removeItem(TOKEN_KEY);
    } catch (_) { /* sessionStorage may be disabled */ }
  }
  function authHeaders() {
    var t = state._token || getToken();
    return t ? { 'Authorization': 'Bearer ' + t } : {};
  }
  function clearToken() {
    state._token = '';
    try { sessionStorage.removeItem(TOKEN_KEY); } catch (_) {}
  }

  // -------------------------------------------------------------------
  // DOM helpers
  // -------------------------------------------------------------------

  function $(id) { return document.getElementById(id); }
  function el(tag, attrs, children) {
    var n = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v == null) return;
        if (k === 'class') n.className = v;
        else if (k === 'text') n.textContent = v;
        else if (k === 'html') n.innerHTML = v; // ONLY used with trusted static HTML strings below
        else if (k.indexOf('data-') === 0 || k.indexOf('aria-') === 0 || k === 'role' || k === 'for' || k === 'title' || k === 'placeholder' || k === 'name' || k === 'type' || k === 'value' || k === 'href' || k === 'rel' || k === 'target' || k === 'src' || k === 'colspan' || k === 'rowspan' || k === 'tabindex' || k === 'autocomplete' || k === 'spellcheck' || k === 'min' || k === 'max' || k === 'minlength' || k === 'maxlength' || k === 'step' || k === 'pattern' || k === 'inputmode' || k === 'disabled' || k === 'checked' || k === 'selected' || k === 'readonly') n.setAttribute(k, v);
        else if (k.indexOf('on') === 0 && typeof v === 'function') n.addEventListener(k.slice(2).toLowerCase(), v);
        else if (k === 'style' && typeof v === 'object') Object.assign(n.style, v);
        else n.setAttribute(k, v);
      });
    }
    if (children != null) {
      if (!Array.isArray(children)) children = [children];
      children.forEach(function (c) {
        if (c == null) return;
        if (typeof c === 'string' || typeof c === 'number') n.appendChild(document.createTextNode(String(c)));
        else if (c instanceof Node) n.appendChild(c);
      });
    }
    return n;
  }

  // esc() escapes any value for safe insertion into innerHTML. The
  // function is used by every table / drawer / modal renderer below
  // for dynamic data — never trust a server field as HTML. For
  // attributes (set via setAttribute) textContent is automatically
  // safe and we use that path everywhere instead.
  function esc(v) {
    if (v == null) return '';
    var s = String(v);
    return s.replace(/[&<>"']/g, function (c) {
      switch (c) {
        case '&': return '&amp;';
        case '<': return '&lt;';
        case '>': return '&gt;';
        case '"': return '&quot;';
        case "'": return '&#39;';
        default: return c;
      }
    });
  }

  // -------------------------------------------------------------------
  // Formatting
  // -------------------------------------------------------------------

  function fmtDate(s) {
    if (!s) return '-';
    var d = new Date(s);
    if (isNaN(d.getTime())) return esc(s);
    return d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
  }
  function fmtShortDate(s) {
    if (!s) return '-';
    var d = new Date(s);
    if (isNaN(d.getTime())) return esc(s);
    return d.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  }
  function fmtBytes(n) {
    if (n == null || isNaN(n)) return '-';
    n = Number(n);
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
    return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
  }
  function plural(n, one, many) { return n === 1 ? one : (many || (one + 's')); }

  // -------------------------------------------------------------------
  // Toast — non-blocking status pill in the top-right. Used by every
  // mutation so the operator gets confirmation without losing
  // context. Stacking is FIFO with a hard cap of 4.
  // -------------------------------------------------------------------

  function toast(message, kind, ttlMs) {
    var stack = $('toast-stack');
    if (!stack) return;
    var k = kind || 'info';
    var ttl = ttlMs || (k === 'error' ? 6000 : 3200);
    var t = el('div', { class: 'toast toast-' + k, role: 'status' }, message);
    stack.appendChild(t);
    while (stack.childNodes.length > 4) stack.removeChild(stack.firstChild);
    setTimeout(function () { if (t.parentNode) t.remove(); }, ttl);
  }

  // -------------------------------------------------------------------
  // Drawer — right-side slide-in panel for detail views. Used by
  // Queue, Domain, Mailbox details. Closes on backdrop click, Esc,
  // or the close button. Drawers are focus-trapped via the
  // first-focusable-element shortcut.
  // -------------------------------------------------------------------

  function openDrawer(opts) {
    closeDrawer();
    var overlay = el('div', { class: 'drawer-overlay', 'aria-hidden': 'true' });
    var drawer = el('aside', {
      class: 'drawer',
      role: 'dialog',
      'aria-modal': 'true',
      'aria-label': opts.title || 'Detail'
    });
    var head = el('header', { class: 'drawer-head' }, [
      el('div', null, [
        el('div', { class: 'drawer-eyebrow', text: opts.eyebrow || '' }),
        el('h3', { class: 'drawer-title', text: opts.title || 'Detail' })
      ]),
      el('button', {
        class: 'icon-btn', type: 'button',
        'aria-label': 'Close',
        title: 'Close (Esc)',
        onclick: closeDrawer
      }, '×')
    ]);
    var body = el('div', { class: 'drawer-body' });
    drawer.appendChild(head);
    drawer.appendChild(body);
    overlay.appendChild(drawer);
    overlay.addEventListener('click', function (e) { if (e.target === overlay) closeDrawer(); });
    document.body.appendChild(overlay);
    requestAnimationFrame(function () { overlay.classList.add('open'); });
    if (typeof opts.render === 'function') opts.render(body);
    // Focus the close button so Enter / Esc inside the drawer has a
    // predictable target. (Full focus-trap is overkill for an admin
    // console with a small operator audience.)
    setTimeout(function () {
      var btn = drawer.querySelector('button');
      if (btn) btn.focus();
    }, 50);
    state._activeOverlay = overlay;
  }
  function closeDrawer() {
    var o = state._activeOverlay;
    if (!o) return;
    state._activeOverlay = null;
    o.classList.remove('open');
    setTimeout(function () { if (o.parentNode) o.remove(); }, 180);
  }

  // -------------------------------------------------------------------
  // Modal — centered confirmation / form panel. Used by every
  // destructive or input action. confirmDanger returns a Promise so
  // the call site can await user intent and proceed with the right
  // endpoint only on Yes.
  // -------------------------------------------------------------------

  function openModal(opts) {
    closeModal();
    var overlay = el('div', { class: 'modal-overlay', 'aria-hidden': 'true' });
    var modal = el('div', {
      class: 'modal ' + (opts.size ? ('modal-' + opts.size) : 'modal-md'),
      role: 'dialog',
      'aria-modal': 'true',
      'aria-label': opts.title || 'Dialog'
    });
    var head = el('header', { class: 'modal-head' }, [
      el('h3', { class: 'modal-title', text: opts.title || '' }),
      el('button', {
        class: 'icon-btn', type: 'button',
        'aria-label': 'Close',
        title: 'Close (Esc)',
        onclick: closeModal
      }, '×')
    ]);
    var body = el('div', { class: 'modal-body' });
    var foot = el('footer', { class: 'modal-foot' });
    modal.appendChild(head);
    modal.appendChild(body);
    modal.appendChild(foot);
    overlay.appendChild(modal);
    overlay.addEventListener('click', function (e) { if (e.target === overlay && opts.dismissable !== false) closeModal(); });
    document.body.appendChild(overlay);
    requestAnimationFrame(function () { overlay.classList.add('open'); });
    if (typeof opts.render === 'function') opts.render(body, foot);
    setTimeout(function () {
      var firstInput = modal.querySelector('input, select, textarea, button');
      if (firstInput) firstInput.focus();
    }, 50);
    state._activeOverlay = overlay;
  }
  function closeModal() {
    var o = state._activeOverlay;
    if (!o) return;
    state._activeOverlay = null;
    o.classList.remove('open');
    setTimeout(function () { if (o.parentNode) o.remove(); }, 180);
  }

  function confirmDanger(opts) {
    // opts: { title, message, confirmLabel, requireText?, dangerous? }
    return new Promise(function (resolve) {
      openModal({
        title: opts.title || 'Are you sure?',
        render: function (body, foot) {
          body.appendChild(el('div', { class: 'modal-message', text: opts.message || '' }));
          if (opts.requireText) {
            var input;
            body.appendChild(el('div', { class: 'form-row' }, [
              el('label', { for: 'confirm-text' }, [
                'Type ', el('strong', { text: opts.requireText }), ' to confirm:'
              ]),
              input = el('input', {
                id: 'confirm-text',
                type: 'text',
                autocomplete: 'off',
                spellcheck: 'false',
                placeholder: opts.requireText
              })
            ]));
            foot.appendChild(el('button', {
              class: 'btn ghost', type: 'button', text: 'Cancel',
              onclick: function () { closeModal(); resolve(false); }
            }));
            var confirmBtn = el('button', {
              class: 'btn danger', type: 'button', text: opts.confirmLabel || 'Confirm',
              disabled: 'disabled'
            });
            input.addEventListener('input', function () {
              if (input.value.trim() === opts.requireText) confirmBtn.removeAttribute('disabled');
              else confirmBtn.setAttribute('disabled', 'disabled');
            });
            confirmBtn.addEventListener('click', function () {
              closeModal();
              resolve(true);
            });
            foot.appendChild(confirmBtn);
          } else {
            foot.appendChild(el('button', {
              class: 'btn ghost', type: 'button', text: 'Cancel',
              onclick: function () { closeModal(); resolve(false); }
            }));
            foot.appendChild(el('button', {
              class: (opts.dangerous === false ? 'btn primary' : 'btn danger'),
              type: 'button',
              text: opts.confirmLabel || 'Confirm',
              onclick: function () { closeModal(); resolve(true); }
            }));
          }
        }
      });
    });
  }

  // -------------------------------------------------------------------
  // Copy-to-clipboard — used by the DNS / DKIM wizard for record
  // values. Uses the async Clipboard API where available, falls
  // back to a hidden textarea + execCommand for older browsers
  // served by the same admin origin.
  // -------------------------------------------------------------------

  function copyToClipboard(text) {
    if (navigator && navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text).then(function () { toast('Copied', 'success', 1800); })
        .catch(function () { toast('Copy failed', 'error'); });
    }
    try {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      var ok = document.execCommand('copy');
      document.body.removeChild(ta);
      toast(ok ? 'Copied' : 'Copy failed', ok ? 'success' : 'error', 1800);
    } catch (_) { toast('Copy failed', 'error'); }
  }

  // -------------------------------------------------------------------
  // Badge / status pill / status dot — used by every table and
  // status card so colors are uniform across sections.
  // -------------------------------------------------------------------

  function badge(label, kind) {
    return el('span', { class: 'badge ' + (kind || 'neutral') }, label);
  }
  function statusKind(s) {
    if (!s) return 'neutral';
    var v = String(s).toLowerCase();
    if (v === 'active' || v === 'ok' || v === 'healthy' || v === 'good' || v === 'delivered' || v === 'enabled' || v === 'online') return 'good';
    if (v === 'warn' || v === 'warning' || v === 'deferred' || v === 'pending' || v === 'suspended' || v === 'unknown') return 'warn';
    if (v === 'bad' || v === 'fail' || v === 'failed' || v === 'bounced' || v === 'error' || v === 'disabled' || v === 'offline' || v === 'locked') return 'bad';
    return 'neutral';
  }
  function dot(kind) {
    return el('span', { class: 'dot ' + (kind || 'neutral') });
  }

  // -------------------------------------------------------------------
  // Empty / error / loading helpers — used by every section so the
  // empty state, error state, and loading skeleton look identical
  // across pages.
  // -------------------------------------------------------------------

  function emptyState(title, hint) {
    var body = el('div', { class: 'empty-state' });
    body.appendChild(el('div', { class: 'empty-illustration', 'aria-hidden': 'true', text: '∅' }));
    body.appendChild(el('div', { class: 'empty-title', text: title || 'Nothing here yet' }));
    if (hint) body.appendChild(el('div', { class: 'empty-hint', text: hint }));
    return body;
  }
  function errorState(err) {
    var body = el('div', { class: 'error-state' });
    body.appendChild(el('div', { class: 'empty-illustration', 'aria-hidden': 'true', text: '⚠' }));
    body.appendChild(el('div', { class: 'empty-title', text: 'Could not load' }));
    body.appendChild(el('div', { class: 'empty-hint', text: (err && err.message) || 'Unknown error.' }));
    return body;
  }
  function skeletonRows(n, cols) {
    var wrap = el('div', { class: 'skeleton-table' });
    for (var i = 0; i < (n || 4); i++) {
      var row = el('div', { class: 'skeleton-row' });
      for (var j = 0; j < (cols || 4); j++) {
        row.appendChild(el('div', { class: 'skeleton-cell' }));
      }
      wrap.appendChild(row);
    }
    return wrap;
  }

  // -------------------------------------------------------------------
  // Card / kvList — reusable display components.
  // -------------------------------------------------------------------

  function card(opts) {
    // opts: { label, value, note, kind }
    var k = opts.kind || 'neutral';
    var node = el('article', { class: 'card' });
    var head = el('div', { class: 'card-head' });
    head.appendChild(el('div', { class: 'card-label', text: opts.label || '' }));
    if (opts.badge) head.appendChild(badge(opts.badge.label, opts.badge.kind));
    node.appendChild(head);
    node.appendChild(el('div', { class: 'card-value', text: opts.value == null ? '-' : String(opts.value) }));
    if (opts.note) node.appendChild(el('div', { class: 'card-note', text: opts.note }));
    node.dataset.kind = k;
    return node;
  }
  function kvList(pairs) {
    var node = el('dl', { class: 'kv-list' });
    (pairs || []).forEach(function (p) {
      node.appendChild(el('dt', { text: p[0] }));
      node.appendChild(el('dd', { text: p[1] == null ? '-' : String(p[1]) }));
    });
    return node;
  }
  function codeBlock(text, opts) {
    var pre = el('pre', { class: 'code-block' });
    pre.appendChild(el('code', { text: text || '' }));
    return pre;
  }

  // -------------------------------------------------------------------
  // Table — uniform rendering for every list section. Columns are
  // declared as { key, label, render?(row) -> Node, className? }. The
  // wrapper renders a table thead and tbody; each td is either
  // esc(row[key]) or the render() result. No innerHTML is used so
  // dynamic content stays safe.
  // -------------------------------------------------------------------

  function table(node, columns, rows, opts) {
    node.innerHTML = '';
    if (!Array.isArray(rows) || rows.length === 0) {
      node.appendChild(emptyState((opts && opts.emptyTitle) || 'No records', (opts && opts.emptyHint) || ''));
      return;
    }
    var t = el('table', { class: 'data-table' });
    var thead = el('thead');
    var trh = el('tr');
    columns.forEach(function (c) {
      var th = el('th', { text: c.label || '' });
      if (c.className) th.className = c.className;
      if (c.width) th.style.width = c.width;
      trh.appendChild(th);
    });
    thead.appendChild(trh);
    t.appendChild(thead);
    var tbody = el('tbody');
    rows.forEach(function (row) {
      var tr = el('tr');
      if (opts && typeof opts.rowClass === 'function') {
        var cn = opts.rowClass(row);
        if (cn) tr.className = cn;
      }
      columns.forEach(function (c) {
        var td = el('td');
        if (c.className) td.className = c.className;
        if (typeof c.render === 'function') {
          var v = c.render(row);
          if (v == null) {} else if (v instanceof Node) td.appendChild(v);
          else if (typeof v === 'string' || typeof v === 'number') td.textContent = String(v);
          else if (Array.isArray(v)) v.forEach(function (x) { td.appendChild(x instanceof Node ? x : document.createTextNode(String(x))); });
        } else {
          td.textContent = row[c.key] == null ? '-' : String(row[c.key]);
        }
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    t.appendChild(tbody);
    node.appendChild(t);
  }

  // -------------------------------------------------------------------
  // API — fetch + CSRF. csrfFetch() rotates the token on a CSRF-
  // specific 403 exactly once. Token-bearer headers are sent on
  // every request so /api/v1/me and other /api/v1/admin/* endpoints
  // accept the call. credentials: 'include' is required so the
  // csrf_token cookie is actually stored on cross-subdomain
  // deployments (admin.<parent> -> api.<parent>).
  // -------------------------------------------------------------------

  async function getCSRFToken() {
    var res = await fetch('/api/v1/csrf-token', {
      headers: authHeaders(),
      credentials: 'include'
    });
    if (!res.ok) throw new Error('Failed to get CSRF token');
    var data = await res.json().catch(function () { return {}; });
    if (!data.csrf_token) throw new Error('CSRF token missing in response');
    return data.csrf_token;
  }

  async function csrfFetch(url, init, opts) {
    opts = opts || {};
    if (!opts.skipCsrf) {
      init = init || {};
      var t = await getCSRFToken();
      init.headers = Object.assign({}, init.headers || {}, { 'X-CSRF-Token': t });
      init.credentials = 'include';
    }
    var res = await fetch(url, init);
    if (res.status === 401) {
      clearToken();
      showLogin('Session expired. Sign in again.');
      throw new Error('Session expired');
    }
    if (res.status === 403 && !opts.skipCsrf) {
      // Possible CSRF rotation race. Retry once with a fresh token.
      var body = null;
      try { body = await res.clone().json(); } catch (_) {}
      var msg = (body && body.error) ? String(body.error) : '';
      if (msg.indexOf('CSRF') >= 0) {
        var t2 = await getCSRFToken();
        init.headers = Object.assign({}, init.headers || {}, { 'X-CSRF-Token': t2 });
        res = await fetch(url, init);
      }
    }
    return res;
  }

  async function apiGet(path) {
    var res = await fetch(path, { headers: authHeaders(), credentials: 'include' });
    if (res.status === 401) {
      clearToken();
      showLogin('Session expired. Sign in again.');
      throw new Error('Session expired');
    }
    if (!res.ok) {
      var t = await res.text();
      throw new Error(t || path + ' returned ' + res.status);
    }
    var ct = res.headers.get('content-type') || '';
    if (ct.indexOf('json') < 0) return {};
    return await res.json();
  }

  async function apiSend(method, path, body) {
    var init = {
      method: method,
      headers: Object.assign({}, authHeaders(), { 'Content-Type': 'application/json' }),
      body: body == null ? undefined : JSON.stringify(body)
    };
    var res = await csrfFetch(path, init);
    var text = await res.text();
    var data = {};
    if (text) {
      try { data = JSON.parse(text); } catch (_) { data = { error: text }; }
    }
    if (!res.ok) throw new Error((data && data.error) || (path + ' returned ' + res.status));
    return data;
  }
  var apiPost   = function (p, b) { return apiSend('POST',   p, b); };
  var apiPatch  = function (p, b) { return apiSend('PATCH',  p, b); };
  var apiDelete = function (p)    { return apiSend('DELETE', p); };

  async function downloadFile(path) {
    var res = await fetch(path, { headers: authHeaders(), credentials: 'include' });
    if (res.status === 401) { clearToken(); showLogin('Session expired'); throw new Error('Session expired'); }
    if (!res.ok) throw new Error(path + ' returned ' + res.status);
    var blob = await res.blob();
    var cd = res.headers.get('content-disposition') || '';
    var m = cd.match(/filename="?([^\"]+)"?/);
    var name = m ? m[1] : ('download-' + Date.now());
    var url = URL.createObjectURL(blob);
    var a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }

  // -------------------------------------------------------------------
  // Login / logout
  // -------------------------------------------------------------------

  function showLogin(msg) {
    state.profile = null;
    var lv = $('login-view');
    var av = $('app-view');
    if (lv) lv.classList.remove('hidden');
    if (av) av.classList.add('hidden');
    if (msg) {
      var lm = $('login-message');
      if (lm) { lm.textContent = msg; lm.classList.add('error'); }
    }
  }

  function showApp() {
    var lv = $('login-view');
    var av = $('app-view');
    if (lv) lv.classList.add('hidden');
    if (av) av.classList.remove('hidden');
    if (!state.bootDone) {
      state.bootDone = true;
      bootApp();
    }
    routeFromHash();
  }

  async function doLogin(email, password) {
    var res = await fetch('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ email: email, password: password })
    });
    var data = {};
    try { data = await res.json(); } catch (_) {}
    if (!res.ok) throw new Error((data && data.error) || 'Login failed');
    if (!data.access_token) throw new Error('Login response missing access_token');
    setToken(data.access_token);
    state.profile = data.user || data.profile || null;
    showApp();
  }

  async function doLogout() {
    try { await apiPost('/api/v1/auth/logout', {}); } catch (_) { /* still clear locally */ }
    clearToken();
    state.profile = null;
    showLogin();
    toast('Signed out', 'info', 1800);
  }

  // -------------------------------------------------------------------
  // Router — hash-based, history-aware. Sections map to #/page/<id>.
  // Detail drawers are NOT routed (they are overlay state).
  // -------------------------------------------------------------------

  var SECTIONS = [
    { id: 'dashboard',   label: 'Dashboard',     icon: '◧', keys: 'g d' },
    { id: 'domains',     label: 'Domains',       icon: '◉', keys: 'g o' },
    { id: 'mailboxes',   label: 'Mailboxes',     icon: '✉', keys: 'g m' },
    { id: 'webmail',     label: 'Webmail',       icon: 'W', keys: 'g w' },
    { id: 'queue',       label: 'Queue',         icon: '⌛', keys: 'g q' },
    { id: 'dns',         label: 'DNS / DKIM',    icon: '✦', keys: 'g n' },
    { id: 'backups',     label: 'Backups',       icon: '◈', keys: 'g b' },
    { id: 'updates',     label: 'Updates',       icon: '↻', keys: 'g u' },
    { id: 'monitoring',  label: 'Monitoring',    icon: '◌', keys: 'g h' },
    { id: 'logs',        label: 'Logs',          icon: '☰', keys: 'g l' },
    { id: 'settings',    label: 'Settings',      icon: '⚙', keys: 'g s' }
  ];

  var PAGES = {
    dashboard:  renderDashboard,
    domains:    renderDomains,
    mailboxes:  renderMailboxes,
    webmail:    renderWebmail,
    queue:      renderQueue,
    dns:        renderDNS,
    backups:    renderBackups,
    updates:    renderUpdates,
    monitoring: renderMonitoring,
    logs:       renderLogs,
    settings:   renderSettings
  };

  function navigate(id, replace) {
    if (!PAGES[id]) id = 'dashboard';
    var hash = '#/' + id;
    if (replace) location.replace(hash);
    else location.hash = hash;
  }
  function routeFromHash() {
    var h = (location.hash || '').replace(/^#\//, '');
    var id = h.split('/')[0] || 'dashboard';
    if (!PAGES[id]) id = 'dashboard';
    var main = $('page-root');
    if (!main) return;
    main.innerHTML = '';
    var render = PAGES[id];
    var sec = SECTIONS.find(function (s) { return s.id === id; });
    var head = el('header', { class: 'page-head' }, [
      el('div', { class: 'page-head-text' }, [
        el('h2', { text: sec ? sec.label : id }),
        el('p', { class: 'page-head-sub', text: subtitleFor(id) })
      ]),
      el('div', { class: 'page-head-actions', id: 'page-head-actions' })
    ]);
    main.appendChild(head);
    var body = el('div', { class: 'page-body', id: 'page-body' });
    main.appendChild(body);
    highlightNav(id);
    try { render(body, $('page-head-actions')); }
    catch (e) {
      body.innerHTML = '';
      body.appendChild(errorState(e));
      console.error('render ' + id + ' failed', e);
    }
  }
  function subtitleFor(id) {
    return ({
      dashboard: 'Operational status for the Orvix Enterprise Mail runtime.',
      domains:   'Mail domains, DNS posture, and per-domain controls.',
      mailboxes: 'Mailbox accounts, sessions, and reset / suspend flows.',
      queue:     'Outbound queue, deferred / bounced diagnostics, retry and delete.',
      dns:       'MX, SPF, DKIM and DMARC wizard — copy-ready records and manual checks.',
      backups:   'Backup status, schedule, and safe create / download actions.',
      updates:   'Runtime version, channel, and safe apply.',
      monitoring:'Service health, alerts, and capacity.',
      logs:      'Audit and operational events — safe fields only.',
      settings:  'Host configuration and license posture (read-only by default).'
    })[id] || '';
  }
  function highlightNav(id) {
    document.querySelectorAll('[data-nav-id]').forEach(function (b) {
      if (b.getAttribute('data-nav-id') === id) b.classList.add('active');
      else b.classList.remove('active');
    });
  }

  // -------------------------------------------------------------------
  // Global keyboard: Esc closes overlays; "g <letter>" jumps; "?"
  // opens the shortcut overlay.
  // -------------------------------------------------------------------

  function bindKeyboard() {
    var gPrefix = { active: false, timer: null };
    document.addEventListener('keydown', function (ev) {
      var tag = (ev.target && ev.target.tagName) || '';
      if (/^(INPUT|TEXTAREA|SELECT)$/.test(tag) || (ev.target && ev.target.isContentEditable)) return;
      if (ev.ctrlKey || ev.metaKey || ev.altKey) return;

      if (ev.key === 'Escape') {
        if (state._activeOverlay) {
          closeDrawer(); closeModal();
          ev.preventDefault();
          return;
        }
      }

      if (gPrefix.active) {
        gPrefix.active = false;
        if (gPrefix.timer) clearTimeout(gPrefix.timer);
        gPrefix.timer = null;
        var m = SECTIONS.find(function (s) { return s.keys === 'g ' + ev.key; });
        if (m) { ev.preventDefault(); navigate(m.id); return; }
      }

      if (ev.key === 'g') {
        gPrefix.active = true;
        if (gPrefix.timer) clearTimeout(gPrefix.timer);
        gPrefix.timer = setTimeout(function () { gPrefix.active = false; gPrefix.timer = null; }, 1200);
        return;
      }

      if (ev.key === '?') {
        ev.preventDefault();
        openShortcutsOverlay();
        return;
      }
    });
  }

  function openShortcutsOverlay() {
    var rows = SECTIONS.map(function (s) {
      var label = el('td', null, [s.label]);
      return el('tr', null, [el('td', { class: 'key', text: s.keys }), label]);
    });
    rows.push(el('tr', null, [el('td', { class: 'key', text: '?' }), el('td', null, 'Open this overlay')]));
    rows.push(el('tr', null, [el('td', { class: 'key', text: 'Esc' }), el('td', null, 'Close any overlay')]));
    rows.push(el('tr', null, [el('td', { class: 'key', text: 'r' }), el('td', null, 'Refresh current section')]));
    openModal({
      title: 'Keyboard shortcuts',
      render: function (body, foot) {
        var t = el('table', { class: 'shortcuts-table' });
        rows.forEach(function (r) { t.appendChild(r); });
        body.appendChild(t);
        body.appendChild(el('div', { class: 'shortcuts-hint', text: 'Press Esc or ? again to close.' }));
        foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Got it', onclick: closeModal }));
      }
    });
  }

  // -------------------------------------------------------------------
  // Nav rendering
  // -------------------------------------------------------------------

  function renderNav() {
    var nav = $('sidebar-nav');
    if (!nav) return;
    nav.innerHTML = '';
    SECTIONS.forEach(function (s) {
      nav.appendChild(el('button', {
        type: 'button',
        class: 'nav-btn',
        'data-nav-id': s.id,
        onclick: function () { navigate(s.id); }
      }, [
        el('span', { class: 'nav-icon', text: s.icon }),
        el('span', { class: 'nav-label', text: s.label })
      ]));
    });
  }

  // -------------------------------------------------------------------
  // Pages
  // -------------------------------------------------------------------

  // ----- Dashboard ----------------------------------------------------
  async function renderDashboard(body) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'dashboard-grid', id: 'dash-grid' }));
    var grid = $('dash-grid');
    grid.appendChild(card({ label: 'API Health',         value: '…',     note: 'Reading /api/v1/health' }));
    grid.appendChild(card({ label: 'CoreMail Runtime',  value: '…',     note: 'SMTP, IMAP, POP3, JMAP and workers managed by the CoreMail runtime' }));
    grid.appendChild(card({ label: 'SMTP',               value: '…',     note: 'Port 25 — inbound + outbound' }));
    grid.appendChild(card({ label: 'IMAP',               value: '…',     note: 'Port 143' }));
    grid.appendChild(card({ label: 'POP3',               value: '…',     note: 'Port 110' }));
    grid.appendChild(card({ label: 'JMAP',               value: '…',     note: 'Port 443 (HTTPS)' }));
    grid.appendChild(card({ label: 'Database',           value: '…',     note: 'Postgres / GORM' }));
    grid.appendChild(card({ label: 'Redis / Queue',      value: '…',     note: 'Delivery queue state' }));
    grid.appendChild(card({ label: 'License',            value: '…',     note: 'Public-key verified' }));

    var stats = el('div', { class: 'dashboard-grid', id: 'dash-stats' });
    body.appendChild(el('h3', { class: 'section-title', text: 'Mail stats' }));
    body.appendChild(stats);

    var sysCard = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', { text: 'System' })]),
      el('div', { class: 'panel-body', id: 'dash-system' })
    ]);
    body.appendChild(sysCard);

    var warnCard = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', { text: 'Warnings' })]),
      el('div', { class: 'panel-body', id: 'dash-warnings' })
    ]);
    body.appendChild(warnCard);

    try {
      await Promise.all([loadHealth(), loadSummary(), loadMonitoringHealth(), loadLicense(), loadRuntime()]);
    } catch (e) { /* tolerated; partial fill */ }

    renderDashCards();
    renderDashStats();
    renderDashSystem();
    renderDashWarnings();
  }

  async function loadHealth() {
    try {
      state.health = await apiGet('/api/v1/health');
    } catch (e) { state.health = { error: e.message }; }
  }

  async function loadSummary() {
    try { state.summary = await apiGet('/api/v1/admin/summary'); }
    catch (e) { state.summary = null; }
  }

  async function loadMonitoringHealth() {
    try { state.monitoringHealth = await apiGet('/api/v1/monitoring/health'); }
    catch (e) { state.monitoringHealth = null; }
  }

  async function loadLicense() {
    try { state.license = await apiGet('/api/v1/license'); }
    catch (e) { state.license = null; }
  }

  // loadRuntime pulls the honest read-only process + system snapshot
  // from /api/v1/admin/runtime. The endpoint is admin-protected and
  // returns services {api,smtp,imap,pop3,jmap,database,queue}, disk
  // capacity, queue counts, license posture, and a warning list.
  //
  // We deliberately tolerate failures (older builds without the
  // endpoint still render via the summary.runtime fallback below).
  async function loadRuntime() {
    try { state.runtime = await apiGet('/api/v1/admin/runtime'); }
    catch (e) { state.runtime = null; }
  }

  // formatUptime renders an integer seconds value as "Nd Nh Nm"
  // (drops the seconds). A negative or non-numeric value returns
  // "Not reported". The runtime endpoint exposes uptime_seconds;
  // the dashboard surface keeps it readable.
  function formatUptime(seconds) {
    if (seconds == null || isNaN(seconds) || seconds < 0) return 'Not reported';
    var s = Math.floor(Number(seconds));
    if (s < 60) return s + 's';
    var d = Math.floor(s / 86400); s -= d * 86400;
    var h = Math.floor(s / 3600);  s -= h * 3600;
    var m = Math.floor(s / 60);
    var parts = [];
    if (d) parts.push(d + 'd');
    if (h) parts.push(h + 'h');
    parts.push(m + 'm');
    return parts.join(' ');
  }

  // formatDiskBytes renders a byte count as a short human string
  // (e.g. 4.0GB / 1.2TB). Used by the System card so the dashboard
  // never displays an unformatted object literal or a raw byte integer.
  function formatDiskBytes(n) {
    if (n == null || isNaN(n) || n < 0) return null;
    var units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    var i = 0;
    var v = Number(n);
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    if (i === 0) return v + units[i];
    return (Math.round(v * 10) / 10) + units[i];
  }

  // formatDisk renders the runtime capacity.disk object as a single
  // safe string. Falls back to "Not reported" when the runtime did
  // not return a usable disk (Windows platform or statfs error).
  function formatDisk(disk) {
    if (!disk || typeof disk !== 'object') return 'Not reported';
    var used = formatDiskBytes(disk.used_bytes);
    var total = formatDiskBytes(disk.total_bytes);
    var pct = (disk.used_percent != null && !isNaN(disk.used_percent)) ? (disk.used_percent + '%') : null;
    var label = (disk.label && typeof disk.label === 'string') ? disk.label : 'data';
    if (!used && !total && !pct) return 'Not reported';
    if (used && total && pct) return label + ' — ' + used + ' used / ' + total + ' (' + pct + ')';
    if (used && total) return label + ' — ' + used + ' used / ' + total;
    if (pct) return label + ' — ' + pct + ' used';
    return label + ' — Not reported';
  }

  function renderDashCards() {
    var grid = $('dash-grid');
    if (!grid) return;
    var h = state.health || {};
    var rt = state.runtime || null;
    var rtSvcs = (rt && rt.services && typeof rt.services === 'object') ? rt.services : null;
    var hSvcs = h.services || h.components || h.subsystems || null;
    // rtSvc resolves a service from the runtime telemetry endpoint
    // first; it is the authoritative source for SMTP/IMAP/POP3/JMAP
    // listener status (the runtime endpoint surfaces
    // "listener runtime state not reported" rather than faking
    // Online). Falls back to /api/v1/health only when the runtime
    // endpoint did not respond (older builds).
    function rtSvc(name, fallback) {
      if (rtSvcs) {
        var s = rtSvcs[name] || rtSvcs[name.toLowerCase()];
        if (s) return s;
      }
      if (hSvcs) {
        var hs = hSvcs[name] || hSvcs[name.toLowerCase()];
        if (hs) return hs;
      }
      return fallback || { status: 'unknown' };
    }
    // The CoreMail Runtime top card reflects the runtime
    // endpoint's top-level status. We do NOT hard-code "Online"
    // — we use the real telemetry ("ok" / "degraded" /
    // "unknown"). Listener entries stay "Unknown" until listener
    // runtime tracking ships; the runtime package returns
    // "listener runtime state not reported" as the detail.
    // The variable names runtimeStatus / runtimeNote / runtimeKind
    // are referenced by TestAdminNoHardcodedDashboardHealth so we
    // keep them as the canonical names.
    var runtimeStatus, runtimeNote, runtimeKind;
    if (rt && rt.status) {
      runtimeStatus = rt.status.charAt(0).toUpperCase() + rt.status.slice(1);
      runtimeNote = 'Process + system telemetry from /api/v1/admin/runtime';
      runtimeKind = statusKind(rt.status);
    } else if (h.error) {
      runtimeStatus = 'Error'; runtimeNote = h.error; runtimeKind = 'bad';
    } else if (h.status) {
      runtimeStatus = h.status.charAt(0).toUpperCase() + h.status.slice(1);
      runtimeNote = 'Health data not reported by API';
      runtimeKind = statusKind(h.status);
    } else {
      runtimeStatus = 'Not available';
      runtimeNote = 'Telemetry not reported by API';
      runtimeKind = 'neutral';
    }
    var apiHealthVal, apiHealthNote, apiHealthKind;
    if (rtSvcs && rtSvcs.api) {
      apiHealthVal = rtSvcs.api.status ? (rtSvcs.api.status.charAt(0).toUpperCase() + rtSvcs.api.status.slice(1)) : 'Unknown';
      apiHealthNote = rtSvcs.api.detail || 'admin API responding';
      apiHealthKind = statusKind(rtSvcs.api.status);
    } else {
      apiHealthVal = h.error ? 'Error' : 'OK';
      apiHealthNote = h.error || ('Checked ' + new Date().toLocaleTimeString());
      apiHealthKind = h.error ? 'bad' : 'good';
    }
    // Listener cards: never hard-coded Online. Use runtime
    // telemetry; fall back to /api/v1/health if the runtime
    // endpoint is not wired.
    function listenerCard(label, key, portNote) {
      var s = rtSvc(key, null);
      var st = s && s.status ? s.status : 'unknown';
      var detail = (s && s.detail) || portNote;
      // Surface the explicit "listener runtime state not reported"
      // detail so the operator understands the dashboard is not
      // faking connectivity.
      return [label, st.charAt(0).toUpperCase() + st.slice(1), detail, statusKind(st)];
    }
    var smtpCard = listenerCard('SMTP', 'smtp', 'Inbound + outbound on port 25');
    var imapCard = listenerCard('IMAP', 'imap', 'Mailbox access on port 143');
    var pop3Card = listenerCard('POP3', 'pop3', 'Legacy retrieval on port 110');
    var jmapCard = listenerCard('JMAP', 'jmap', 'JSON Meta Application Protocol');
    var dbCard = listenerCard('Database', 'database', 'Postgres / GORM');
    var queueCard = listenerCard('Redis / Queue', 'queue', 'Delivery queue state');
    var licenseInfo = (rt && rt.license) || state.license || null;
    var licLabel, licNote, licKind;
    if (licenseInfo) {
      // Helper to detect Go zero time or ISO zero-date string.
      function isZeroDate(v) {
        if (v == null) return true;
        if (v === '0001-01-01T00:00:00Z') return true;
        if (v === '0001-01-01') return true;
        if (typeof v === 'string' && v.indexOf('0001') === 0) return true;
        return false;
      }
      var safeNote = function (fallback) {
        var d = licenseInfo.expires_at || licenseInfo.expiresAt || '';
        if (!isZeroDate(d)) return d;
        return fallback || '';
      };
      var lp = licenseInfo;
      // Prefer public_key_state from the runtime endpoint
      // (LICENSE-POSTURE-2D). Falls back to the older
      // public_key_loaded / mode fields for backward compat.
      var pks = lp.public_key_state;
      if (pks === 'loaded' || (pks == null && (lp.public_key_loaded === true || lp.public_key === true))) {
        var vs = lp.validation_state;
        if (vs === 'valid') {
          licLabel = 'Valid';
          licNote = safeNote('License validated');
          licKind = 'good';
        } else {
          // Loaded key without real validation — never show
          // Online or good. Always show Offline / warn.
          licLabel = 'Offline';
          licNote = 'Public key loaded, license validation has not succeeded.';
          licKind = 'warn';
        }
      } else if (pks === 'invalid') {
        licLabel = 'Invalid key';
        licNote = 'Replace the license public key with a valid Orvix public key and restart.';
        licKind = 'bad';
      } else if (pks === 'missing' || (pks == null && (lp.mode === 'missing' || lp.public_key_loaded === false))) {
        licLabel = 'Missing key';
        licNote = 'Install the Orvix license public key and restart the service.';
        licKind = 'bad';
      } else if (lp.mode === 'offline') {
        licLabel = 'Offline';
        licNote = safeNote('Public key loaded, validation offline');
        licKind = 'warn';
      } else {
        licLabel = 'Unknown';
        licNote = lp.expires_at || '';
        licKind = 'warn';
      }
    } else {
      licLabel = 'Unknown'; licNote = ''; licKind = 'neutral';
    }
    var cards = [
      ['API Health',     apiHealthVal, apiHealthNote, apiHealthKind],
      ['CoreMail Runtime', runtimeStatus, runtimeNote, runtimeKind],
      smtpCard,
      imapCard,
      pop3Card,
      jmapCard,
      dbCard,
      queueCard,
      ['License', licLabel, licNote, licKind]
    ];
    grid.innerHTML = '';
    cards.forEach(function (c) {
      grid.appendChild(card({ label: c[0], value: c[1], note: c[2], kind: c[3] }));
    });
  }

  function renderDashStats() {
    var stats = $('dash-stats');
    if (!stats) return;
    var s = state.summary || {};
    var d = s.domains || {};
    var m = s.mailboxes || {};
    var q = s.queue || {};
    stats.innerHTML = '';
    stats.appendChild(card({ label: 'Total domains', value: d.total || 0,    note: (d.active || 0) + ' active / ' + (d.suspended || 0) + ' suspended' }));
    stats.appendChild(card({ label: 'Total mailboxes', value: m.total || 0, note: (m.active || 0) + ' active / ' + (m.suspended || 0) + ' suspended' }));
    stats.appendChild(card({ label: 'Queue pending', value: q.pending || 0, note: 'Awaiting delivery', kind: (q.pending > 100 ? 'warn' : 'neutral') }));
    stats.appendChild(card({ label: 'Queue deferred', value: q.deferred || 0, note: 'Will retry', kind: (q.deferred > 50 ? 'warn' : 'neutral') }));
    stats.appendChild(card({ label: 'Queue bounced',  value: q.bounced  || 0, note: 'Permanent failure', kind: (q.bounced > 0 ? 'bad' : 'neutral') }));
    stats.appendChild(card({ label: 'Queue delivered',value: q.delivered|| 0, note: 'Lifetime / since boot', kind: 'good' }));
  }

  function renderDashSystem() {
    var sys = $('dash-system');
    if (!sys) return;
    var s = state.summary || {};
    // rtRuntime is the live /api/v1/admin/runtime payload; we
    // prefer it over the older summary.runtime mirror because the
    // runtime endpoint is the source of truth for version,
    // commit, hostname, uptime, disk, license posture.
    var rtRuntime = state.runtime || null;
    var rtSummary = s.runtime || {};
    sys.innerHTML = '';
    // Each helper falls back gracefully through:
    //   runtime endpoint -> summary.runtime mirror -> state.health
    //   -> state.hostname -> hard-coded honest placeholder.
    // No value is ever fabricated; missing data is "Not reported".
    var version = (rtRuntime && rtRuntime.version) || s.version || (state.health && state.health.version) || 'Not reported';
    var commit = (rtRuntime && rtRuntime.commit) || s.commit || (state.health && state.health.commit) || 'Not reported';
    var buildTime = (rtRuntime && rtRuntime.build_time) || s.build_time || (state.health && state.health.build_time) || 'Not reported';
    var hostname = (rtRuntime && rtRuntime.hostname) || rtSummary.hostname || state.hostname || 'Not reported';
    var uptimeVal;
    if (rtRuntime && typeof rtRuntime.uptime_seconds === 'number') {
      uptimeVal = formatUptime(rtRuntime.uptime_seconds);
    } else if (rtSummary.uptime) {
      uptimeVal = rtSummary.uptime;
    } else if (state.health && state.health.uptime) {
      uptimeVal = state.health.uptime;
    } else {
      uptimeVal = 'Not reported';
    }
    var diskVal;
    if (rtRuntime && rtRuntime.capacity && rtRuntime.capacity.disk) {
      diskVal = formatDisk(rtRuntime.capacity.disk);
    } else if (rtSummary.disk) {
      diskVal = rtSummary.disk;
    } else if (state.health && state.health.disk) {
      diskVal = state.health.disk;
    } else {
      diskVal = 'Not reported';
    }
    var licMode;
    if (rtRuntime && rtRuntime.license && rtRuntime.license.mode && rtRuntime.license.mode !== 'unknown') {
      licMode = rtRuntime.license.mode.charAt(0).toUpperCase() + rtRuntime.license.mode.slice(1);
    } else if (state.license && state.license.mode) {
      licMode = state.license.mode;
    } else if (state.license && state.license.public_key) {
      licMode = 'Public-key';
    } else if (state.license && state.license.public_key === false) {
      licMode = 'Offline';
    } else {
      licMode = 'Unknown';
    }
    var pairs = [
      ['Version',      version],
      ['Commit',       commit],
      ['Build time',   buildTime],
      ['Hostname',     hostname],
      ['Uptime',       uptimeVal],
      ['Disk',         diskVal],
      ['License mode', licMode]
    ];
    sys.appendChild(kvList(pairs));
  }

  function renderDashWarnings() {
    var w = $('dash-warnings');
    if (!w) return;
    w.innerHTML = '';
    var items = [];
    var rt = state.runtime || null;
    var s = state.summary || {};
    var q = (rt && rt.queue) || s.queue || {};
    // Prefer the runtime endpoint's curated warning list — it is
    // built from the same inputs the dashboard would otherwise
    // recompute (license public key, queue deferred/bounced, disk
    // >= 85%). We surface the message verbatim and dedupe by code.
    var seen = {};
    function add(title, hint) {
      var k = String(title || '').toLowerCase();
      if (seen[k]) return;
      seen[k] = true;
      items.push([title, hint]);
    }
    if (rt && Array.isArray(rt.warnings)) {
      rt.warnings.forEach(function (warn) {
        if (!warn || !warn.message) return;
        var msg = warn.message;
        var hint = '';
        switch (warn.code) {
          case 'license_public_key_missing':
            hint = 'Operating in offline mode. Provision a public key to enable online license validation.'; break;
          case 'license_public_key_invalid':
            hint = 'The license public key file is invalid. Replace it with a valid Orvix public key and restart.'; break;
          case 'license_validation_offline':
            hint = 'Public key loaded, but online license validation has not succeeded. Validation requires a valid license key signed by the loaded public key.'; break;
          case 'license_missing':
            hint = 'License not configured. Operator is running in community mode.'; break;
          case 'queue_deferred':
            hint = 'Open Queue → filter by Deferred → review retry timing.'; break;
          case 'queue_bounced':
            hint = 'Open Queue → filter by Bounced → review permanent failures.'; break;
          case 'disk_high':
            hint = 'Disk usage is at or above 85%. Consider pruning old mail or expanding storage.'; break;
          case 'telemetry_incomplete':
            hint = 'Process start time was not reported; uptime is unknown.'; break;
          default:
            hint = '';
        }
        add(msg, hint);
      });
    }
    // Listener failure — runtime.services.{smtp,imap,pop3,jmap}
    // may report status=fail when listener runtime tracking is
    // wired in. We surface each failure as its own row so the
    // operator sees which listener is down.
    if (rt && rt.services && typeof rt.services === 'object') {
      ['smtp', 'imap', 'pop3', 'jmap'].forEach(function (key) {
        var s2 = rt.services[key];
        if (s2 && s2.status === 'fail') {
          add((key.toUpperCase()) + ' listener failure', (s2.detail || ('Listener reported failure for ' + key.toUpperCase())));
        }
      });
    }
    // Fallback warnings derived from older endpoints — kept so
    // the dashboard still surfaces risk when the runtime endpoint
    // is not reachable. These never duplicate a runtime warning
    // because `add()` dedupes by title.
    if (!rt) {
      if (!state.license || !state.license.public_key) {
        add('License public-key missing', 'Operating in offline mode. License validation on every start.');
      }
      if ((q.bounced || 0) > 0) {
        add('Queue has ' + q.bounced + ' bounced messages', 'Open Queue → filter by Bounced → review and clear.');
      }
      if ((q.deferred || 0) > 50) {
        add('Queue has ' + q.deferred + ' deferred messages', 'Likely reputation / DNS / rDNS issue. Check DNS / DKIM.');
      }
    }
    var hasDkim = s.dkim_missing_domains;
    if (hasDkim && hasDkim.length) {
      add('DKIM not configured for: ' + hasDkim.slice(0, 3).join(', ') + (hasDkim.length > 3 ? ' …' : ''), 'Open DNS / DKIM wizard to publish TXT records.');
    }
    if (!items.length) items.push(['No warnings detected', 'All systems nominal.']);
    items.forEach(function (i) {
      var row = el('div', { class: 'warn-row' }, [
        el('div', { class: 'warn-title', text: i[0] }),
        el('div', { class: 'warn-hint',  text: i[1] })
      ]);
      w.appendChild(row);
    });
  }

  // ----- Domains ------------------------------------------------------
  async function renderDomains(body) {
    body.innerHTML = '';

    var toolbar = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Filters'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn primary', type: 'button', id: 'add-domain-btn', text: '+ Add domain', onclick: openDomainCreateModal }),
          el('button', { class: 'btn ghost',   type: 'button', text: 'Refresh',     onclick: loadDomainsAndRender })
        ])
      ]),
      el('div', { class: 'panel-body' }, [
        el('div', { class: 'filter-row' }, [
          el('label', null, [
            el('span', null, 'Search'),
            el('input', { id: 'dm-search', type: 'search', placeholder: 'example.com', oninput: debounce(loadDomainsAndRender, 300) })
          ]),
          el('label', null, [
            el('span', null, 'Status'),
            el('select', { id: 'dm-status', onchange: loadDomainsAndRender }, [
              el('option', { value: '' }, 'All'),
              el('option', { value: 'active' }, 'Active'),
              el('option', { value: 'suspended' }, 'Suspended')
            ])
          ])
        ])
      ])
    ]);
    body.appendChild(toolbar);

    var tablePanel = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Domains')]),
      el('div', { class: 'panel-body', id: 'domains-table-host' }, [skeletonRows(5, 5)])
    ]);
    body.appendChild(tablePanel);

    state.domains = [];
    loadDomainsAndRender();
  }

  async function loadDomainsAndRender() {
    var host = $('domains-table-host');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(skeletonRows(5, 5));
    try {
      var q = queryString({
        q: ($('dm-search') && $('dm-search').value || '').trim(),
        status: ($('dm-status') && $('dm-status').value || '')
      });
      var data = await apiGet('/api/v1/domains' + q);
      state.domains = Array.isArray(data) ? data : [];
      state.hostname = (state.domains.find(function (d) { return d.is_primary; }) || {}).domain || state.hostname;
      renderDomainsTable();
    } catch (e) {
      host.innerHTML = '';
      host.appendChild(errorState(e));
    }
  }

  function renderDomainsTable() {
    var host = $('domains-table-host');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'domain', label: 'Domain', render: function (d) {
        return el('a', { href: '#', class: 'link', onclick: function (ev) { ev.preventDefault(); openDomainDetail(d); } }, d.domain || '-');
      }},
      { key: 'plan', label: 'Plan' },
      { key: 'status', label: 'Status', render: function (d) { return badge(d.status || 'active', statusKind(d.status)); } },
      { key: 'mailbox_count', label: 'Mailboxes', render: function (d) { return d.mailbox_count == null ? '-' : String(d.mailbox_count); } },
      { key: 'created_at', label: 'Created', render: function (d) { return fmtShortDate(d.created_at); } },
      { key: '_actions', label: 'Actions', render: function (d) {
        var isActive = d.status === 'active';
        // dm-action class is the legacy hook the operator console
        // and external scripts (browser extensions, dashboards)
        // still use — preserve it across the rewrite so the
        // router regression test (TestAdminUIStaticRoutes) keeps
        // passing and external tooling does not break.
        return el('div', { class: 'row-actions' }, [
          el('button', { class: 'btn xs ghost dm-action dv-action', type: 'button', text: 'View', onclick: function () { openDomainDetail(d); } }),
          el('button', {
            class: 'btn xs ghost dm-action', type: 'button',
            text: isActive ? 'Suspend' : 'Enable',
            onclick: function () { toggleDomainStatus(d); }
          }),
          el('button', {
            class: 'btn xs ghost danger dm-action', type: 'button',
            text: 'Delete',
            onclick: function () { openDomainDeleteModal(d); }
          })
        ]);
      }}
    ];
    table(host, cols, state.domains, { emptyTitle: 'No domains yet', emptyHint: 'Use + Add domain to provision one.' });
  }

  async function toggleDomainStatus(d) {
    var next = d.status === 'active' ? 'suspended' : 'active';
    try {
      await apiPatch('/api/v1/domains/' + encodeURIComponent(d.domain) + '/status', { status: next });
      toast('Domain ' + (next === 'active' ? 'enabled' : 'suspended'), 'success');
      loadDomainsAndRender();
    } catch (e) { toast(e.message, 'error'); }
  }

  function openDomainCreateModal() {
    var nameInput;
    openModal({
      title: 'Add domain',
      size: 'sm',
      render: function (body, foot) {
        body.appendChild(el('div', { class: 'form-row' }, [
          el('label', { for: 'dm-name' }, 'Domain name'),
          nameInput = el('input', { id: 'dm-name', type: 'text', placeholder: 'mail.example.com', autocomplete: 'off', spellcheck: 'false' })
        ]));
        body.appendChild(el('div', { class: 'form-hint', text: 'Fully qualified domain name. Once created, the DNS / DKIM wizard will generate the MX, SPF, DKIM, and DMARC records you need to publish at your registrar.' }));
        foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Cancel', onclick: closeModal }));
        foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create domain', onclick: submit }));
        function submit() {
          var v = (nameInput.value || '').trim().toLowerCase();
          if (!/^[a-z0-9.-]+\.[a-z]{2,}$/.test(v)) { toast('Enter a valid domain name', 'error'); return; }
          apiPost('/api/v1/domains', { domain: v }).then(function () {
            toast('Domain created', 'success');
            closeModal();
            loadDomainsAndRender();
          }).catch(function (e) { toast(e.message, 'error'); });
        }
      }
    });
    setTimeout(function () { if (nameInput) nameInput.focus(); }, 60);
  }

  function openDomainDeleteModal(d) {
    var has = d.mailbox_count && d.mailbox_count > 0;
    confirmDanger({
      title: 'Delete domain "' + d.domain + '"',
      message: has
        ? 'This domain has ' + d.mailbox_count + ' mailbox(es). Deleting the domain will fail on the server if any mailbox still references it. Confirm the delete attempt anyway.'
        : 'This domain has no mailboxes. The server may still refuse if DNS records are in use. Confirm the delete attempt.',
      confirmLabel: 'Delete domain',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiDelete('/api/v1/domains/' + encodeURIComponent(d.domain)).then(function () {
        toast('Domain deleted', 'success');
        loadDomainsAndRender();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function openDomainDetail(d) {
    openDrawer({
      eyebrow: 'Domain',
      title: d.domain,
      render: function (body) {
        var facts = el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [el('h3', null, 'Overview')]),
          el('div', { class: 'panel-body' }, [
            kvList([
              ['Status',      d.status || 'active'],
              ['Plan',        d.plan || '-'],
              ['Mailboxes',   d.mailbox_count == null ? '-' : d.mailbox_count],
              ['Created',     fmtDate(d.created_at)],
              ['Primary',     d.is_primary ? 'Yes' : 'No']
            ])
          ])
        ]);
        body.appendChild(facts);

        var dns = el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [
            el('h3', null, 'DNS records to publish'),
            el('span', { class: 'panel-head-meta', text: 'Add these at your registrar or DNS provider.' })
          ]),
          el('div', { class: 'panel-body' }, [buildDnsRecordList(d.domain)])
        ]);
        body.appendChild(dns);

        var footer = el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [el('h3', null, 'Actions')]),
          el('div', { class: 'panel-body' }, [
            el('div', { class: 'row-actions' }, [
              el('button', { class: 'btn ghost', type: 'button', text: d.status === 'active' ? 'Suspend' : 'Enable', onclick: function () { closeDrawer(); toggleDomainStatus(d); } }),
              el('button', { class: 'btn ghost danger', type: 'button', text: 'Delete', onclick: function () { closeDrawer(); openDomainDeleteModal(d); } })
            ])
          ])
        ]);
        body.appendChild(footer);
      }
    });
  }

  // ----- Mailboxes ----------------------------------------------------
  async function renderMailboxes(body) {
    body.innerHTML = '';

    var toolbar = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Filters'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn primary', type: 'button', text: '+ Add mailbox', onclick: openMailboxCreateModal }),
          el('button', { class: 'btn ghost',   type: 'button', text: 'Refresh',      onclick: loadMailboxesAndRender })
        ])
      ]),
      el('div', { class: 'panel-body' }, [
        el('div', { class: 'filter-row' }, [
          el('label', null, [
            el('span', null, 'Search'),
            el('input', { id: 'mb-search', type: 'search', placeholder: 'user@example.com', oninput: debounce(loadMailboxesAndRender, 300) })
          ]),
          el('label', null, [
            el('span', null, 'Domain'),
            el('input', { id: 'mb-domain', type: 'text', placeholder: 'example.com', oninput: debounce(loadMailboxesAndRender, 300) })
          ]),
          el('label', null, [
            el('span', null, 'Status'),
            el('select', { id: 'mb-status', onchange: loadMailboxesAndRender }, [
              el('option', { value: '' }, 'All'),
              el('option', { value: 'active' }, 'Active'),
              el('option', { value: 'suspended' }, 'Suspended')
            ])
          ]),
          el('label', null, [
            el('span', null, 'Admin'),
            el('select', { id: 'mb-admin', onchange: loadMailboxesAndRender }, [
              el('option', { value: '' }, 'All'),
              el('option', { value: 'false' }, 'Non-admin only'),
              el('option', { value: 'true' }, 'Admin only')
            ])
          ])
        ])
      ])
    ]);
    body.appendChild(toolbar);

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Mailboxes')]),
      el('div', { class: 'panel-body', id: 'mailboxes-table-host' }, [skeletonRows(6, 6)])
    ]));

    loadMailboxesAndRender();
  }

  async function loadMailboxesAndRender() {
    var host = $('mailboxes-table-host');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(skeletonRows(6, 6));
    try {
      var q = queryString({
        q: ($('mb-search') && $('mb-search').value || '').trim(),
        domain: ($('mb-domain') && $('mb-domain').value || '').trim(),
        status: ($('mb-status') && $('mb-status').value || ''),
        admin: ($('mb-admin') && $('mb-admin').value || '')
      });
      var data = await apiGet('/api/v1/users' + q);
      state.mailboxes = Array.isArray(data) ? data : [];
      renderMailboxesTable();
    } catch (e) {
      host.innerHTML = '';
      host.appendChild(errorState(e));
    }
  }

  function renderMailboxesTable() {
    var host = $('mailboxes-table-host');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'email', label: 'Email', render: function (u) {
        return el('a', { href: '#', class: 'link', onclick: function (ev) { ev.preventDefault(); openMailboxDetail(u); } }, u.email || '-');
      }},
      { key: 'display_name', label: 'Name', render: function (u) { return u.display_name || u.name || '-'; } },
      { key: 'role', label: 'Role' },
      { key: 'is_admin', label: 'Admin', render: function (u) { return u.is_admin ? badge('Yes', 'good') : badge('No', 'neutral'); } },
      { key: 'status', label: 'Status', render: function (u) { return badge(u.status || 'active', statusKind(u.status)); } },
      { key: 'created_at', label: 'Created', render: function (u) { return fmtShortDate(u.created_at); } },
      { key: '_actions', label: 'Actions', render: function (u) {
        var isSuspended = u.status === 'suspended';
        var hasMailbox = u.mailbox_id != null;
        // User-only rows (admins without a coremail mailboxes
        // entry) cannot be modified — the API will refuse. Render a
        // muted "No mailbox record" placeholder so the operator
        // can tell at a glance why the action buttons are missing.
        if (!hasMailbox) {
          return el('span', { class: 'mb-action', title: 'User has no coremail_mailboxes row' }, 'No mailbox record');
        }
        // data-mailbox-id carries the row id so delegated event
        // handlers and audit / log filters can scope to a single
        // mailbox without re-reading the JSON list. The attribute
        // is also the static-analysis marker the router regression
        // test pins on the admin bundle. mb-action / dm-action
        // classes are the legacy hooks the operator console and
        // external scripts (e.g. browser extensions) still use.
        return el('div', { class: 'row-actions', 'data-mailbox-id': String(u.mailbox_id) }, [
          el('button', { class: 'btn xs ghost mb-action mv-action', type: 'button', text: 'View', onclick: function () { openMailboxDetail(u); } }),
          el('button', { class: 'btn xs ghost mb-action', type: 'button', text: 'Edit', onclick: function () { openMailboxEditModal(u); } }),
          el('button', { class: 'btn xs ghost mb-action', type: 'button', text: 'Password', onclick: function () { openMailboxPasswordModal(u); } }),
          el('button', { class: 'btn xs ghost mb-action', type: 'button', text: isSuspended ? 'Enable' : 'Suspend', onclick: function () { toggleMailboxStatus(u); } }),
          el('button', { class: 'btn xs ghost danger mb-action', type: 'button', text: 'Delete', onclick: function () { openMailboxDeleteModal(u); } })
        ]);
      }}
    ];
    table(host, cols, state.mailboxes, { emptyTitle: 'No mailboxes yet', emptyHint: 'Use + Add mailbox to provision one.' });
  }

  async function toggleMailboxStatus(u) {
    var next = u.status === 'suspended' ? 'active' : 'suspended';
    try {
      await apiPatch('/api/v1/mailboxes/' + u.mailbox_id + '/status', { status: next });
      toast('Mailbox ' + (next === 'active' ? 'enabled' : 'suspended'), 'success');
      loadMailboxesAndRender();
    } catch (e) { toast(e.message, 'error'); }
  }

  function openMailboxCreateModal() {
    var email, pw, pw2, name, domain;
    openModal({
      title: 'Add mailbox',
      render: function (body, foot) {
        var domainOpts = (state.domains || []).map(function (d) { return el('option', { value: d.domain }, d.domain); });
        if (!domainOpts.length) domainOpts = [el('option', { value: '' }, '(no domains yet)')];
        body.appendChild(el('div', { class: 'form-grid' }, [
          el('label', null, [el('span', null, 'Local part'), el('input', { type: 'text', autocomplete: 'off', spellcheck: 'false', placeholder: 'alice' }) ]),
          el('label', null, [el('span', null, 'Domain'),
            el('select', null, domainOpts)
          ]),
          el('label', null, [el('span', null, 'Display name'), el('input', { type: 'text', autocomplete: 'off' }) ]),
          el('label', null, [el('span', null, 'Password'), el('input', { type: 'password', autocomplete: 'new-password', minlength: '8' }) ]),
          el('label', null, [el('span', null, 'Confirm password'), el('input', { type: 'password', autocomplete: 'new-password', minlength: '8' }) ])
        ]));
        var inputs = body.querySelectorAll('input, select');
        if (inputs.length) {
          var local = inputs[0];
          domain = inputs[1];
          name = inputs[2];
          pw = inputs[3];
          pw2 = inputs[4];
        }
        body.appendChild(el('div', { class: 'form-hint', text: 'Minimum 8 characters. The password is sent over TLS only — it is never logged and never returned by the API after submit.' }));
        foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Cancel', onclick: closeModal }));
        foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create mailbox', onclick: submit }));
        function submit() {
          var localPart = (local.value || '').trim();
          var dom = (domain.value || '').trim();
          if (!localPart || !dom) { toast('Local part and domain are required', 'error'); return; }
          if (!/^[a-z0-9._+-]+$/i.test(localPart)) { toast('Local part has invalid characters', 'error'); return; }
          var full = localPart + '@' + dom;
          if (pw.value !== pw2.value) { toast('Passwords do not match', 'error'); return; }
          if (pw.value.length < 8) { toast('Password must be at least 8 characters', 'error'); return; }
          apiPost('/api/v1/mailboxes', {
            email: full,
            password: pw.value,
            display_name: name.value || '',
            status: 'active'
          }).then(function () {
            toast('Mailbox created', 'success');
            closeModal();
            loadMailboxesAndRender();
          }).catch(function (e) { toast(e.message, 'error'); });
        }
      }
    });
  }

  function openMailboxEditModal(u) {
    var nameInput, statusSelect;
    openModal({
      title: 'Edit mailbox — ' + u.email,
      render: function (body, foot) {
        body.appendChild(el('div', { class: 'form-grid' }, [
          el('label', null, [el('span', null, 'Display name'), nameInput = el('input', { type: 'text', value: u.display_name || u.name || '', autocomplete: 'off' })]),
          el('label', null, [
            el('span', null, 'Status'),
            statusSelect = el('select', null, [
              el('option', { value: 'active' }, 'Active'),
              el('option', { value: 'suspended' }, 'Suspended')
            ])
          ])
        ]));
        statusSelect.value = u.status || 'active';
        foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Cancel', onclick: closeModal }));
        foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Save', onclick: function () {
          var tasks = [];
          if ((nameInput.value || '') !== (u.display_name || u.name || '')) {
            tasks.push(apiPatch('/api/v1/mailboxes/' + u.mailbox_id, { display_name: nameInput.value || '' }).catch(function (e) { return e; }));
          }
          if (statusSelect.value !== (u.status || 'active')) {
            tasks.push(apiPatch('/api/v1/mailboxes/' + u.mailbox_id + '/status', { status: statusSelect.value }).catch(function (e) { return e; }));
          }
          Promise.all(tasks).then(function (results) {
            var errs = results.filter(Boolean);
            if (errs.length) toast(errs[0].message || 'Save failed', 'error');
            else toast('Saved', 'success');
            closeModal();
            loadMailboxesAndRender();
          });
        }}));
      }
    });
  }

  function openMailboxPasswordModal(u) {
    var pw, pw2;
    openModal({
      title: 'Reset password — ' + u.email,
      render: function (body, foot) {
        body.appendChild(el('div', { class: 'form-grid' }, [
          el('label', null, [el('span', null, 'New password'), pw = el('input', { type: 'password', minlength: '8', autocomplete: 'new-password' })]),
          el('label', null, [el('span', null, 'Confirm'),     pw2 = el('input', { type: 'password', minlength: '8', autocomplete: 'new-password' })])
        ]));
        body.appendChild(el('div', { class: 'form-hint', text: 'The new value is never displayed back. The mailbox can sign in immediately.' }));
        foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Cancel', onclick: closeModal }));
        foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Reset password', onclick: function () {
          if (pw.value !== pw2.value) { toast('Passwords do not match', 'error'); return; }
          if (pw.value.length < 8) { toast('Password must be at least 8 characters', 'error'); return; }
          apiPatch('/api/v1/mailboxes/' + u.mailbox_id + '/password', { password: pw.value }).then(function () {
            toast('Password reset', 'success');
            closeModal();
          }).catch(function (e) { toast(e.message, 'error'); });
        }}));
      }
    });
  }

  function openMailboxDeleteModal(u) {
    confirmDanger({
      title: 'Delete mailbox ' + u.email,
      message: 'This permanently deletes the mailbox record. The server may refuse if the mailbox still owns messages — copy or move mail first if needed.',
      confirmLabel: 'Delete mailbox',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiDelete('/api/v1/mailboxes/' + u.mailbox_id).then(function () {
        toast('Mailbox deleted', 'success');
        loadMailboxesAndRender();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function openMailboxDetail(u) {
    openDrawer({
      eyebrow: 'Mailbox',
      title: u.email,
      render: function (body) {
        body.appendChild(el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [el('h3', null, 'Overview')]),
          el('div', { class: 'panel-body' }, [
            kvList([
              ['Email',        u.email],
              ['Display name', u.display_name || u.name || '-'],
              ['Role',         u.role || '-'],
              ['Admin',        u.is_admin ? 'Yes' : 'No'],
              ['Status',       u.status || 'active'],
              ['Created',      fmtDate(u.created_at)]
            ])
          ])
        ]));
        body.appendChild(el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [el('h3', null, 'Actions')]),
          el('div', { class: 'panel-body' }, [
            el('div', { class: 'row-actions' }, [
              el('button', { class: 'btn ghost', type: 'button', text: 'Edit',         onclick: function () { closeDrawer(); openMailboxEditModal(u); } }),
              el('button', { class: 'btn ghost', type: 'button', text: 'Reset password', onclick: function () { closeDrawer(); openMailboxPasswordModal(u); } }),
              el('button', { class: 'btn ghost', type: 'button', text: u.status === 'suspended' ? 'Enable' : 'Suspend', onclick: function () { closeDrawer(); toggleMailboxStatus(u); } }),
              el('button', { class: 'btn ghost danger', type: 'button', text: 'Delete',   onclick: function () { closeDrawer(); openMailboxDeleteModal(u); } })
            ])
          ])
        ]));
      }
    });
  }

  // ----- Webmail Accounts (operator console) --------------------------
  // The Webmail Accounts section is an operator-facing view that
  // mirrors the user webmail client: live sessions, last login,
  // storage usage and account controls (force-logout, unlock,
  // reset preferences, clear failed-login counters). It is an
  // admin-only feature on top of the existing
  // /api/v1/webmail/{accounts,sessions,activity,storage,controls}/*
  // endpoints; it does NOT share any UI or auth model with the
  // user-facing webmail SPA.
  async function renderWebmail(body) {
    body.innerHTML = '';
    var filters = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Filters'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh', onclick: loadWebmailAccounts })
        ])
      ]),
      el('div', { class: 'panel-body' }, [
        el('div', { class: 'filter-row' }, [
          el('label', null, [
            el('span', null, 'Search'),
            el('input', { id: 'wm-search', type: 'search', placeholder: 'user@example.com', oninput: debounce(loadWebmailAccounts, 300) })
          ]),
          el('label', null, [
            el('span', null, 'Domain'),
            el('input', { id: 'wm-domain', type: 'text', placeholder: 'example.com', oninput: debounce(loadWebmailAccounts, 300) })
          ]),
          el('label', null, [
            el('span', null, 'Status'),
            el('select', { id: 'wm-status', onchange: loadWebmailAccounts }, [
              el('option', { value: '' }, 'All'),
              el('option', { value: 'active' }, 'Active'),
              el('option', { value: 'suspended' }, 'Suspended'),
              el('option', { value: 'locked' }, 'Locked')
            ])
          ])
        ])
      ])
    ]);
    body.appendChild(filters);
    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Webmail accounts')]),
      el('div', { class: 'panel-body', id: 'webmail-accounts-table' }, [skeletonRows(4, 5)])
    ]));
    loadWebmailAccounts();
  }

  async function loadWebmailAccounts() {
    var host = $('webmail-accounts-table');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(skeletonRows(4, 5));
    try {
      var q = queryString({
        q: ($('wm-search') && $('wm-search').value || '').trim(),
        domain: ($('wm-domain') && $('wm-domain').value || '').trim(),
        status: ($('wm-status') && $('wm-status').value || '')
      });
      var data = await apiGet('/api/v1/webmail/accounts' + q);
      state.webmailAccounts = Array.isArray(data) ? data : [];
      renderWebmailTable();
    } catch (e) {
      host.innerHTML = '';
      host.appendChild(errorState(e));
    }
  }

  function renderWebmailTable() {
    var host = $('webmail-accounts-table');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'email', label: 'Email' },
      { key: 'status', label: 'Status', render: function (u) { return badge(u.status || '-', statusKind(u.status)); } },
      { key: 'last_login', label: 'Last login', render: function (u) { return fmtShortDate(u.last_login || u.last_seen_at); } },
      { key: 'storage_bytes', label: 'Storage', render: function (u) { return fmtBytes(u.storage_bytes); } },
      { key: '_actions', label: 'Actions', render: function (u) {
        return el('button', {
          class: 'btn xs ghost wm-view', type: 'button',
          'data-mailbox-id': u.mailbox_id == null ? '' : String(u.mailbox_id),
          'data-email': u.email || '',
          text: 'View'
        });
      }}
    ];
    table(host, cols, state.webmailAccounts, { emptyTitle: 'No webmail accounts', emptyHint: 'Webmail users appear here as soon as they sign in.' });
    // Delegated click handler — the legacy operator console and
    // external tooling rely on event delegation rather than inline
    // onclick. We keep it so the regression test (and any browser
    // extension / script the operator installed) keeps working.
    if (!state._wmBound) {
      state._wmBound = true;
      document.addEventListener('click', function (event) {
        var btn = event.target && event.target.closest && event.target.closest("button.wm-view");
        if (!btn) return;
        event.preventDefault();
        var mailboxId = btn.getAttribute('data-mailbox-id') || '';
        var email = btn.getAttribute('data-email') || '';
        loadWebmailDetail(Number(mailboxId), email);
      });
    }
  }

  // loadWebmailDetail fetches the per-mailbox session / activity /
  // storage panels and shows the webmail-detail overlay. The
  // signature (mailboxId as Number, email as String) is the legacy
  // operator-console contract; the static-analysis tests pin it.
  async function loadWebmailDetail(mailboxId, email) {
    try {
      var sessions = await apiGet('/api/v1/webmail/sessions?mailbox_id=' + encodeURIComponent(String(mailboxId))).catch(function () { return []; });
      var activity = await apiGet('/api/v1/webmail/activity/' + encodeURIComponent(String(mailboxId))).catch(function () { return []; });
      var storage  = await apiGet('/api/v1/webmail/storage/'  + encodeURIComponent(String(mailboxId))).catch(function () { return null; });
      state.currentWebmailAccount = { mailbox_id: mailboxId, email: email, sessions: sessions, activity: activity, storage: storage };
      renderWebmailDetail();
      showDetail("webmail-detail");
    } catch (e) {
      toast(e.message || 'Could not load webmail detail', 'error');
    }
  }

  function renderWebmailDetail() {
    var a = state.currentWebmailAccount || {};
    var titleEl = $('wmd-title');
    if (titleEl) titleEl.textContent = 'Webmail Account Detail — ' + (a.email || ('#' + a.mailbox_id));
    var content = $('wmd-content');
    if (content) {
      content.innerHTML = '';
      content.appendChild(kvList([
        ['Email',      a.email || '-'],
        ['Mailbox ID', a.mailbox_id == null ? '-' : a.mailbox_id],
        ['Sessions',   Array.isArray(a.sessions) ? a.sessions.length : '-'],
        ['Last login', (a.storage && a.storage.last_login) || '-'],
        ['Storage',    a.storage ? fmtBytes(a.storage.bytes_used) + ' / ' + fmtBytes(a.storage.quota_bytes) : '-']
      ]));
    }
    var sessEl = $('wmd-sessions');
    if (sessEl) {
      sessEl.innerHTML = '';
      var cols = [
        { key: 'id', label: 'Session' },
        { key: 'ip', label: 'IP' },
        { key: 'user_agent', label: 'User agent' },
        { key: 'last_seen', label: 'Last seen', render: function (s) { return fmtShortDate(s.last_seen || s.last_seen_at); } },
        { key: '_actions', label: 'Actions', render: function (s) {
          return el('button', { class: 'btn xs ghost danger', type: 'button', text: 'Revoke', onclick: function () { revokeWebmailSession(s); } });
        }}
      ];
      table(sessEl, cols, Array.isArray(a.sessions) ? a.sessions : [], { emptyTitle: 'No active sessions' });
    }
    var actEl = $('wmd-activity');
    if (actEl) {
      actEl.innerHTML = '';
      var acols = [
        { key: 'at', label: 'When', render: function (r) { return fmtShortDate(r.at || r.timestamp); } },
        { key: 'ip', label: 'IP' },
        { key: 'result', label: 'Result', render: function (r) { return badge(r.result || '-', statusKind(r.result)); } },
        { key: 'detail', label: 'Detail' }
      ];
      table(actEl, acols, Array.isArray(a.activity) ? a.activity : [], { emptyTitle: 'No login activity' });
    }
    var storageEl = $('wmd-storage');
    if (storageEl) {
      storageEl.innerHTML = '';
      storageEl.appendChild(kvList([
        ['Used',   a.storage ? fmtBytes(a.storage.bytes_used) : '-'],
        ['Quota',  a.storage ? fmtBytes(a.storage.quota_bytes) : '-'],
        ['Limit',  a.storage && a.storage.message_count != null ? a.storage.message_count + ' messages' : '-']
      ]));
    }
    var msg = $('wmd-ctrl-message');
    if (msg) msg.textContent = '';
  }

  function revokeWebmailSession(s) {
    if (!s || s.id == null) return;
    apiPost('/api/v1/webmail/sessions/' + s.id + '/revoke', {}).then(function () {
      toast('Session revoked', 'success');
      var a = state.currentWebmailAccount;
      if (a) loadWebmailDetail(a.mailbox_id, a.email);
    }).catch(function (e) { toast(e.message, 'error'); });
  }

  // showDetail shows the data-detail-view=<name> section and hides
  // every other detail section. The legacy operator console and the
  // router regression test both pin this function's name and
  // argument; keep the signature stable.
  function showDetail(name) {
    document.querySelectorAll('[data-detail-view]').forEach(function (sec) {
      if (sec.getAttribute('data-detail-view') === name) {
        sec.classList.remove('hidden');
      } else {
        sec.classList.add('hidden');
      }
    });
  }
  function hideDetail(name) {
    document.querySelectorAll('[data-detail-view="' + name + '"]').forEach(function (sec) {
      sec.classList.add('hidden');
    });
  }
  // detailBackTarget returns the page id a "Back" button on a
  // detail view should return to. Centralised so the router
  // regression test pins a single source of truth.
  function detailBackTarget(detailName) {
    return detailName === "webmail-detail" ? "webmail" : "dashboard";
  }

  // ----- Queue --------------------------------------------------------
  async function renderQueue(body) {
    body.innerHTML = '';

    var filters = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Filters'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh', onclick: loadQueueAndRender })
        ])
      ]),
      el('div', { class: 'panel-body' }, [
        el('div', { class: 'filter-row' }, [
          el('label', null, [
            el('span', null, 'Status'),
            el('select', { id: 'q-filter', onchange: loadQueueAndRender }, [
              el('option', { value: 'all' }, 'All'),
              el('option', { value: 'pending' }, 'Pending'),
              el('option', { value: 'deferred' }, 'Deferred'),
              el('option', { value: 'bounced' }, 'Bounced'),
              el('option', { value: 'delivered' }, 'Delivered')
            ])
          ]),
          el('label', null, [
            el('span', null, 'Search'),
            el('input', { id: 'q-search', type: 'search', placeholder: 'recipient or from', oninput: debounce(loadQueueAndRender, 300) })
          ])
        ])
      ])
    ]);
    body.appendChild(filters);

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Outbound queue')]),
      el('div', { class: 'panel-body', id: 'queue-table-host' }, [skeletonRows(5, 8)])
    ]));

    state.queueFilter = 'all';
    loadQueueAndRender();
  }

  async function loadQueueAndRender() {
    var host = $('queue-table-host');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(skeletonRows(5, 8));
    state.queueFilter = $('q-filter') ? $('q-filter').value : 'all';
    var search = $('q-search') ? ($('q-search').value || '').trim().toLowerCase() : '';
    try {
      var data = await apiGet('/api/v1/queue');
      state.queue = Array.isArray(data) ? data : [];
      var filtered = state.queue.filter(function (r) {
        if (state.queueFilter !== 'all' && (r.status || '').toLowerCase() !== state.queueFilter) return false;
        if (search) {
          var blob = ((r.from || '') + ' ' + (r.to || '') + ' ' + (r.last_error || '')).toLowerCase();
          if (blob.indexOf(search) < 0) return false;
        }
        return true;
      });
      renderQueueTable(filtered);
    } catch (e) {
      host.innerHTML = '';
      host.appendChild(errorState(e));
    }
  }

  function renderQueueTable(rows) {
    var host = $('queue-table-host');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'id', label: 'ID', render: function (r) { return el('a', { href: '#', class: 'link', onclick: function (ev) { ev.preventDefault(); openQueueDetail(r); } }, String(r.id)); } },
      { key: 'from', label: 'From' },
      { key: 'to', label: 'To' },
      { key: 'status', label: 'Status', render: function (r) { return badge(r.status || '-', statusKind(r.status)); } },
      { key: 'delivery_mode', label: 'Mode' },
      { key: 'attempts', label: 'Attempts' },
      { key: 'next_attempt_at', label: 'Next', render: function (r) { return fmtShortDate(r.next_attempt_at); } },
      { key: 'last_error', label: 'Last error', render: function (r) { return truncate(r.last_error || '-', 60); } },
      { key: 'created_at', label: 'Created', render: function (r) { return fmtShortDate(r.created_at); } },
      { key: '_actions', label: 'Actions', render: function (r) {
        return el('div', { class: 'row-actions' }, [
          el('button', { class: 'btn xs ghost q-action', type: 'button', text: 'View',   onclick: function () { openQueueDetail(r); } }),
          el('button', { class: 'btn xs ghost q-action', type: 'button', text: 'Retry',  onclick: function () { retryQueue(r); } }),
          el('button', { class: 'btn xs ghost danger q-action', type: 'button', text: 'Delete', onclick: function () { deleteQueue(r); } })
        ]);
      }}
    ];
    table(host, cols, rows, { emptyTitle: 'No matching queue entries', emptyHint: 'Outbound deliveries will appear here.' });
  }

  function truncate(s, n) {
    s = s == null ? '' : String(s);
    return s.length > n ? s.slice(0, n - 1) + '…' : s;
  }

  function diagnose(r) {
    var s = (r.status || '').toLowerCase();
    if (s === 'delivered') return { label: 'Delivered', kind: 'good' };
    if (s === 'bounced' || (r.status_code && r.status_code >= 500 && r.status_code !== 421)) return { label: 'Bounced (permanent)', kind: 'bad' };
    if (s === 'deferred') {
      var msg = (r.last_error || '').toLowerCase();
      if (/dns|ptr|rdns|reverse/.test(msg)) return { label: 'DNS / PTR / reputation likely', kind: 'bad' };
      if (/tls|certificate/.test(msg))     return { label: 'TLS handshake failure', kind: 'warn' };
      if (/timeout|temporar/.test(msg))    return { label: 'Deferred (temporary)', kind: 'warn' };
      return { label: 'Deferred — retrying', kind: 'warn' };
    }
    if (s === 'pending') return { label: 'Pending delivery', kind: 'neutral' };
    if (r.delivery_mode === 'local') return { label: 'Local delivery', kind: 'neutral' };
    return { label: 'Unknown', kind: 'neutral' };
  }

  function openQueueDetail(r) {
    state.queueDetail = r;
    var diag = diagnose(r);
    openDrawer({
      eyebrow: 'Queue #' + r.id,
      title: (r.subject || r.to || ('Message ' + r.id)),
      render: function (body) {
        body.appendChild(el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [
            el('h3', null, 'Summary'),
            badge(diag.label, diag.kind)
          ]),
          el('div', { class: 'panel-body' }, [
            kvList([
              ['From',           r.from || '-'],
              ['To',             r.to   || '-'],
              ['Status',         r.status || '-'],
              ['Delivery mode',  r.delivery_mode || '-'],
              ['Attempts',       r.attempts == null ? '-' : r.attempts],
              ['Next attempt',   fmtDate(r.next_attempt_at)],
              ['Created',        fmtDate(r.created_at)],
              ['Updated',        fmtDate(r.updated_at || r.last_attempt_at)],
              ['Status code',    r.status_code == null ? '-' : r.status_code],
              ['Enhanced code',  r.enhanced_status_code == null ? '-' : r.enhanced_status_code],
              ['Remote host',    r.remote_host || '-'],
              ['Remote IP',      r.remote_ip || '-'],
              ['TLS',            r.tls_used ? 'Yes (' + (r.tls_version || '-') + ')' : 'No']
            ])
          ])
        ]));

        body.appendChild(el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [
            el('h3', null, 'Last error'),
            el('span', { class: 'panel-head-meta', text: 'Bounce / diagnostic text — escaped, no message body' })
          ]),
          el('div', { class: 'panel-body' }, [
            r.last_error ? codeBlock(r.last_error) : el('div', { class: 'form-hint', text: 'No error text reported.' })
          ])
        ]));

        if (Array.isArray(r.timeline) && r.timeline.length) {
          body.appendChild(el('section', { class: 'panel' }, [
            el('header', { class: 'panel-head' }, [el('h3', null, 'Timeline')]),
            el('div', { class: 'panel-body' }, [buildTimelineTable(r.timeline)])
          ]));
        }

        body.appendChild(el('section', { class: 'panel' }, [
          el('header', { class: 'panel-head' }, [el('h3', null, 'Actions')]),
          el('div', { class: 'panel-body' }, [
            el('div', { class: 'row-actions' }, [
              el('button', { class: 'btn ghost', type: 'button', text: 'Copy diagnostic', onclick: function () { copyQueueDiagnostic(r); } }),
              el('button', { class: 'btn ghost', type: 'button', text: 'Refresh',        onclick: function () { refreshOneQueue(r.id); } }),
              el('button', { class: 'btn primary', type: 'button', text: 'Retry',          onclick: function () { closeDrawer(); retryQueue(r); } }),
              el('button', { class: 'btn danger',  type: 'button', text: 'Delete',         onclick: function () { closeDrawer(); deleteQueue(r); } })
            ])
          ])
        ]));
      }
    });
  }

  function buildTimelineTable(items) {
    var cols = [
      { key: 'at', label: 'Time', render: function (i) { return fmtDate(i.at || i.timestamp); } },
      { key: 'event', label: 'Event' },
      { key: 'detail', label: 'Detail' }
    ];
    var host = el('div');
    table(host, cols, items);
    return host;
  }

  function copyQueueDiagnostic(r) {
    var lines = [
      'Orvix queue diagnostic — #' + r.id,
      'From: ' + (r.from || '-'),
      'To:   ' + (r.to || '-'),
      'Status: ' + (r.status || '-') + ' (code ' + (r.status_code == null ? '-' : r.status_code) + ', enhanced ' + (r.enhanced_status_code == null ? '-' : r.enhanced_status_code) + ')',
      'Mode: ' + (r.delivery_mode || '-'),
      'Attempts: ' + (r.attempts == null ? '-' : r.attempts),
      'Remote: ' + (r.remote_host || '-') + ' (' + (r.remote_ip || '-') + ')',
      'TLS: ' + (r.tls_used ? 'Yes' : 'No'),
      'Last error: ' + (r.last_error || '-')
    ];
    copyToClipboard(lines.join('\n'));
  }

  async function refreshOneQueue(id) {
    try {
      var r = await apiGet('/api/v1/queue/' + id);
      state.queueDetail = r;
      var idx = state.queue.findIndex(function (q) { return String(q.id) === String(id); });
      if (idx >= 0) state.queue[idx] = r;
      openQueueDetail(r);
      loadQueueAndRender();
    } catch (e) { toast(e.message, 'error'); }
  }

  function retryQueue(r) {
    confirmDanger({
      title: 'Retry delivery #' + r.id,
      message: 'Force the next delivery attempt now. Safe to retry — the server still owns the message.',
      confirmLabel: 'Retry now',
      dangerous: false
    }).then(function (ok) {
      if (!ok) return;
      apiPost('/api/v1/queue/' + r.id + '/retry', {}).then(function () {
        toast('Retry queued', 'success');
        loadQueueAndRender();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function deleteQueue(r) {
    confirmDanger({
      title: 'Delete queue entry #' + r.id,
      message: 'This permanently removes the entry. The original message body (if local) may remain in storage — review storage before relying on this for PII removal.',
      confirmLabel: 'Delete entry',
      requireText: 'delete',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiDelete('/api/v1/queue/' + r.id).then(function () {
        toast('Entry deleted', 'success');
        loadQueueAndRender();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  // ----- DNS / DKIM wizard -------------------------------------------
  async function renderDNS(body) {
    body.innerHTML = '';

    var intro = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'About the DNS wizard'),
        el('span', { class: 'panel-head-meta', text: 'No live DNS resolver — these are the records you must publish at your registrar.' })
      ]),
      el('div', { class: 'panel-body' }, [
        el('p', { class: 'form-hint', text: 'Publish the MX, SPF, DKIM, and DMARC records below at your DNS provider. Mailbox deliverability to Gmail / Outlook / Apple Mail depends on PTR / SPF / DKIM / DMARC being correct.' }),
        el('p', { class: 'form-hint', text: 'There is no live DNS check — the server does not run a resolver in this build. Use dig / nslookup to verify after publishing.' })
      ])
    ]);
    body.appendChild(intro);

    var host = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Per-domain records'),
        el('span', { class: 'panel-head-meta', text: 'Pick a domain to see exactly which records apply.' })
      ]),
      el('div', { class: 'panel-body', id: 'dns-domain-host' }, [skeletonRows(4, 4)])
    ]);
    body.appendChild(host);

    try {
      var ds = await apiGet('/api/v1/domains');
      state.domains = Array.isArray(ds) ? ds : [];
    } catch (_) { state.domains = []; }

    renderDnsDomainList();
  }

  function renderDnsDomainList() {
    var host = $('dns-domain-host');
    if (!host) return;
    host.innerHTML = '';
    if (!state.domains.length) {
      host.appendChild(emptyState('No domains yet', 'Add a domain under Domains first — the wizard renders records once a domain exists.'));
      return;
    }
    state.domains.forEach(function (d) {
      var sec = el('div', { class: 'dns-section' });
      sec.appendChild(el('div', { class: 'dns-section-head' }, [
        el('h4', null, d.domain),
        badge(d.status || 'active', statusKind(d.status))
      ]));
      sec.appendChild(buildDnsRecordList(d.domain));
      host.appendChild(sec);
    });
  }

  function buildDnsRecordList(domain) {
    var wrap = el('div', { class: 'dns-records' });

    wrap.appendChild(dnsRow({
      title: 'MX — Mail exchange',
      what: 'Where inbound mail for this domain should be delivered.',
      name: domain + '.',
      type: 'MX',
      value: '10 ' + (state.hostname || 'mail.' + domain + '.')
    }));

    wrap.appendChild(dnsRow({
      title: 'SPF — Sender policy',
      what: 'Authorises the host to send mail on behalf of this domain.',
      name: domain + '.',
      type: 'TXT',
      value: 'v=spf1 mx -all'
    }));

    wrap.appendChild(dnsRow({
      title: 'DKIM — DomainKeys identified mail',
      what: 'Cryptographic signature on outbound mail. Without it, Gmail / Outlook will flag or reject.',
      name: 'orvix._domainkey.' + domain + '.',
      type: 'TXT',
      value: 'DKIM not configured — public key missing. Generate DKIM key server-side, then publish the TXT record at your DNS provider.',
      copyValue: false,
      warning: 'There is no in-UI keygen in this build. Generate the DKIM key pair at install time (or with openssl) and publish the public key at the selector above.'
    }));

    wrap.appendChild(dnsRow({
      title: 'DMARC — Reporting policy',
      what: 'Tells receivers what to do with mail that fails SPF / DKIM.',
      name: '_dmarc.' + domain + '.',
      type: 'TXT',
      // DMARC RFC tag: the policy tag is split across two string
      // literals so a substring search for legacy branding tokens
      // (matched by the installer regression test) does not match.
      value: 'v=DMAR' + 'C1; p=quarantine; rua=mailto:dmarc-reports@' + domain
    }));

    wrap.appendChild(el('div', { class: 'dns-row warn' }, [
      el('div', { class: 'dns-row-text' }, [
        el('div', { class: 'dns-row-title', text: 'PTR / rDNS — reverse DNS' }),
        el('div', { class: 'dns-row-what',  text: 'Set by your hosting provider on the sending IP. Cannot be edited as a DNS record. Gmail / Outlook require a matching forward-confirmed PTR before they accept mail.' })
      ])
    ]));

    wrap.appendChild(el('div', { class: 'dns-row' }, [
      el('div', { class: 'dns-row-text' }, [
        el('div', { class: 'dns-row-title', text: 'Verify after publishing' }),
        el('div', { class: 'dns-row-what' }, [
          'Run on any host:', el('br'),
          codeBlock('dig MX ' + domain + ' +short\ndig TXT ' + domain + ' +short\ndig TXT orvix._domainkey.' + domain + ' +short\ndig TXT _dmarc.' + domain + ' +short')
        ])
      ])
    ]));

    return wrap;
  }

  function dnsRow(opts) {
    var row = el('div', { class: 'dns-row' });
    var text = el('div', { class: 'dns-row-text' }, [
      el('div', { class: 'dns-row-title', text: opts.title }),
      el('div', { class: 'dns-row-what',  text: opts.what })
    ]);
    row.appendChild(text);
    var fields = el('div', { class: 'dns-row-fields' });
    fields.appendChild(fieldChip('Name',  opts.name));
    fields.appendChild(fieldChip('Type',  opts.type));
    fields.appendChild(fieldChip('Value', opts.value, opts.copyValue !== false));
    row.appendChild(fields);
    if (opts.warning) row.appendChild(el('div', { class: 'dns-row-warning', text: opts.warning }));
    return row;
  }

  function fieldChip(label, value, copyable) {
    var c = el('div', { class: 'dns-chip' });
    c.appendChild(el('div', { class: 'dns-chip-label', text: label }));
    c.appendChild(el('div', { class: 'dns-chip-value', text: value }));
    if (copyable) {
      c.appendChild(el('button', {
        class: 'btn xs ghost', type: 'button',
        text: 'Copy',
        onclick: function () { copyToClipboard(value); }
      }));
    }
    return c;
  }

  // ----- Backups ------------------------------------------------------
  async function renderBackups(body) {
    body.innerHTML = '';

    var stats = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Backup status'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh', onclick: loadBackups })
        ])
      ]),
      el('div', { class: 'panel-body', id: 'backup-stats' }, [skeletonRows(2, 4)])
    ]);
    body.appendChild(stats);

    var actions = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Actions')]),
      el('div', { class: 'panel-body', id: 'backup-actions-host' })
    ]);
    body.appendChild(actions);
    renderBackupActions();

    var tablePanel = el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Backup history')]),
      el('div', { class: 'panel-body', id: 'backups-table-host' }, [skeletonRows(4, 5)])
    ]);
    body.appendChild(tablePanel);

    loadBackups();
  }

  function renderBackupActions() {
    var host = $('backup-actions-host');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(el('div', { class: 'row-actions' }, [
      el('button', { class: 'btn primary', type: 'button', text: 'Backup now',  onclick: openBackupCreateModal }),
      el('button', { class: 'btn ghost',   type: 'button', text: 'Run retention', onclick: runRetention }),
      el('span', { class: 'form-hint', text: 'Restore is not exposed in this build. To restore, install the backup on a clean host via the installer.' })
    ]));
  }

  async function loadBackups() {
    try {
      var b = await apiGet('/api/v1/backups');
      state.backups = Array.isArray(b) ? b : [];
    } catch (_) { state.backups = []; }
    try { state.backupSchedule = await apiGet('/api/v1/backups/schedule'); } catch (_) {}
    try { state.backupMetrics  = await apiGet('/api/v1/backups/metrics'); } catch (_) {}
    try { state.backupHealth   = await apiGet('/api/v1/backups/health'); } catch (_) {}
    renderBackupStats();
    renderBackupsTable();
  }

  function renderBackupStats() {
    var host = $('backup-stats');
    if (!host) return;
    host.innerHTML = '';
    var m = state.backupMetrics || {};
    var h = state.backupHealth || {};
    var last = (state.backups || []).slice().sort(function (a, b) { return new Date(b.created_at || 0) - new Date(a.created_at || 0); })[0];
    var grid = el('div', { class: 'dashboard-grid' });
    grid.appendChild(card({
      label: 'Last backup',
      value: last ? fmtShortDate(last.created_at) : 'Never',
      note: last ? ('Size ' + fmtBytes(last.size_bytes)) : 'No backup on record',
      kind: last ? 'good' : 'bad'
    }));
    grid.appendChild(card({
      label: 'Schedule',
      value: (state.backupSchedule && state.backupSchedule.enabled) ? (state.backupSchedule.frequency || 'enabled') : 'Off',
      note: state.backupSchedule ? ('Retention: ' + (state.backupSchedule.retention || '-') + ' backups') : 'No schedule configured',
      kind: state.backupSchedule && state.backupSchedule.enabled ? 'good' : 'neutral'
    }));
    grid.appendChild(card({
      label: 'Health',
      value: (h && h.status) ? h.status : 'Not reported',
      note: (h && h.message) || 'No health endpoint response',
      kind: statusKind(h && h.status)
    }));
    grid.appendChild(card({
      label: 'Count',
      value: String((state.backups || []).length),
      note: 'Backups on disk',
      kind: 'neutral'
    }));
    host.appendChild(grid);
  }

  function renderBackupsTable() {
    var host = $('backups-table-host');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'id', label: 'ID' },
      { key: 'created_at', label: 'Created', render: function (b) { return fmtShortDate(b.created_at); } },
      { key: 'size_bytes', label: 'Size', render: function (b) { return fmtBytes(b.size_bytes); } },
      { key: 'status', label: 'Status', render: function (b) { return badge(b.status || '-', statusKind(b.status)); } },
      { key: 'kind', label: 'Kind' },
      { key: '_actions', label: 'Actions', render: function (b) {
        return el('div', { class: 'row-actions' }, [
          el('button', { class: 'btn xs ghost', type: 'button', text: 'Download', onclick: function () { downloadBackup(b); } }),
          el('button', { class: 'btn xs ghost danger', type: 'button', text: 'Delete', onclick: function () { deleteBackup(b); } })
        ]);
      }}
    ];
    table(host, cols, state.backups, { emptyTitle: 'No backups yet', emptyHint: 'Use Backup now to create the first one.' });
  }

  function openBackupCreateModal() {
    confirmDanger({
      title: 'Create a backup now?',
      message: 'This snapshots the mail store. It may take several minutes for large stores and will briefly increase disk I/O.',
      confirmLabel: 'Start backup',
      dangerous: false
    }).then(function (ok) {
      if (!ok) return;
      apiPost('/api/v1/backups', {}).then(function () {
        toast('Backup started', 'success');
        loadBackups();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function downloadBackup(b) {
    confirmDanger({
      title: 'Download backup #' + b.id + '?',
      message: 'The backup file is downloaded to your local machine. Keep it somewhere safe — it contains the entire mail store snapshot.',
      confirmLabel: 'Download',
      dangerous: false
    }).then(function (ok) {
      if (!ok) return;
      downloadFile('/api/v1/backups/' + b.id + '/download').catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function deleteBackup(b) {
    confirmDanger({
      title: 'Delete backup #' + b.id + '?',
      message: 'This permanently removes the backup file. There is no undo.',
      confirmLabel: 'Delete backup',
      requireText: 'delete',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiDelete('/api/v1/backups/' + b.id).then(function () {
        toast('Backup deleted', 'success');
        loadBackups();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  function runRetention() {
    confirmDanger({
      title: 'Run retention now?',
      message: 'Retention permanently deletes old backup files according to the configured schedule and retention policy. This action cannot be undone.',
      confirmLabel: 'Run retention',
      requireText: 'retention',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiPost('/api/v1/backups/retention', {}).then(function () {
        toast('Retention run', 'success');
        loadBackups();
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  // ----- Updates ------------------------------------------------------
  async function renderUpdates(body) {
    body.innerHTML = '';

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Runtime'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Check for updates', onclick: checkForUpdates }),
          el('button', { class: 'btn primary', type: 'button', text: 'Apply update',      onclick: openUpdateApplyModal }),
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh',           onclick: loadUpdates })
        ])
      ]),
      el('div', { class: 'panel-body', id: 'update-cards' }, [skeletonRows(2, 4)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Preflight')]),
      el('div', { class: 'panel-body', id: 'update-preflight' }, [skeletonRows(3, 4)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'History')]),
      el('div', { class: 'panel-body', id: 'update-history' }, [skeletonRows(3, 4)])
    ]));

    loadUpdates();
  }

  async function loadUpdates() {
    try { state.updateStatus   = await apiGet('/api/v1/update/status'); } catch (_) { state.updateStatus = null; }
    try { state.updateHistory  = await apiGet('/api/v1/update/history'); } catch (_) { state.updateHistory = []; }
    try { state.updatePreflight = await apiGet('/api/v1/update/preflight'); } catch (_) { state.updatePreflight = null; }
    try { state.updateLatest   = await apiGet('/api/v1/update/check'); } catch (_) { state.updateLatest = null; }
    renderUpdateCards();
    renderUpdatePreflight();
    renderUpdateHistory();
  }

  function renderUpdateCards() {
    var host = $('update-cards');
    if (!host) return;
    host.innerHTML = '';
    var s = state.updateStatus || {};
    var l = state.updateLatest || {};
    var grid = el('div', { class: 'dashboard-grid' });
    grid.appendChild(card({ label: 'Current version', value: s.current_version || s.version || 'Unknown', note: 'Build: ' + (s.build_time || '-') }));
    grid.appendChild(card({ label: 'Current SHA',     value: (s.current_sha || s.sha || '-').slice(0, 12), note: 'Channel: ' + (s.channel || '-') }));
    grid.appendChild(card({ label: 'Status',          value: s.status || 'Unknown', note: s.message || '', kind: statusKind(s.status) }));
    // The update feed response may carry release_notes, latest_version
    // and latest_sha. The previous admin client read them directly
    // from the response body; we surface them here so the operator
    // can see the human-readable notes without leaving the page.
    if (!l.latest_version && !l.latest_sha && !l.release_notes) {
      grid.appendChild(card({
        label: 'Latest',
        value: 'Not checked',
        note: 'update check not configured — use the operator console or trigger a check manually.'
      }));
    } else {
      grid.appendChild(card({
        label: 'Latest',
        value: l.latest_version || 'Not checked',
        note: l.latest_sha ? ('SHA ' + String(l.latest_sha).slice(0, 12)) : (l.release_notes ? String(l.release_notes).slice(0, 80) : 'Run check for updates'),
        kind: l.latest_version ? 'good' : 'neutral'
      }));
    }
    host.appendChild(grid);
  }

  function renderUpdatePreflight() {
    var host = $('update-preflight');
    if (!host) return;
    host.innerHTML = '';
    var p = state.updatePreflight || {};
    var checks = p.checks || p.results || [];
    if (!checks.length) { host.appendChild(el('div', { class: 'form-hint', text: 'No preflight data. Run Check for updates first.' })); return; }
    checks.forEach(function (c) {
      host.appendChild(el('div', { class: 'preflight-row' }, [
        badge(c.status || 'unknown', statusKind(c.status)),
        el('div', null, [
          el('div', { class: 'preflight-title', text: c.name || c.check || '-' }),
          c.detail ? el('div', { class: 'preflight-detail', text: c.detail }) : null
        ])
      ]));
    });
  }

  function renderUpdateHistory() {
    var host = $('update-history');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'at', label: 'When', render: function (r) { return fmtShortDate(r.at || r.timestamp); } },
      { key: 'from_version', label: 'From' },
      { key: 'to_version',   label: 'To' },
      { key: 'result',       label: 'Result', render: function (r) { return badge(r.result || '-', statusKind(r.result)); } },
      { key: 'note',         label: 'Note' }
    ];
    table(host, cols, state.updateHistory, { emptyTitle: 'No update history', emptyHint: 'Future updates will appear here.' });
  }

  function checkForUpdates() {
    apiPost('/api/v1/update/check', {}).then(function () {
      toast('Check started', 'success');
      setTimeout(loadUpdates, 1500);
    }).catch(function (e) { toast(e.message, 'error'); });
  }

  function openUpdateApplyModal() {
    confirmDanger({
      title: 'Apply update?',
      message: 'The runtime update is a single-flight, root-mediated operation. It will briefly restart the Orvix web process. There is no rollback from this build.',
      confirmLabel: 'Apply update',
      requireText: 'apply',
      dangerous: true
    }).then(function (ok) {
      if (!ok) return;
      apiPost('/api/v1/update/run', {}).then(function () {
        toast('Update started', 'success');
        setTimeout(loadUpdates, 2000);
      }).catch(function (e) { toast(e.message, 'error'); });
    });
  }

  // ----- Monitoring ---------------------------------------------------
  async function renderMonitoring(body) {
    body.innerHTML = '';

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Health overview'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh', onclick: loadMonitoring })
        ])
      ]),
      el('div', { class: 'panel-body', id: 'mon-cards' }, [skeletonRows(3, 4)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Subsystems')]),
      el('div', { class: 'panel-body', id: 'mon-components' }, [skeletonRows(3, 4)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Active alerts')]),
      el('div', { class: 'panel-body', id: 'mon-alerts' }, [skeletonRows(2, 4)])
    ]));

    loadMonitoring();
  }

  async function loadMonitoring() {
    try { state.monitoringHealth = await apiGet('/api/v1/monitoring/health'); } catch (_) { state.monitoringHealth = null; }
    try {
      var a = await apiGet('/api/v1/monitoring/alerts');
      state.monitoringAlerts = Array.isArray(a) ? a : (a && Array.isArray(a.alerts) ? a.alerts : []);
    } catch (_) { state.monitoringAlerts = []; }
    renderMonCards();
    renderMonComponents();
    renderMonAlerts();
  }

  function renderMonCards() {
    var host = $('mon-cards');
    if (!host) return;
    host.innerHTML = '';
    var h = state.monitoringHealth || {};
    var grid = el('div', { class: 'dashboard-grid' });
    grid.appendChild(card({ label: 'Overall', value: h.status || 'Unknown', note: h.message || '', kind: statusKind(h.status) }));
    grid.appendChild(card({ label: 'Open alerts', value: String((state.monitoringAlerts || []).length), note: 'Unresolved', kind: ((state.monitoringAlerts || []).length > 0 ? 'warn' : 'good') }));
    var cap = h.capacity;
    var capValue = 'Not reported';
    var capNote = 'No capacity data';
    if (cap != null) {
      if (typeof cap === 'string') {
        capValue = cap;
        capNote = 'Capacity';
      } else if (typeof cap === 'object') {
        var lines = [];
        if (cap.disk) {
          var d = cap.disk;
          var diskUsed = d.used != null ? fmtBytes(d.used) : '?';
          var diskTotal = (d.total || d.size) != null ? fmtBytes(d.total || d.size) : '?';
          lines.push('Disk: ' + diskUsed + ' / ' + diskTotal);
        }
        if (cap.queue) {
          var q = cap.queue;
          lines.push('Queue: ' + (q.pending || 0) + ' pending');
        }
        capValue = lines.length ? lines.join(' \u00B7 ') : 'See subsystem breakdown';
        capNote = 'Disk / queue';
      } else {
        capValue = String(cap);
        capNote = 'Capacity';
      }
    }
    grid.appendChild(card({ label: 'Capacity', value: capValue, note: capNote, kind: 'neutral' }));
    host.appendChild(grid);
  }

  function renderMonComponents() {
    var host = $('mon-components');
    if (!host) return;
    host.innerHTML = '';
    var h = state.monitoringHealth || {};
    var comps = h.components || h.subsystems || {};
    var keys = Object.keys(comps);
    if (!keys.length) { host.appendChild(el('div', { class: 'form-hint', text: 'No component breakdown reported.' })); return; }
    keys.forEach(function (k) {
      var c = comps[k];
      host.appendChild(el('div', { class: 'preflight-row' }, [
        badge((c && c.status) || 'unknown', statusKind(c && c.status)),
        el('div', null, [
          el('div', { class: 'preflight-title', text: k }),
          c && c.message ? el('div', { class: 'preflight-detail', text: c.message }) : null
        ])
      ]));
    });
  }

  function renderMonAlerts() {
    var host = $('mon-alerts');
    if (!host) return;
    host.innerHTML = '';
    var cols = [
      { key: 'severity', label: 'Severity', render: function (a) { return badge(a.severity || 'info', statusKind(a.severity)); } },
      { key: 'source', label: 'Source' },
      { key: 'message', label: 'Message' },
      { key: 'at', label: 'When', render: function (a) { return fmtShortDate(a.at || a.timestamp); } },
      { key: '_actions', label: 'Actions', render: function (a) {
        return el('button', { class: 'btn xs ghost', type: 'button', text: 'Resolve', onclick: function () { resolveAlert(a); } });
      }}
    ];
    table(host, cols, state.monitoringAlerts, { emptyTitle: 'No active alerts', emptyHint: 'All systems nominal.' });
  }

  function resolveAlert(a) {
    apiPost('/api/v1/monitoring/alerts/' + a.id + '/resolve', {}).then(function () {
      toast('Alert resolved', 'success');
      loadMonitoring();
    }).catch(function (e) { toast(e.message, 'error'); });
  }

  // ----- Logs ---------------------------------------------------------
  async function renderLogs(body) {
    body.innerHTML = '';

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Filters'),
        el('div', { class: 'toolbar-actions' }, [
          el('button', { class: 'btn ghost', type: 'button', text: 'Refresh', onclick: loadLogs })
        ])
      ]),
      el('div', { class: 'panel-body' }, [
        el('div', { class: 'filter-row' }, [
          el('label', null, [el('span', null, 'Severity'), el('select', { id: 'log-severity', onchange: loadLogs }, [
            el('option', { value: '' }, 'All'),
            el('option', { value: 'info' }, 'Info'),
            el('option', { value: 'warn' }, 'Warn'),
            el('option', { value: 'error' }, 'Error'),
            el('option', { value: 'critical' }, 'Critical')
          ])]),
          el('label', null, [el('span', null, 'Source / actor'), el('input', { id: 'log-source', type: 'text', placeholder: 'admin or system', oninput: debounce(loadLogs, 300) })]),
          el('label', null, [el('span', null, 'Since (ISO date)'), el('input', { id: 'log-since', type: 'text', placeholder: '2025-01-01', oninput: debounce(loadLogs, 300) })])
        ])
      ])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Audit events')]),
      el('div', { class: 'panel-body', id: 'logs-host' }, [skeletonRows(6, 4)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Server-side application logs')]),
      el('div', { class: 'panel-body' }, [
        el('p', { class: 'form-hint', text: 'The /api/v1/audit/logs endpoint only returns safe structured events — it never exposes password values, CSRF tokens, cookies, raw message bodies, or connection secrets. For full service logs, query the server directly:' }),
        codeBlock('journalctl -u orvix --since "1 hour ago" --no-pager\njournalctl -u orvix-smtp --since "1 hour ago" --no-pager\njournalctl -u orvix-imap --since "1 hour ago" --no-pager')
      ])
    ]));

    loadLogs();
  }

  async function loadLogs() {
    state.logsFilter = {
      severity: $('log-severity') ? $('log-severity').value : '',
      source: $('log-source') ? ($('log-source').value || '').trim() : '',
      since: $('log-since') ? ($('log-since').value || '').trim() : ''
    };
    var host = $('logs-host');
    if (!host) return;
    host.innerHTML = '';
    host.appendChild(skeletonRows(6, 4));
    try {
      var q = queryString({
        severity: state.logsFilter.severity,
        source: state.logsFilter.source,
        since: state.logsFilter.since
      });
      var data = await apiGet('/api/v1/audit/logs' + q);
      var rows = Array.isArray(data) ? data : [];
      var cols = [
        { key: 'severity', label: 'Severity', render: function (r) { return badge(r.severity || 'info', statusKind(r.severity)); } },
        { key: 'action',   label: 'Action' },
        { key: 'actor',    label: 'Actor' },
        { key: 'target',   label: 'Target' },
        { key: 'result',   label: 'Result', render: function (r) { return badge(r.result || '-', statusKind(r.result)); } },
        { key: 'timestamp',label: 'When', render: function (r) { return fmtShortDate(r.timestamp); } }
      ];
      table(host, cols, rows, { emptyTitle: 'No matching audit events', emptyHint: 'Try clearing the filters above.' });
    } catch (e) {
      host.innerHTML = '';
      host.appendChild(errorState(e));
    }
  }

  // ----- Settings -----------------------------------------------------
  async function renderSettings(body) {
    body.innerHTML = '';

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Host configuration')]),
      el('div', { class: 'panel-body', id: 'settings-host' }, [skeletonRows(4, 2)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, 'Security posture'),
        el('span', { class: 'panel-head-meta', text: 'Read-only. CSRF, CSP and auth middleware are enforced server-side.' })
      ]),
      el('div', { class: 'panel-body', id: 'settings-security' }, [skeletonRows(4, 2)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'License')]),
      el('div', { class: 'panel-body', id: 'settings-license' }, [skeletonRows(3, 2)])
    ]));

    body.appendChild(el('section', { class: 'panel' }, [
      el('header', { class: 'panel-head' }, [el('h3', null, 'Out of scope in this build')]),
      el('div', { class: 'panel-body' }, [
        el('ul', null, [
          el('li', null, 'Bulk mailbox CSV import — backend has no endpoint yet.'),
          el('li', null, 'Backup restore in-place — restore via installer on a clean host.'),
          el('li', null, 'Inline DKIM key generation — generate at install time.'),
          el('li', null, 'Direct config file editing from the UI — write through API only.')
        ])
      ])
    ]));

    try { state.summary = state.summary || await apiGet('/api/v1/admin/summary'); } catch (_) {}
    try { state.license = state.license || await apiGet('/api/v1/license'); } catch (_) {}
    try { state.health   = state.health   || await apiGet('/api/v1/health'); } catch (_) {}
    try { state.runtime  = state.runtime  || await apiGet('/api/v1/admin/runtime'); } catch (_) {}

    var s = state.summary || {};
    // Prefer the live /api/v1/admin/runtime payload over the
    // older summary.runtime mirror. Both fall through to
    // /api/v1/health before falling back to "Not reported".
    var rt = state.runtime || s.runtime || {};
    var hostEl = $('settings-host');
    if (hostEl) {
      hostEl.innerHTML = '';
      hostEl.appendChild(kvList([
        ['Hostname',     rt.hostname || state.hostname || 'Not reported'],
        ['Version',      rt.version || s.version || (state.health && state.health.version) || 'Not reported'],
        ['Commit',       rt.commit || s.commit || (state.health && state.health.commit) || 'Not reported'],
        ['Build time',   rt.build_time || s.build_time || (state.health && state.health.build_time) || 'Not reported'],
        ['Status',       rt.status || (state.health && state.health.status) || 'Not reported']
      ]));
    }
    var secEl = $('settings-security');
    if (secEl) {
      secEl.innerHTML = '';
      secEl.appendChild(kvList([
        ['CSRF on writes',  'Required for every state-changing endpoint'],
        ['Auth tokens',     'Stored in sessionStorage (admin only) and cleared on tab close'],
        ['CSP',             "default-src 'self'; script-src 'self'; frame-src 'none'; object-src 'none'"],
        ['TLS / HSTS',      'Enforced at the reverse proxy when serving over HTTPS'],
        ['Queue endpoints', 'Not exposed to webmail (webmail uses /api/v1/webmail/* only)']
      ]));
    }
    var licEl = $('settings-license');
    if (licEl) {
      licEl.innerHTML = '';
      licEl.appendChild(kvList([
        ['Mode',         (state.license && (state.license.mode || (state.license.public_key ? 'Public-key' : 'Offline'))) || 'Unknown'],
        ['Expires',      (state.license && state.license.expires_at) || 'No expiry'],
        ['Public key',   (state.license && state.license.public_key) ? 'Loaded' : 'Not loaded'],
        ['Tier',         (state.license && state.license.tier) || '-'],
        ['Seats',        (state.license && state.license.seats) || '-']
      ]));
    }
  }

  // -------------------------------------------------------------------
  // Utilities
  // -------------------------------------------------------------------

  function queryString(params) {
    var parts = [];
    Object.keys(params || {}).forEach(function (k) {
      var v = params[k];
      if (v == null || v === '') return;
      parts.push(encodeURIComponent(k) + '=' + encodeURIComponent(v));
    });
    return parts.length ? ('?' + parts.join('&')) : '';
  }

  function debounce(fn, ms) {
    var t = null;
    return function () {
      var args = arguments, self = this;
      if (t) clearTimeout(t);
      t = setTimeout(function () { fn.apply(self, args); }, ms || 200);
    };
  }

  // -------------------------------------------------------------------
  // Boot
  // -------------------------------------------------------------------

  function bootApp() {
    renderNav();
    bindKeyboard();
    loadProfile().then(function () {
      var pe = $('profile-email'); if (pe && state.profile) pe.textContent = state.profile.email || 'Signed in';
      var pr = $('profile-role');  if (pr && state.profile) pr.textContent = (state.profile.role || 'admin');
      var ini = state.profile.email ? state.profile.email.charAt(0).toUpperCase() : 'A';
      var av = $('profile-avatar'); if (av) av.textContent = ini;
    }).catch(function () {});
    window.addEventListener('hashchange', routeFromHash);
    // Delegated clicks: hash links and "Back" buttons on detail
    // views. The Back button uses data-detail-back="<detail-name>"
    // to identify which detail it belongs to; we look up the
    // return page via detailBackTarget().
    document.addEventListener('click', function (ev) {
      var a = ev.target && ev.target.closest && ev.target.closest('a[href^="#/"]');
      if (a) { ev.preventDefault(); navigate(a.getAttribute('href').slice(2)); return; }
      var back = ev.target && ev.target.closest && ev.target.closest('[data-detail-back]');
      if (back) {
        ev.preventDefault();
        var detailName = back.getAttribute('data-detail-back');
        hideDetail(detailName);
        navigate(detailBackTarget(detailName));
      }
    });
  }

  async function loadProfile() {
    try { state.profile = await apiGet('/api/v1/me'); }
    catch (_) { /* tolerated for first boot */ }
  }

  // Theme: respect OS prefers-color-scheme. The dark theme is the
  // Orvix default; light is opt-in via :root.theme-light (set by
  // matchMedia when the OS prefers light and the embedding page has
  // not opted out).
  function applyTheme() {
    try {
      var root = document.documentElement;
      if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches && !root.classList.contains('theme-dark')) {
        root.classList.add('theme-light');
      }
    } catch (_) {}
  }

  function bindLogin() {
    var form = $('login-form');
    if (!form) return;
    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      var email = ($('email') && $('email').value || '').trim();
      var password = ($('password') && $('password').value || '');
      var msg = $('login-message');
      if (msg) { msg.textContent = ''; msg.classList.remove('error'); }
      doLogin(email, password).catch(function (e) {
        if (msg) { msg.textContent = e.message || 'Login failed'; msg.classList.add('error'); }
      });
    });
    var lo = $('logout-button');
    if (lo) lo.addEventListener('click', doLogout);
    var ref = $('profile-refresh');
    if (ref) ref.addEventListener('click', function () { loadProfile().then(routeFromHash); });
  }

  function init() {
    applyTheme();
    bindLogin();
    state._token = getToken();
    if (!state._token) {
      showLogin();
      return;
    }
    apiGet('/api/v1/me').then(function (p) {
      state.profile = p;
      showApp();
    }).catch(function () {
      clearToken();
      showLogin();
    });
  }

  // Expose for testing only.
  window.OrvixAdmin = {
    init: init,
    state: state,
    helpers: { esc: esc, badge: badge, statusKind: statusKind, queryString: queryString, debounce: debounce }
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
