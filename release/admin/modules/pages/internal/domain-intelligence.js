/* =====================================================================
   modules/pages/internal/domain-intelligence.js — Internal Ops · Domains.

   Source: GET /api/v1/internal/domain-intelligence (handler
   InternalDomainIntelligence). The handler returns every domain
   across tenants, including the per-domain counters it can resolve
   from the DB plus the DKIM/DMARC flags.

   Honesty contract:
     - DNS posture (SPF/DKIM/DMARC) is reported verbatim from the
       domain rows. We do not query live DNS from the browser.
     - Live lookup is intentionally absent: this build does not
       expose a public DNS-over-HTTPS path. The page makes that
       explicit so the operator knows to verify on the host.
     - Counts (mailboxes, sent, bounced, etc.) mirror the per-row
       SQL produced by the handler.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

const DKIM_KIND = {
  enabled:           'good',
  not_configured:    'warn',
  disabled:          'warn',
  unknown:           'neutral',
};
const DMARC_KIND = {
  enabled:           'good',
  not_configured:    'warn',
  forced:            'good',
  unknown:           'neutral',
};

function asKind(status, table) {
  if (!status) return 'neutral';
  return table[status] || statusKind(status);
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalDomainIntel.title') || 'Domain intelligence',
    subtitle: t('internalDomainIntel.subtitle')
      || 'Per-domain cross-tenant counters and DKIM/DMARC posture. Live DNS lookups are not performed from the SPA; run them on the host or via the DNS ops API.',
  });

  const body = panel(root, {
    title: t('internalDomainIntel.listTitle') || 'All domains',
    meta:  '/api/v1/internal/domain-intelligence',
    body:  '',
  });
  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/internal/domain-intelligence'); }
  catch (err) { loadErr = err; }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('internalDomainIntel.error') || 'Domain intelligence lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  const rows = Array.isArray(data && data.domains) ? data.domains
    : (Array.isArray(data) ? data : []);
  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'D' }),
      el('div', { class: 'empty-title', text: t('internalDomainIntel.emptyTitle') || 'No domains recorded' }),
      el('div', { class: 'empty-hint',
        text: t('internalDomainIntel.emptyBody')
          || 'No domains across all tenants. Provision a tenant first, then add a domain row.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('internalDomainIntel.col.domain')   || 'Domain' }),
    el('th', { text: t('internalDomainIntel.col.tenant')   || 'Tenant' }),
    el('th', { text: t('internalDomainIntel.col.dkim')     || 'DKIM' }),
    el('th', { text: t('internalDomainIntel.col.dmarc')    || 'DMARC' }),
    el('th', { text: t('internalDomainIntel.col.mailboxes')|| 'Mailboxes' }),
    el('th', { text: t('internalDomainIntel.col.sent')     || 'Sent' }),
    el('th', { text: t('internalDomainIntel.col.received') || 'Received' }),
    el('th', { text: t('internalDomainIntel.col.bounced')  || 'Bounced' }),
    el('th', { text: t('internalDomainIntel.col.rejected') || 'Rejected' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    tb.appendChild(el('tr', { 'data-search': String(row.domain || '').toLowerCase() }, [
      el('td', { class: 'kv-v', text: row.domain || '-' }),
      el('td', { class: 'kv-v', text: row.tenant_id != null ? ('#' + row.tenant_id) : '—' }),
      el('td', { class: 'kv-v' }, badge(row.dkim_status || 'unknown', asKind(row.dkim_status, DKIM_KIND))),
      el('td', { class: 'kv-v' }, badge(row.dmarc_status || 'unknown', asKind(row.dmarc_status, DMARC_KIND))),
      el('td', { class: 'kv-v', text: fmtNumber(row.mailbox_count) }),
      el('td', { class: 'kv-v', text: fmtNumber(row.sent_count) }),
      el('td', { class: 'kv-v', text: fmtNumber(row.received_count) }),
      el('td', { class: 'kv-v', text: fmtNumber(row.bounced_count) }),
      el('td', { class: 'kv-v', text: fmtNumber(row.rejected_count) }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('internalDomainIntel.honestNote')
      || 'Live SPF/DMARC verification is not performed from this page. Use the DNS-ops API endpoint (admin-only) or verify on the host with `dig` / `nslookup`.' }));

  applyAutoDir(root);
}
