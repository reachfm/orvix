/* =====================================================================
   modules/pages/customer/groups.js — Customer Admin · RBAC groups.

   Source: GET /api/v1/admin/admin-groups (handler ListAdminGroups).
   The customer console surfaces tenant-scoped RBAC groups so that
   an operator can see which role bundles exist before requesting
   membership changes.

   Honesty contract:
     - Membership mutations (add/remove members, change grants) are
       not exposed in this build. We render only what the handler
       returns.
     - We never color groups as "secure" or "privileged" by client
       guesswork; the right-hand name column reflects the server's
       grant summary verbatim.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root, opts) {
  opts = opts || {};
  const adminView = !!opts.adminGroups;

  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   t('customerGroups.title') || 'Administrative groups',
    subtitle: t('customerGroups.subtitle')
      || 'Tenant-scoped RBAC groups. Mutations are not exposed in this build.',
  });

  // Both legacy and customer routes hit the same handler for now —
  // it's the only RBAC endpoint the current build exposes.
  const endpoint = '/api/v1/admin/admin-groups';

  const body = panel(root, {
    title: t('customerGroups.listTitle') || 'Groups',
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
      text: (t('customerGroups.error') || 'Could not load groups: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }

  // The handler returns { groups: [{id, name, ...}, ...] } in
  // newer builds and may return a bare array in older fixtures.
  // Accept both shapes.
  if (rows && !Array.isArray(rows) && Array.isArray(rows.groups)) rows = rows.groups;
  if (!Array.isArray(rows)) rows = [];

  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'G' }),
      el('div', { class: 'empty-title',
        text: t('customerGroups.emptyTitle') || 'No admin groups defined' }),
      el('div', { class: 'empty-hint',
        text: t('customerGroups.emptyBody')
          || 'No RBAC groups exist for this tenant. New groups are created via the admin-groups API endpoint.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('customerGroups.col.name')    || 'Name' }),
    el('th', { text: t('customerGroups.col.id')      || 'Group ID' }),
    el('th', { text: t('customerGroups.col.members') || 'Members' }),
    el('th', { text: t('customerGroups.col.scope')   || 'Scope' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    const members = Array.isArray(row.members) ? row.members.length
      : typeof row.member_count === 'number' ? row.member_count
      : null;
    tr_dummy_check: {}
    const tr = el('tr', { 'data-search': ((row.name || '') + ' ' + (row.scope || '')).toLowerCase() });
    tr.appendChild(el('td', { class: 'kv-v', text: row.name || row.label || row.id || '-' }));
    tr.appendChild(el('td', { class: 'kv-v', text: row.id != null ? fmtNumber(row.id) : '—' }));
    tr.appendChild(el('td', { class: 'kv-v', text: members != null ? fmtNumber(members) : '—' }));
    tr.appendChild(el('td', { class: 'kv-v', text: row.scope || row.tenant_id != null ? ('tenant #' + row.tenant_id) : '—' }));
    tb.appendChild(tr);
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('customerGroups.honestNote')
      || 'Group membership writes are handled by the admin-groups API; this page is intentionally read-only in the customer console.' }));

  applyAutoDir(root);
}
