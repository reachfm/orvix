/* =====================================================================
   modules/pages/internal/mail-flow-ops.js — Internal Ops · Mail flow.

   Source: GET /api/v1/internal/mail-flow-ops (handler InternalMailFlowOps).

   Response payload:
     {
       queue_depth,
       deferred,
       bounces,
       outbound_errors,
       top_queued_domains: [{domain, count}],
       status_counts:      [{status, count}]
     }

   Honesty contract:
     - This is a cross-tenant view. Every counter is platform-
       wide; we don't claim "per-tenant" here.
     - Status distributions come from coremail_queue aggregated
       in SQL; no client-side reduction.
   ===================================================================== */

import { pageHeader, panel, kpiRow, apiGet, fmtNumber, t, el } from '../saas-shared.js';
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
    title:   t('internalMailFlowOps.title') || 'Mail flow ops',
    subtitle: t('internalMailFlowOps.subtitle')
      || 'Cross-tenant queue telemetry. Depth, deferred, bounces, outbound errors, and the top queued recipient domains.',
  });

  const summary = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summary);
  function placeholders() {
    summary.innerHTML = '';
    [t('internalMailFlowOps.kpi.queue')    || 'Queue depth',
     t('internalMailFlowOps.kpi.deferred') || 'Deferred',
     t('internalMailFlowOps.kpi.bounces')  || 'Bounces',
     t('internalMailFlowOps.kpi.outbound') || 'Outbound errors'].forEach((label) => {
      summary.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
        el('div', { class: 'kpi-pro-label', text: label }),
        el('div', { class: 'kpi-pro-value', text: '—' }),
      ]));
    });
  }
  placeholders();

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/internal/mail-flow-ops'); }
  catch (err) { loadErr = err; }

  if (loadErr) {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'error',
      text: (t('internalMailFlowOps.error') || 'Mail-flow-ops lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!data || typeof data !== 'object') {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'M' }),
      el('div', { class: 'empty-title', text: t('internalMailFlowOps.emptyTitle') || 'No queue data' }),
      el('div', { class: 'empty-hint',
        text: t('internalMailFlowOps.emptyBody')
          || 'The handler returned no payload. Verify that the queue table is reachable.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  summary.innerHTML = '';
  kpiRow(summary, [
    { label: t('internalMailFlowOps.kpi.queue')    || 'Queue depth',     value: n(data.queue_depth),    tone: 'info' },
    { label: t('internalMailFlowOps.kpi.deferred') || 'Deferred',        value: n(data.deferred),       tone: 'warn' },
    { label: t('internalMailFlowOps.kpi.bounces')  || 'Bounces',         value: n(data.bounces),        tone: 'bad'  },
    { label: t('internalMailFlowOps.kpi.outbound') || 'Outbound errors', value: n(data.outbound_errors), tone: 'bad'  },
  ]);

  // Status counts
  const status = Array.isArray(data.status_counts) ? data.status_counts : [];
  const statusPanel = panel(root, {
    title: t('internalMailFlowOps.statusTitle') || 'Queue status counts',
    meta:  status.length ? (status.length + ' rows') : '',
    body:  '',
  });
  if (status.length === 0) {
    statusPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalMailFlowOps.statusEmpty') || 'No rows in coremail_queue right now.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalMailFlowOps.col.status') || 'Status' }),
      el('th', { text: t('internalMailFlowOps.col.count')  || 'Count' }),
    ])));
    const tb = el('tbody');
    status.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: row.status || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    statusPanel.appendChild(tbl);
  }

  // Top queued recipient domains
  const queued = Array.isArray(data.top_queued_domains) ? data.top_queued_domains : [];
  const queuedPanel = panel(root, {
    title: t('internalMailFlowOps.queuedTitle') || 'Top queued recipient domains',
    meta:  queued.length ? (queued.length + ' rows') : '',
    body:  '',
  });
  if (queued.length === 0) {
    queuedPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalMailFlowOps.queuedEmpty') || 'No recipient-domain data.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalMailFlowOps.col.domain') || 'Domain' }),
      el('th', { text: t('internalMailFlowOps.col.count')  || 'Count' }),
    ])));
    const tb = el('tbody');
    queued.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: row.domain || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count) }),
      ]));
    });
    tbl.appendChild(tb);
    queuedPanel.appendChild(tbl);
  }

  applyAutoDir(root);
}
