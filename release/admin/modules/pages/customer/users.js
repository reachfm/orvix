/* =====================================================================
   modules/pages/customer/users.js — Customer Admin · Mailboxes/Admins.

   Default view: tenant-scoped mailbox users
     GET /api/v1/users → handler ListUsers in handlers.go

   Admin-users view (when opts.adminUsers === true):
     GET /api/v1/admin/admin-users → handler ListAdminUsers

   Honesty contract:
     - We do not invent role assignments or pending invitations.
       The server returns ready rows; absence is shown as an
       honest empty state, not a placeholder.
     - Status values come straight from the server's `status`
       column. We never flip "active"/"suspended" client-side.
     - Search + status filter are kept simple — anything beyond
       what the handler accepts is left untouched.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

const STATUS_KIND = statusKind;

export async function render(root, opts) {
  opts = opts || {};
  const adminView = !!opts.adminUsers;

  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   adminView
      ? (t('customerUsers.adminTitle')  || 'Administrative users')
      : (t('customerUsers.title')       || 'Mailbox users'),
    subtitle: adminView
      ? (t('customerUsers.adminSubtitle')
          || 'Tenant-scoped admin/staff users. Comes from the staff-management handler.')
      : (t('customerUsers.subtitle')
          || 'Tenant-scoped mailbox rows. Server-side tenant scope is authoritative; nothing is recomputed client-side.'),
  });

  const endpoint = adminView
    ? '/api/v1/admin/admin-users'
    : '/api/v1/users';

  const body = panel(root, {
    title: adminView
      ? (t('customerUsers.adminListTitle') || 'Admin users')
      : (t('customerUsers.listTitle')      || 'Users'),
    meta:  endpoint,
    body:  '',
  });

  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let rows = [];
  let loadErr = null;
  try {
    rows = await apiGet(endpoint);
  } catch (err) {
    loadErr = err;
  }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('customerUsers.error') || 'Could not load users: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }

  rows = Array.isArray(rows) ? rows : [];
  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: adminView ? 'U' : 'M' }),
      el('div', { class: 'empty-title',
        text: adminView
          ? (t('customerUsers.emptyAdminTitle') || 'No admin users defined')
          : (t('customerUsers.emptyTitle')      || 'No mailbox users') }),
      el('div', { class: 'empty-hint',
        text: adminView
          ? (t('customerUsers.emptyAdminBody')
              || 'Tenant has no admin rows. Add the first admin user from the staff-management UI when the build exposes it.')
          : (t('customerUsers.emptyBody')
              || 'Tenant has no mailbox rows. Provision a mailbox via the bulk import or the create-mailbox API once available.') }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('customerUsers.col.email')  || 'Email' }),
    el('th', { text: t('customerUsers.col.role')   || 'Role' }),
    el('th', { text: t('customerUsers.col.status') || 'Status' }),
    adminView
      ? el('th', { text: t('customerUsers.col.id') || 'User ID' })
      : el('th', { text: t('customerUsers.col.mailbox') || 'Mailbox ID' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    const tr = el('tr', { 'data-search': ((row.email || '') + ' ' + (row.role || '') + ' ' + (row.status || '')).toLowerCase() });
    tr.appendChild(el('td', { class: 'kv-v', text: row.email || '-' }));
    tr.appendChild(el('td', { class: 'kv-v', text: row.role || (row.is_admin ? 'admin' : 'mailbox') }));
    tr.appendChild(el('td', { class: 'kv-v' }, badge(row.status || 'unknown', STATUS_KIND(row.status || 'unknown'))));
    if (adminView) {
      tr.appendChild(el('td', { class: 'kv-v', text: row.id != null ? fmtNumber(row.id) : '—' }));
    } else {
      tr.appendChild(el('td', { class: 'kv-v', text: row.mailbox_id != null ? fmtNumber(row.mailbox_id) : '—' }));
    }
    tb.appendChild(tr);
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: adminView
      ? (t('customerUsers.adminHonestNote')
          || 'Admin-user writes (create / disable / password reset) are not surfaced in this build. Reach for the staff-management API directly.')
      : (t('customerUsers.honestNote')
          || 'Mailbox create / password reset / quota edits are available server-side but not wired into this read-only console view.') }));

  applyAutoDir(root);
}
