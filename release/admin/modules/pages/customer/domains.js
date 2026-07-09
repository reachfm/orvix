/* =====================================================================
   modules/pages/customer/domains.js — Customer Admin · Domains.

   Renders the tenant-scoped domain list. Source of truth is the
   server-side admin handler at GET /api/v1/domains which already
   scopes results to the JWT-derived tenant. We render only the
   fields the handler returns (id, domain, plan, status,
   mailbox_count) and never fabricate counts, DKIM/DMARC state, or
   DNS posture — those belong on the dedicated Domain intelligence
   page reachable from the Internal console.

   Honesty contract:
     - The page never claims "managed" or "verified" state — status
       comes from the server's `status` column.
     - Mailbox counts are the literal value the handler returns; a
       failed query round-trips as "—" with an explicit error row
       so the operator can see the failure, not a guessed number.
     - Create / edit / delete controls are not surfaced here; the
       customer console is read-only by design in this build. The
       PATCH/POST endpoints exist server-side for future tenants.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   t('customerDomains.title') || 'Domains',
    subtitle: t('customerDomains.subtitle')
      || 'Tenant-scoped domain list. Status and mailbox counts come from the admin handler; nothing is calculated client-side.',
  });

  const body = panel(root, {
    title: t('customerDomains.listTitle') || 'Provisioned domains',
    meta:  t('customerDomains.meta')      || 'Read-only · tenant scope',
    body:  '',
  });

  // Honest loading placeholder (visible for ~1 round-trip).
  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let rows = [];
  let loadErr = null;
  try {
    rows = await apiGet('/api/v1/domains');
  } catch (err) {
    loadErr = err;
  }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('customerDomains.error') || 'Could not load domains: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }

  rows = Array.isArray(rows) ? rows : [];
  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: '◎' }),
      el('div', { class: 'empty-title', text: t('customerDomains.emptyTitle') || 'No domains provisioned' }),
      el('div', { class: 'empty-hint',
        text: t('customerDomains.emptyBody')
          || 'Ask your Orvix operator to provision a domain. Mailboxes cannot be created until a domain row exists for this tenant.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('customerDomains.col.domain')   || 'Domain' }),
    el('th', { text: t('customerDomains.col.plan')     || 'Plan' }),
    el('th', { text: t('customerDomains.col.status')   || 'Status' }),
    el('th', { text: t('customerDomains.col.mailboxes') || 'Mailboxes' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    const tr = el('tr', { 'data-search': ((row.domain || '') + ' ' + (row.plan || '') + ' ' + (row.status || '')).toLowerCase() });
    tr.appendChild(el('td', { class: 'kv-v', text: row.domain || '-' }));
    tr.appendChild(el('td', { class: 'kv-v', text: row.plan || '—' }));
    const state = badge(row.status || 'unknown', statusKind(row.status || 'unknown'));
    const statusCell = el('td', { class: 'kv-v' }, state);
    tr.appendChild(statusCell);
    tr.appendChild(el('td', { class: 'kv-v', text: fmtNumber(row.mailbox_count) }));
    tb.appendChild(tr);
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('customerDomains.honestNote')
      || 'DNS, DKIM and DMARC verification are not surfaced here. Open the Internal console → Domain intelligence for cross-tenant posture.' }));

  applyAutoDir(root);
}

