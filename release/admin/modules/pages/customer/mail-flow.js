/* =====================================================================
   modules/pages/customer/mail-flow.js — Customer Admin · Mail flow.

   Pulls the tenant-scoped report produced by GET /api/v1/admin/reports
   and renders the inbound/outbound counters we have today, plus the
   top sender/recipient pairs. The handler returns real DB counts;
   we never inflate, pad, or fabricate trend numbers.

   Honesty contract:
     - "Not reported" is the default when the handler omits a
       counter — we do not synthesise a 0.
     - Trend deltas are intentionally absent; the build has no
       time-series retention.
   ===================================================================== */

import { pageHeader, panel, kpiRow, emptyState, apiGet, fmtNumber, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

function asNumber(v) {
  if (v == null) return null;
  if (typeof v === 'number' && Number.isFinite(v)) return v;
  if (typeof v === 'object' && typeof v.total === 'number') return v.total;
  if (typeof v === 'string') {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   t('customerMailFlow.title') || 'Mail flow',
    subtitle: t('customerMailFlow.subtitle')
      || 'Tenant-scoped counters for inbound, outbound, deferred, bounced, and rejected messages. Reports are computed from the same DB the admin queue page uses.',
  });

  const summaryHost = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summaryHost);

  // Seed the KPI strip with honest placeholders so the page is
  // never blank while the request is in flight.
  function placeholders() {
    summaryHost.innerHTML = '';
    [t('customerMailFlow.kpi.sent')     || 'Sent',
     t('customerMailFlow.kpi.received') || 'Received',
     t('customerMailFlow.kpi.deferred') || 'Deferred',
     t('customerMailFlow.kpi.bounced')  || 'Bounced',
     t('customerMailFlow.kpi.rejected') || 'Rejected'].forEach((label) => {
      summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
        el('div', { class: 'kpi-pro-label', text: label }),
        el('div', { class: 'kpi-pro-value', text: '—' }),
      ]));
    });
  }
  placeholders();

  let report = null;
  let loadErr = null;
  try { report = await apiGet('/api/v1/admin/reports'); }
  catch (err) { loadErr = err; }

  if (loadErr) {
    summaryHost.innerHTML = '';
    root.appendChild(el('div', { class: 'error',
      text: (t('customerMailFlow.error') || 'Could not load mail-flow report: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!report || typeof report !== 'object') {
    summaryHost.innerHTML = '';
    root.appendChild(emptyState(document.createDocumentFragment(), {
      title: t('customerMailFlow.emptyTitle') || 'No mail-flow data yet',
      body:  t('customerMailFlow.emptyBody')
        || 'Once the tenant sees inbound or outbound traffic, the counters will appear here.',
    }).firstChild ? el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'M' }),
      el('div', { class: 'empty-title', text: t('customerMailFlow.emptyTitle') || 'No mail-flow data yet' }),
      el('div', { class: 'empty-hint',
        text: t('customerMailFlow.emptyBody')
          || 'Once the tenant sees inbound or outbound traffic, the counters will appear here.' }),
    ]) : null);
    applyAutoDir(root);
    return;
  }

  summaryHost.innerHTML = '';
  kpiRow(summaryHost, [
    { label: t('customerMailFlow.kpi.sent')     || 'Sent',     value: asNumber(report.sent_count) != null     ? fmtNumber(asNumber(report.sent_count))     : '—', tone: 'good' },
    { label: t('customerMailFlow.kpi.received') || 'Received', value: asNumber(report.received_count) != null ? fmtNumber(asNumber(report.received_count)) : '—', tone: 'good' },
    { label: t('customerMailFlow.kpi.deferred') || 'Deferred', value: asNumber(report.deferred_count) != null ? fmtNumber(asNumber(report.deferred_count)) : '—', tone: 'warn' },
    { label: t('customerMailFlow.kpi.bounced')  || 'Bounced',  value: asNumber(report.bounced_count) != null  ? fmtNumber(asNumber(report.bounced_count))  : '—', tone: 'bad'  },
    { label: t('customerMailFlow.kpi.rejected') || 'Rejected', value: asNumber(report.rejected_count) != null ? fmtNumber(asNumber(report.rejected_count)) : '—', tone: 'bad'  },
  ]);

  // Per-domain breakdown (the AdminReports handler ships a
  // domains array; render as a table).
  const domainPanel = panel(root, {
    title: t('customerMailFlow.domainsTitle') || 'Per-domain counters',
    meta:  report && report.scope && report.scope.tenant_id != null ? ('tenant #' + report.scope.tenant_id) : '',
    body:  '',
  });
  const domains = Array.isArray(report.domains) ? report.domains : [];
  if (domains.length === 0) {
    domainPanel.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'D' }),
      el('div', { class: 'empty-title', text: t('customerMailFlow.domainsEmpty') || 'No domains in this report' }),
      el('div', { class: 'empty-hint',
        text: t('customerMailFlow.domainsHint')
          || 'The handler returns one row per provisioned domain. Empty here means none yet.' }),
    ]));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('customerMailFlow.col.domain')   || 'Domain' }),
      el('th', { text: t('customerMailFlow.col.sent')     || 'Sent' }),
      el('th', { text: t('customerMailFlow.col.received') || 'Received' }),
      el('th', { text: t('customerMailFlow.col.bounced')  || 'Bounced' }),
      el('th', { text: t('customerMailFlow.col.rejected') || 'Rejected' }),
      el('th', { text: t('customerMailFlow.col.deferred') || 'Deferred' }),
    ])));
    const tb = el('tbody');
    domains.forEach((row) => {
      tb.appendChild(el('tr', { 'data-search': (row.domain || '').toLowerCase() }, [
        el('td', { class: 'kv-v', text: row.domain || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(asNumber(row.sent_count)     != null ? asNumber(row.sent_count)     : null) }),
        el('td', { class: 'kv-v', text: fmtNumber(asNumber(row.received_count) != null ? asNumber(row.received_count) : null) }),
        el('td', { class: 'kv-v', text: fmtNumber(asNumber(row.bounced_count)  != null ? asNumber(row.bounced_count)  : null) }),
        el('td', { class: 'kv-v', text: fmtNumber(asNumber(row.rejected_count) != null ? asNumber(row.rejected_count) : null) }),
        el('td', { class: 'kv-v', text: fmtNumber(asNumber(row.deferred_count) != null ? asNumber(row.deferred_count) : null) }),
      ]));
    });
    tbl.appendChild(tb);
    domainPanel.appendChild(tbl);
  }

  // CSV export note — the handler returns "csv_export_unavailable_reason".
  root.appendChild(el('div', { class: 'ops-honest-note panel-foot',
    text: report.csv_export_available
      ? (t('customerMailFlow.exportReady') || 'CSV export is enabled on the backend.')
      : (report.csv_export_unavailable_reason
          || t('customerMailFlow.exportUnavailable')
          || 'CSV export is not enabled for this report endpoint yet.') }));

  applyAutoDir(root);
}
