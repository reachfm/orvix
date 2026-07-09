/* =====================================================================
   modules/components.js — DOM helpers + UI primitives.

   Every page module imports el / esc / badge / table / openModal /
   openDrawer / confirmDanger / copyToClipboard / fmtXxx / t here.

   This file keeps the markup vocabulary uniform across the admin
   app. Pages should not call document.createElement directly
   unless they have a special reason — using el() avoids XSS by
   routing every attribute through setAttribute (which escapes
   by default) and every text node through textContent.

   The static-analysis tests assert the literal string
   `function closeModal` / `function toast` / `function confirmDanger`
   appears in the admin bundle. Those names are kept below as
   public exports so the legacy contract is honored across the
   modular refactor.
   ===================================================================== */

import { t } from './i18n.js';

// ---------- tiny DOM helpers ------------------------------------
export function $(id) { return document.getElementById(id); }

export function el(tag, attrs, children) {
  const node = document.createElement(tag);
  if (attrs) {
    Object.keys(attrs).forEach((k) => {
      const v = attrs[k];
      if (v == null) return;
      if (k === 'class')        node.className = v;
      else if (k === 'text')    node.textContent = v;
      else if (k === 'html')    node.innerHTML = v; // ONLY for trusted static strings
      else if (k === 'style' && typeof v === 'object') Object.assign(node.style, v);
      else if (k.indexOf('on') === 0 && typeof v === 'function') {
        node.addEventListener(k.slice(2).toLowerCase(), v);
      } else {
        node.setAttribute(k, v);
      }
    });
  }
  if (children != null) {
    if (!Array.isArray(children)) children = [children];
    children.forEach((c) => {
      if (c == null) return;
      if (typeof c === 'string' || typeof c === 'number') {
        node.appendChild(document.createTextNode(String(c)));
      } else if (c instanceof Node) {
        node.appendChild(c);
      }
    });
  }
  return node;
}

// Escape arbitrary value for safe innerHTML interpolation. Used by
// every page renderer for any value that may contain <, > or " —
// attributes that go through setAttribute are safe without esc().
export function esc(v) {
  if (v == null) return '';
  return String(v).replace(/[&<>"']/g, (c) => {
    switch (c) {
      case '&': return '&amp;';
      case '<': return '&lt;';
      case '>': return '&gt;';
      case '"': return '&quot;';
      case "'": return '&#39;';
      default:  return c;
    }
  });
}

// ---------- formatters ------------------------------------------
export function fmtDate(s) {
  if (!s) return '-';
  const d = new Date(s);
  if (isNaN(d.getTime())) return esc(s);
  return d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
}
export function fmtShortDate(s) {
  if (!s) return '-';
  const d = new Date(s);
  if (isNaN(d.getTime())) return esc(s);
  return d.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
}
export function fmtBytes(n) {
  if (n == null || isNaN(Number(n))) return '-';
  n = Number(n);
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024)     return (n / 1024).toFixed(1) + ' KB';
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}
export function fmtNumber(n) {
  if (n == null || isNaN(Number(n))) return '-';
  return Number(n).toLocaleString();
}
export function plural(n, one, many) {
  return n === 1 ? one : (many || (one + 's'));
}

// ---------- badge + status pill --------------------------------
// Trusted badge tones. Anything outside this list falls back to
// `neutral` so a stray or attacker-controlled status string cannot
// inject an unknown CSS class. The Stalwart-style vocabulary
// (active / skipped / degraded / not-monitored / offline /
// warning / error) all map to one of these tones via statusKind().
const _knownKinds = new Set([
  'good', 'active',
  'warn', 'warning', 'degraded',
  'bad', 'error', 'critical',
  'neutral', 'offline', 'skipped', 'muted',
  'not-monitored', 'not-configured',
  'info', 'accent', 'tag',
]);
export function badge(label, kind) {
  const k = _knownKinds.has(kind) ? kind : 'neutral';
  return el('span', { class: 'badge ' + k }, label == null ? '' : String(label));
}
export function statusKind(s) {
  if (!s) return 'neutral';
  const v = String(s).toLowerCase();
  // Health: green
  if (v === 'active' || v === 'ok' || v === 'healthy' || v === 'good' ||
      v === 'delivered' || v === 'enabled' || v === 'online' ||
      v === 'resolved' || v === 'success') return 'good';
  // Soft health: yellow (warn / degraded / pending)
  if (v === 'pending' || v === 'starting' || v === 'queued' ||
      v === 'degraded' || v === 'warn' || v === 'warning') return 'warn';
  // Disabled / not-running — MUTED, not red. Skipped listeners and
  // "managed by Caddy" surfaces should not look like outages.
  if (v === 'skipped' || v === 'disabled' || v === 'muted' ||
      v === 'not-monitored' || v === 'not_configured' ||
      v === 'offline' /* offline defaults to muted unless we add a
                          dedicated red set above */) return 'neutral';
  // Hard failure: red
  if (v === 'failed' || v === 'fail' || v === 'error' || v === 'critical') return 'bad';
  return 'neutral';
}

// ---------- uniform table renderer -----------------------------
export function table({ columns, rows, empty = 'No data', caption, loading = false, search = false, filters = null }) {
  const root = el('div', { class: 'table-wrap' });
  if (caption) root.appendChild(el('h4', { class: 'table-caption', text: caption }));

  if (search || filters) {
    const bar = el('div', { class: 'table-bar' });
    if (search) {
      const s = el('input', { type: 'search', placeholder: typeof search === 'string' ? search : 'Search…', autocomplete: 'off', spellcheck: 'false' });
      bar.appendChild(el('label', { class: 'table-search' }, [s]));
      s.addEventListener('input', () => {
        const q = s.value.toLowerCase();
        renderBody();
        body.querySelectorAll('tr').forEach((tr) => {
          const matches = tr.dataset.search && tr.dataset.search.indexOf(q) >= 0;
          tr.style.display = (q && !matches) ? 'none' : '';
        });
      });
    }
    if (filters) bar.appendChild(filters);
    root.appendChild(bar);
  }

  const tbl = el('table', { class: 'data-table' });
  const thead = el('thead');
  const trh = el('tr');
  columns.forEach((c) => trh.appendChild(el('th', { class: c.headClass || '' }, c.label || c.name)));
  thead.appendChild(trh);
  tbl.appendChild(thead);

  const body = el('tbody');
  tbl.appendChild(body);
  root.appendChild(tbl);

  function renderBody() {
    body.innerHTML = '';
    if (loading) {
      body.appendChild(el('tr', null, el('td', { colspan: String(columns.length), class: 'loading-row', text: 'Loading…' })));
      return;
    }
    const list = rows || [];
    if (list.length === 0) {
      body.appendChild(el('tr', null, el('td', { colspan: String(columns.length), class: 'empty-row', text: empty })));
      return;
    }
    list.forEach((row) => {
      const tr = el('tr');
      const searchIndex = columns.map((c) => {
        try { return String(c.searchKey ? c.searchKey(row) : ''); } catch (_) { return ''; }
      }).join(' ').toLowerCase();
      tr.dataset.search = searchIndex;
      columns.forEach((c) => {
        const cell = el('td', { class: c.cellClass || '' });
        let v;
        try { v = c.render(row); } catch (_) { v = '-'; }
        if (v instanceof Node) cell.appendChild(v);
        else if (v == null) cell.textContent = '';
        else cell.textContent = String(v);
        tr.appendChild(cell);
      });
      body.appendChild(tr);
    });
  }
  renderBody();
  return root;
}

// ---------- modal + drawer -------------------------------------
let _activeOverlay = null;
function closeActiveOverlay() {
  const o = _activeOverlay;
  _activeOverlay = null;
  if (!o) return;
  o.classList.remove('open');
  setTimeout(() => { if (o.parentNode) o.parentNode.removeChild(o); }, 180);
}

export function openModal(opts) {
  closeActiveOverlay();
  const overlay = el('div', { class: 'modal-overlay', 'aria-hidden': 'true' });
  const modal = el('div', {
    class: 'modal ' + (opts.size ? ('modal-' + opts.size) : 'modal-md'),
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': opts.title || 'Dialog',
  });
  const head = el('header', { class: 'modal-head' }, [
    el('h3', { class: 'modal-title', text: opts.title || '' }),
    el('button', {
      class: 'icon-btn', type: 'button', 'aria-label': 'Close',
      title: 'Close (Esc)', onclick: closeActiveOverlay,
    }, '\u00d7'),
  ]);
  const body = el('div', { class: 'modal-body' });
  const foot = el('footer', { class: 'modal-foot' });
  modal.appendChild(head);
  modal.appendChild(body);
  modal.appendChild(foot);
  overlay.appendChild(modal);
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay && opts.dismissable !== false) closeActiveOverlay();
  });
  document.body.appendChild(overlay);
  requestAnimationFrame(() => overlay.classList.add('open'));
  if (typeof opts.render === 'function') opts.render(body, foot);
  setTimeout(() => {
    const firstInput = modal.querySelector('input, select, textarea, button');
    if (firstInput) firstInput.focus();
  }, 50);
  _activeOverlay = overlay;
  return { close: closeActiveOverlay };
}

export function openDrawer(opts) {
  closeActiveOverlay();
  const overlay = el('div', { class: 'drawer-overlay', 'aria-hidden': 'true' });
  const drawer = el('aside', {
    class: 'drawer',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': opts.title || 'Detail',
  });
  const head = el('header', { class: 'drawer-head' }, [
    el('div', null, [
      el('div', { class: 'drawer-eyebrow', text: opts.eyebrow || '' }),
      el('h3', { class: 'drawer-title', text: opts.title || 'Detail' }),
    ]),
    el('button', {
      class: 'icon-btn', type: 'button', 'aria-label': 'Close',
      title: 'Close (Esc)', onclick: closeActiveOverlay,
    }, '\u00d7'),
  ]);
  const body = el('div', { class: 'drawer-body' });
  drawer.appendChild(head);
  drawer.appendChild(body);
  overlay.appendChild(drawer);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) closeActiveOverlay(); });
  document.body.appendChild(overlay);
  requestAnimationFrame(() => overlay.classList.add('open'));
  if (typeof opts.render === 'function') opts.render(body);
  setTimeout(() => {
    const btn = drawer.querySelector('button');
    if (btn) btn.focus();
  }, 50);
  _activeOverlay = overlay;
  return { close: closeActiveOverlay };
}

export function closeOverlay() { closeActiveOverlay(); }

// closeModal is an alias for closeOverlay kept for the static-
// analysis contract. The original monolithic admin used a
// private function `closeModal`; the modular refactor uses
// `closeActiveOverlay` (because the same helper backs both
// the modal and the drawer), and exposes `closeOverlay` for
// the public name. We keep the legacy name available so
// dashboards that imported `closeModal` still work.
export function closeModal() { closeActiveOverlay(); }

// modal is an alias for openModal kept for the same reason.
// Thirteen page modules import `modal` directly (matching the
// legacy monolithic naming) and call `modal({ ... })`; the
// static-analysis smoke (scripts/smoke-admin-import-graph.mjs)
// asserts every imported name is exported by its target, and
// the previous code only exposed `openModal`. Without this
// alias the affected pages (acceptance / ssl / ftp-backup /
// fs-access / migration-sources / incoming-rules / log-rules /
// acl / admin-groups / public-folders / mailing-lists /
// domain-groups / account-classes) failed their dynamic
// import the first time the operator opened them, leaving the
// page-root empty even though the route registered.
export const modal = openModal;

// closeDrawer is the public drawer-close alias. The static-
// analysis test (TestAdminHasDrawerModalToastPrimitives) asserts
// the literal "function closeDrawer" appears in the bundle, so
// we keep a thin wrapper that closes the active overlay.
export function closeDrawer() { closeActiveOverlay(); }

// ---------- toast + confirmDanger (public API) ----------------
// toast is the public toast helper. The static-analysis test
// that scans the source for `function toast` keeps matching
// the legacy contract after the modular refactor.
export function toast(message, kind = 'info', ttlMs) {
  const stack = $('toast-stack');
  if (!stack) return;
  const ttl = ttlMs == null ? (kind === 'error' ? 6000 : 3200) : ttlMs;
  const node = document.createElement('div');
  node.className = 'toast toast-' + kind;
  node.setAttribute('role', 'status');
  node.textContent = String(message == null ? '' : message);
  stack.appendChild(node);
  while (stack.childNodes.length > 4) stack.removeChild(stack.firstChild);
  setTimeout(() => { if (node.parentNode) node.parentNode.removeChild(node); }, ttl);
}

// confirmDanger is the public dangerous-action modal helper.
// Exposed as a function declaration (not an arrow) so the
// static-analysis test for `function confirmDanger` continues
// to match the legacy contract.
export function confirmDanger(opts) {
  return new Promise((resolve) => {
    openModal({
      title: opts.title || 'Are you sure?',
      dismissable: true,
      render: (body, foot) => {
        body.appendChild(el('div', { class: 'modal-message', text: opts.message || '' }));
        let input;
        if (opts.requireText) {
          const row = el('div', { class: 'form-row' });
          row.appendChild(el('label', { for: 'confirm-text' }, [
            'Type ', el('strong', { text: opts.requireText }), ' to confirm:',
          ]));
          input = el('input', {
            id: 'confirm-text', type: 'text', autocomplete: 'off',
            spellcheck: 'false', placeholder: opts.requireText,
          });
          row.appendChild(input);
          body.appendChild(row);
        }
        const cancelBtn = el('button', {
          class: 'btn ghost', type: 'button', text: opts.cancelLabel || 'Cancel',
          onclick: () => { closeActiveOverlay(); resolve(false); },
        });
        foot.appendChild(cancelBtn);

        const confirmBtn = el('button', {
          class: (opts.dangerous === false ? 'btn primary' : 'btn danger'),
          type: 'button', text: opts.confirmLabel || 'Confirm',
          disabled: opts.requireText ? 'disabled' : null,
        });
        if (opts.requireText) {
          input.addEventListener('input', () => {
            if (input.value.trim() === opts.requireText) confirmBtn.removeAttribute('disabled');
            else confirmBtn.setAttribute('disabled', 'disabled');
          });
        }
        confirmBtn.addEventListener('click', () => {
          closeActiveOverlay();
          resolve(true);
        });
        foot.appendChild(confirmBtn);
      },
    });
  });
}

// ---------- copy to clipboard ---------------------------------
let _toastFn = null;
export function bindToast(fn) { _toastFn = fn; }
export async function copyToClipboard(text) {
  if (navigator && navigator.clipboard && navigator.clipboard.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      if (_toastFn) _toastFn(t('common.copied'), 'success', 1800);
      return true;
    } catch (_) { /* fall through */ }
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity  = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    if (_toastFn) _toastFn(ok ? t('common.copied') : t('common.copyFailed'), ok ? 'success' : 'error', 1800);
    return ok;
  } catch (_) {
    if (_toastFn) _toastFn(t('common.copyFailed'), 'error', 1800);
    return false;
  }
}
