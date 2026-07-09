/* =====================================================================
   modules/pages/internal/security-ops.js — Internal Ops · Security operations.

   Source: GET /api/v1/internal/security-ops (handler InternalSecurityOps).

   Response payload (InternalSecurityOps in saas_admin.go):
     {
       failed_auth_total,
       suspicious_total,
       lockouts,
       failed_auth_domains: [{key, count}],
       attacked_accounts:  [{key, count}],
       timeline:           [{ip, email, event_type, count, created_at}]
     }

   Honesty contract:
     - Lockouts count comes from the trust service. The list is
       intentionally summarised; clearing a lockout is not
       exposed in this build (must use the trust-management CLI).
     - Aggregations are computed in SQL; we render them as-is so
       the UI never disagrees with the backend.
   ===================================================================== */

import { pageHeader, panel, kpiRow, apiGet, fmtNumber, fmtShortDate, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

function n(v) {
  if (v == null) return '—';
  if (typeof v === 'number' && Number.isFinite(v)) return fmtNumber(v);
  return String(v);
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalSecurityOps.title') || 'Security operations',
    subtitle: t('internalSecurityOps.subtitle')
      || 'Cross-tenant security telemetry. Failed auth, suspicious logins, lockouts, and per-target groupings.',
  });

  const summary = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summary);

  function placeholders() {
    summary.innerHTML = '';
    [t('internalSecurityOps.kpi.failed')     || 'Failed auth',
     t('internalSecurityOps.kpi.suspicious') || 'Suspicious',
     t('internalSecurityOps.kpi.lockouts')   || 'Lockouts'].forEach((label) => {
      summary.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
        el('div', { class: 'kpi-pro-label', text: label }),
        el('div', { class: 'kpi-pro-value', text: '—' }),
      ]));
    });
  }
  placeholders();

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/internal/security-ops'); }
  catch (err) { loadErr = err; }

  if (loadErr) {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'error',
      text: (t('internalSecurityOps.error') || 'Security-ops lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!data || typeof data !== 'object') {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'S' }),
      el('div', { class: 'empty-title', text: t('internalSecurityOps.emptyTitle') || 'No security data' }),
      el('div', { class: 'empty-hint',
        text: t('internalSecurityOps.emptyBody')
          || 'The security-ops endpoint returned no payload.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  summary.innerHTML = '';
  kpiRow(summary, [
    { label: t('internalSecurityOps.kpi.failed')     || 'Failed auth',  value: n(data.failed_auth_total), tone: 'bad'  },
    { label: t('internalSecurityOps.kpi.suspicious') || 'Suspicious',   value: n(data.suspicious_total),   tone: 'warn' },
    { label: t('internalSecurityOps.kpi.lockouts')   || 'Lockouts',     value: n(data.lockouts),           tone: 'info' },
  ]);

  // Failed-auth-by-domain
  const byDomain = Array.isArray(data.failed_auth_domains) ? data.failed_auth_domains : [];
  const domainPanel = panel(root, {
    title: t('internalSecurityOps.byDomainTitle') || 'Failed auth by domain',
    meta:  byDomain.length ? (byDomain.length + ' rows') : '',
    body:  '',
  });
  if (byDomain.length === 0) {
    domainPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalSecurityOps.byDomainEmpty') || 'No grouping data.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalSecurityOps.col.domain') || 'Domain' }),
      el('th', { text: t('internalSecurityOps.col.count')  || 'Count' }),
    ])));
    const tb = el('tbody');
    byDomain.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: row.key || row.domain || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    domainPanel.appendChild(tbl);
  }

  // Attacked accounts
  const accounts = Array.isArray(data.attacked_accounts) ? data.attacked_accounts : [];
  const acctPanel = panel(root, {
    title: t('internalSecurityOps.accountsTitle') || 'Top attacked accounts',
    meta:  accounts.length ? (accounts.length + ' rows') : '',
    body:  '',
  });
  if (accounts.length === 0) {
    acctPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalSecurityOps.accountsEmpty') || 'No per-account data.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalSecurityOps.col.account') || 'Account' }),
      el('th', { text: t('internalSecurityOps.col.count')   || 'Count' }),
    ])));
    const tb = el('tbody');
    accounts.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: row.key || row.email || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    acctPanel.appendChild(tbl);
  }

  // Timeline
  const timeline = Array.isArray(data.timeline) ? data.timeline : [];
  const timelinePanel = panel(root, {
    title: t('internalSecurityOps.timelineTitle') || 'Recent security events',
    meta:  timeline.length ? (timeline.length + ' rows') : '',
    body:  '',
  });
  if (timeline.length === 0) {
    timelinePanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalSecurityOps.timelineEmpty') || 'No recent security events.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalSecurityOps.col.created') || 'When' }),
      el('th', { text: t('internalSecurityOps.col.type')    || 'Type' }),
      el('th', { text: t('internalSecurityOps.col.email')   || 'Email' }),
      el('th', { text: t('internalSecurityOps.col.ip')      || 'IP' }),
      el('th', { text: t('internalSecurityOps.col.count')   || 'Count' }),
    ])));
    const tb = el('tbody');
    timeline.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: fmtShortDate(row.created_at || '') }),
        el('td', { class: 'kv-v', text: row.event_type || '-' }),
        el('td', { class: 'kv-v', text: row.email || '-' }),
        el('td', { class: 'kv-v', text: row.ip || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    timelinePanel.appendChild(tbl);
  }

  root.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('internalSecurityOps.honestNote')
      || 'Clear-lockout writes are handled by the trust-management CLI/API; not exposed in this UI.' }));

  applyAutoDir(root);
}
