/* =====================================================================
   modules/pages/customer/reports.js — Customer Admin · Reports.

   Default view: tenant-scoped report built from GET /api/v1/admin/reports

   Audit view (when opts.audit === true):
     GET /api/v1/admin/audit-logs → handler ListAdminAuditLogs
     Surfaces the audit log for the tenant so admins can answer
     "who changed what" without leaving the customer console.

   Honesty contract:
     - Reports mirror exactly what /api/v1/admin/reports returns.
       Trend lines are intentionally absent.
     - The audit table is paginated by the handler; we render the
       page the server sent and explicitly flag empty results.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, fmtShortDate, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root, opts) {
  opts = opts || {};
  const auditView = !!opts.audit;
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   auditView
      ? (t('customerReports.auditTitle') || 'Audit log')
      : (t('customerReports.title')     || 'Reports'),
    subtitle: auditView
      ? (t('customerReports.auditSubtitle')
          || 'Tenant-scoped admin audit log. Read-only; the source-of-truth is the backend audit table.')
      : (t('customerReports.subtitle')
          || 'Per-domain counters, top senders, top recipient domains. Numbers come from the report handler; the UI does not compute or extrapolate.'),
  });

  if (auditView) {
    return renderAudit(root);
  }
  return renderReport(root);
}

async function renderReport(root) {
  const summaryHost = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summaryHost);

  function placeholders() {
    summaryHost.innerHTML = '';
    [t('customerReports.kpi.sent')     || 'Sent',
     t('customerReports.kpi.received') || 'Received',
     t('customerReports.kpi.deferred') || 'Deferred',
     t('customerReports.kpi.bounced')  || 'Bounced',
     t('customerReports.kpi.rejected') || 'Rejected'].forEach((label) => {
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
      text: (t('customerReports.error') || 'Could not load report: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!report || typeof report !== 'object') {
    summaryHost.innerHTML = '';
    root.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'R' }),
      el('div', { class: 'empty-title', text: t('customerReports.emptyTitle') || 'No report data' }),
      el('div', { class: 'empty-hint',
        text: t('customerReports.emptyBody')
          || 'The report endpoint returned no payload. Try again after the next ingest window.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  summaryHost.innerHTML = '';
  [
    report.sent_count, report.received_count, report.deferred_count, report.bounced_count, report.rejected_count,
  ].forEach((v, i) => {
    if (v == null) summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'neutral' }, [
      el('div', { class: 'kpi-pro-label', text: ['Sent','Received','Deferred','Bounced','Rejected'][i] }),
      el('div', { class: 'kpi-pro-value', text: '—' }),
    ]));
  });
  // The above intentionally only fills in the KPIs that the
  // handler returned; remaining slots stay at "—".

  // Top senders
  const senders = Array.isArray(report.top_senders) ? report.top_senders : [];
  const sendersPanel = panel(root, {
    title: t('customerReports.topSendersTitle') || 'Top senders',
    meta:  senders.length ? (t('customerReports.topMeta', { n: senders.length }) || (senders.length + ' rows')) : '',
    body:  '',
  });
  if (senders.length === 0) {
    sendersPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('customerReports.topEmpty') || 'No outbound traffic yet.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('customerReports.col.sender') || 'Sender' }),
      el('th', { text: t('customerReports.col.count')  || 'Count' }),
    ])));
    const tb = el('tbody');
    senders.forEach((row) => {
      tb.appendChild(el('tr', { 'data-search': String(row.sender || '').toLowerCase() }, [
        el('td', { class: 'kv-v', text: row.sender || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    sendersPanel.appendChild(tbl);
  }

  // Top recipient domains
  const recipients = Array.isArray(report.top_recipient_domains) ? report.top_recipient_domains : [];
  const recipPanel = panel(root, {
    title: t('customerReports.topRecipientsTitle') || 'Top recipient domains',
    meta:  recipients.length ? (recipients.length + ' rows') : '',
    body:  '',
  });
  if (recipients.length === 0) {
    recipPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('customerReports.topRecipientsEmpty') || 'No recipient-domain data.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('customerReports.col.domain') || 'Domain' }),
      el('th', { text: t('customerReports.col.count')  || 'Count' }),
    ])));
    const tb = el('tbody');
    recipients.forEach((row) => {
      tb.appendChild(el('tr', { 'data-search': String(row.domain || '').toLowerCase() }, [
        el('td', { class: 'kv-v', text: row.domain || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    recipPanel.appendChild(tbl);
  }

  root.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: report.csv_export_available
      ? (t('customerReports.exportReady') || 'CSV export is available on this endpoint.')
      : (report.csv_export_unavailable_reason
          || t('customerReports.exportUnavailable')
          || 'CSV export is not enabled for this report endpoint yet.') }));

  applyAutoDir(root);
}

async function renderAudit(root) {
  const body = panel(root, {
    title: t('customerReports.auditListTitle') || 'Admin audit log',
    meta:  '/api/v1/admin/audit-logs',
    body:  '',
  });
  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/admin/audit-logs'); }
  catch (err) { loadErr = err; }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('customerReports.auditError') || 'Audit log lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  const rows = Array.isArray(data) ? data : (Array.isArray(data && data.logs) ? data.logs : []);
  if (rows.length === 0) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: '∅' }),
      el('div', { class: 'empty-title', text: t('customerReports.auditEmptyTitle') || 'No audit entries' }),
      el('div', { class: 'empty-hint',
        text: t('customerReports.auditEmptyBody')
          || 'Once an admin action lands in the audit table it appears here. This view is read-only and never writes.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('customerReports.col.timestamp') || 'Timestamp' }),
    el('th', { text: t('customerReports.col.actor')    || 'Actor' }),
    el('th', { text: t('customerReports.col.action')   || 'Action' }),
    el('th', { text: t('customerReports.col.target')   || 'Target' }),
    el('th', { text: t('customerReports.col.result')   || 'Result' }),
  ])));
  const tb = el('tbody');
  rows.forEach((row) => {
    tb.appendChild(el('tr', null, [
      el('td', { class: 'kv-v', text: fmtShortDate(row.timestamp || row.created_at || '') }),
      el('td', { class: 'kv-v', text: row.actor || row.user || '-' }),
      el('td', { class: 'kv-v', text: row.action || row.event || '-' }),
      el('td', { class: 'kv-v', text: row.target || row.resource || '-' }),
      el('td', { class: 'kv-v', text: row.result || row.status || '-' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  applyAutoDir(root);
}
