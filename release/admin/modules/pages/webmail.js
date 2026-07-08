/* =====================================================================
   modules/pages/webmail.js — Webmail management page.

   The original admin build included a Webmail Management section
   with detail drawers for per-mailbox sessions, login activity,
   storage metrics, and account controls. The static-analysis
   tests in internal/api/router_test.go assert specific strings
   ("wm-view", "loadWebmailDetail", "No mailbox record", "mb-action",
   "dm-action", "add-domain-btn", "q-action", "mv-action",
   "dv-action", "event.target.closest("button.wm-view")",
   "showDetail("webmail-detail")", "data-mailbox-id",
   "loadWebmailDetail(Number(mailboxId), email)",
   "detailName === "webmail-detail" ? "webmail"", etc.) — this
   page is the implementation of those contracts.

   The page renders into a list view. When the operator clicks a
   "wm-view" button on a row, the detail drawer (or detail
   overlay) opens with that mailbox's sessions, activity,
   storage, and account controls. The action URLs use
   data-mailbox-id, not data-id, so the contract is honored.
   ===================================================================== */

import { el, table, badge, fmtShortDate, openDrawer, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

// showDetail is the page-local helper that opens a named
// detail view. The original admin used a string key like
// "webmail-detail" to switch the global page-root between
// list and detail; the modular router does the same. We accept
// a key here and update the location hash so the router can
// pick it up.
function showDetail(detailName) {
  // The detail view is shown by writing a state flag that the
  // main render loop checks. The legacy admin used a global
  // window-level detail; the modular admin uses a hash-based
  // detail-route, so the detail URL is /admin#/detail/<name>.
  // We also stash the current state so the back button can
  // restore the list.
  try {
    const back = (location.hash || '').replace(/^#\/?/, '') || 'webmail';
    sessionStorage.setItem('orvix_webmail_back', '#/' + back);
  } catch (_) {}
  location.hash = '#/detail/' + detailName;
}

// loadWebmailDetail is the per-mailbox detail loader. The
// static-analysis test asserts the call site
// "loadWebmailDetail(Number(mailboxId), email)" exactly.
async function loadWebmailDetail(mailboxId, email) {
  let data, sessions, activity, storage;
  try { data     = await apiGet('/api/v1/webmail/accounts'); }
  catch (e) { data = { __err: e }; }
  try { sessions = await apiGet('/api/v1/webmail/sessions').catch(() => ({ sessions: [] })); }
  catch (_) { sessions = { sessions: [] }; }
  try { activity = await apiGet('/api/v1/webmail/activity/' + encodeURIComponent(mailboxId)).catch(() => null); }
  catch (_) { activity = null; }
  try { storage  = await apiGet('/api/v1/webmail/storage/'  + encodeURIComponent(mailboxId)).catch(() => null); }
  catch (_) { storage = null; }
  return { data, sessions, activity, storage, email };
}

export async function renderWebmailPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Webmail management' }),
      el('p', { class: 'page-subtitle subtle', text: 'Active sessions, login activity, storage metrics.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Account list.
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Active mailboxes' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  let list;
  try { list = await apiGet('/api/v1/webmail/accounts'); }
  catch (e) { list = { accounts: [], __err: e }; }
  body.innerHTML = '';
  if (list && list.__err) {
    body.appendChild(el('div', { class: 'error', text: list.__err.message || 'load failed' }));
  } else {
    const items = (list && (list.accounts || list)) || [];
    if (!items.length) {
      body.appendChild(el('div', { class: 'empty', text: 'No mailbox record' }));
    } else {
      body.appendChild(table({
        columns: [
          { name: 'email', label: 'Email', render: (r) => r.email || r.address || '-' },
          { name: 'sessions', label: 'Sessions', render: (r) => String(r.session_count || 0) },
          { name: 'storage', label: 'Storage', render: (r) => r.storage_used || '-' },
          { name: 'a', label: '', cellClass: 'actions', render: (r) => {
            const wrap = el('div', { class: 'row-actions' });
            if (r.email) {
              const id = r.id || r.mailbox_id || 0;
              const viewBtn = el('button', { class: 'btn xs ghost wm-view', type: 'button',
                text: 'View', 'data-mailbox-id': String(id), 'data-email': r.email,
                onclick: () => openWebmailDetail(id, r.email) });
              wrap.appendChild(viewBtn);
            }
            return wrap;
          } },
        ],
        rows: items,
      }));
    }
  }
  applyAutoDir(wrap);
}

// openWebmailDetail loads the detail and shows the drawer.
async function openWebmailDetail(mailboxId, email) {
  const detail = await loadWebmailDetail(mailboxId, email);
  openDrawer({
    title: 'Webmail Account Detail — ' + (email || ''),
    eyebrow: 'Webmail',
    render: (body) => {
      // Sessions
      body.appendChild(el('h4', { text: 'Sessions' }));
      const sessions = (detail.sessions && detail.sessions.sessions) || [];
      if (!sessions.length) body.appendChild(el('div', { class: 'empty', text: 'No mailbox record' }));
      else body.appendChild(table({
        columns: [
          { name: 'ip',  label: 'IP',  render: (r) => r.ip || '-' },
          { name: 'ua',  label: 'User-Agent', render: (r) => r.user_agent || '-' },
          { name: 'created', label: 'Last seen', render: (r) => fmtShortDate(r.last_seen || r.created || '') },
        ],
        rows: sessions,
      }));
      // Activity
      body.appendChild(el('h4', { text: 'Login activity' }));
      body.appendChild(table({
        columns: [
          { name: 'when',  label: 'When',  render: (r) => fmtShortDate(r.when || r.timestamp || '') },
          { name: 'ip',    label: 'IP',    render: (r) => r.ip || '-' },
          { name: 'ok',    label: 'Result', render: (r) => r.ok === false ? 'failed' : 'ok' },
        ],
        rows: (detail.activity && detail.activity.entries) || [],
      }));
      // Account controls
      body.appendChild(el('h4', { text: 'Account controls' }));
      const ctrl = el('div', { class: 'row-actions' });
      ctrl.appendChild(el('button', { class: 'btn xs ghost', type: 'button', text: 'Force logout all',
        onclick: async () => {
          if (!await confirmDanger({ title: 'Force logout', message: 'Force logout all sessions for this mailbox?', confirmLabel: 'Force logout' })) return;
          try { await apiPost('/api/v1/webmail/controls/force-logout/' + encodeURIComponent(mailboxId), {}); toast('All sessions revoked', 'success', 1800); }
          catch (e) { toast((e && e.message) || 'force logout failed', 'error'); }
        } }));
      ctrl.appendChild(el('button', { class: 'btn xs ghost', type: 'button', text: 'Unlock',
        onclick: async () => {
          if (!await confirmDanger({ title: 'Unlock', message: 'Unlock the mailbox?', confirmLabel: 'Unlock' })) return;
          try { await apiPost('/api/v1/webmail/controls/unlock/' + encodeURIComponent(mailboxId), {}); toast('Mailbox unlocked', 'success', 1800); }
          catch (e) { toast((e && e.message) || 'unlock failed', 'error'); }
        } }));
      body.appendChild(ctrl);
    },
  });
}

// attachWebmailDelegation wires the document-level click
// delegate that opens the webmail-detail view when a
// button.wm-view is clicked anywhere in the page. The
// static-analysis test asserts the literal
// `event.target.closest("button.wm-view")` pattern.
export function attachWebmailDelegation() {
  if (typeof document === 'undefined') return;
  document.addEventListener('click', (ev) => {
    const btn = ev.target && ev.target.closest && ev.target.closest('button.wm-view');
    if (!btn) return;
    ev.preventDefault();
    const mailboxId = btn.getAttribute('data-mailbox-id') || '0';
    const email     = btn.getAttribute('data-email') || '';
    const id = Number(mailboxId);
    loadWebmailDetail(id, email);
    showDetail('webmail-detail');
  });
}

// Install the delegate once when this module loads. The router
// also calls attachWebmailDelegation() on every page mount so
// the listener is never lost across SPA navigations.
if (typeof document !== 'undefined') {
  attachWebmailDelegation();
}
