/* =====================================================================
   modules/pages/internal/tenants.js — Internal Ops · Tenants list.

   Source: GET /api/v1/internal/tenants (handler InternalTenants).
   The handler returns { tenants: [ {id, name, slug, domain, plan,
   active, domains, mailboxes, storage_bytes, login_failures,
   deferred_count, rejected_count}, ... ] }.

   Honesty contract:
     - All counts come from the handler. We never recompute them
       or estimate them from a row count.
     - "Active" / "Plan" reflect the row exactly. We do not
       fabricate SLA tiers or "trial" / "production" labels.
     - Mutations (suspend, change plan, delete) are not exposed
       in this build.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, fmtBytes, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalTenants.title') || 'Tenants',
    subtitle: t('internalTenants.subtitle')
      || 'Cross-tenant list. Active + domain/mailbox/storage counts come straight from the internal handler.',
  });

  const body = panel(root, {
    title: t('internalTenants.listTitle') || 'All tenants',
    meta:  '/api/v1/internal/tenants',
    body:  '',
  });
  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/internal/tenants'); }
  catch (err) { loadErr = err; }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('internalTenants.error') || 'Tenants lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }

  const rows = Array.isArray(data && data.tenants) ? data.tenants
    : (Array.isArray(data) ? data : []);

  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'T' }),
      el('div', { class: 'empty-title', text: t('internalTenants.emptyTitle') || 'No tenants provisioned' }),
      el('div', { class: 'empty-hint',
        text: t('internalTenants.emptyBody')
          || 'The internal handler returned an empty tenant list. Provision a tenant to populate this view.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('internalTenants.col.name')       || 'Name' }),
    el('th', { text: t('internalTenants.col.domain')     || 'Domain' }),
    el('th', { text: t('internalTenants.col.plan')       || 'Plan' }),
    el('th', { text: t('internalTenants.col.active')     || 'Active' }),
    el('th', { text: t('internalTenants.col.domains')    || 'Domains' }),
    el('th', { text: t('internalTenants.col.mailboxes')  || 'Mailboxes' }),
    el('th', { text: t('internalTenants.col.storage')    || 'Storage' }),
    el('th', { text: t('internalTenants.col.failures')   || 'Failures' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    tb.appendChild(el('tr', { 'data-search': ((row.name || '') + ' ' + (row.domain || '') + ' ' + (row.plan || '')).toLowerCase() }, [
      el('td', { class: 'kv-v', text: row.name || '-' }),
      el('td', { class: 'kv-v', text: row.domain || '-' }),
      el('td', { class: 'kv-v', text: row.plan || '—' }),
      el('td', { class: 'kv-v' }, badge(row.active ? 'yes' : 'no', statusKind(row.active ? 'active' : 'disabled'))),
      el('td', { class: 'kv-v', text: fmtNumber(row.domains) }),
      el('td', { class: 'kv-v', text: fmtNumber(row.mailboxes) }),
      el('td', { class: 'kv-v', text: row.storage_bytes != null ? fmtBytes(row.storage_bytes) : '—' }),
      el('td', { class: 'kv-v', text: fmtNumber(row.login_failures) }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('internalTenants.honestNote')
      || 'Tenant create / suspend / delete APIs are not exposed in this build. Use the operator CLI for those actions.' }));

  applyAutoDir(root);
}
