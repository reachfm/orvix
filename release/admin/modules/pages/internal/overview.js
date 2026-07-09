/* =====================================================================
   modules/pages/internal/overview.js — Internal Ops · Cross-tenant overview.

   Source: GET /api/v1/internal/overview (handler InternalOverview).
   Renders the platform-wide counter set returned by the handler.

   The handler returns:
     {
       platform:       { status, generated_at },
       tenant_count, domains_count, mailbox_count,
       queue:          { pending, deferred, failed },
       security:       { failed_logins, suspicious },
       recent_alerts:  [],
       recent_audit_events: [{actor, action, target, result, timestamp}]
     }

   Honesty contract:
     - All numbers are taken verbatim from the API. We never
       fabricate trend deltas or model success metrics.
     - We do not surface "alerts" unless the handler supplies
       them — empty array means empty list, not "no issues".
   ===================================================================== */

import { pageHeader, panel, kpiRow, apiGet, fmtNumber, fmtShortDate, badge, statusKind, t, el } from '../saas-shared.js';
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
    title:   t('internalOverview.title') || 'Orvix Internal Overview',
    subtitle: t('internalOverview.subtitle')
      || 'Cross-tenant platform health. Numbers come straight from /api/v1/internal/overview.',
  });

  const summary = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summary);

  function placeholders() {
    summary.innerHTML = '';
    [t('internalOverview.kpi.tenants')   || 'Tenants',
     t('internalOverview.kpi.domains')   || 'Domains',
     t('internalOverview.kpi.mailboxes') || 'Mailboxes',
     t('internalOverview.kpi.queue')     || 'Queue'],
    [t('internalOverview.kpi.failed')    || 'Failed logins'].forEach((label) => {
      summary.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
        el('div', { class: 'kpi-pro-label', text: label }),
        el('div', { class: 'kpi-pro-value', text: '—' }),
      ]));
    });
  }
  placeholders();

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/internal/overview'); }
  catch (err) { loadErr = err; }

  if (loadErr) {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'error',
      text: (t('internalOverview.error') || 'Internal overview failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!data || typeof data !== 'object') {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'O' }),
      el('div', { class: 'empty-title', text: t('internalOverview.emptyTitle') || 'No overview data' }),
      el('div', { class: 'empty-hint',
        text: t('internalOverview.emptyBody')
          || 'The internal overview endpoint returned no payload. This usually means no tenants are configured yet.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  summary.innerHTML = '';
  kpiRow(summary, [
    { label: t('internalOverview.kpi.tenants')   || 'Tenants',   value: n(data.tenant_count),   tone: 'info' },
    { label: t('internalOverview.kpi.domains')   || 'Domains',   value: n(data.domains_count),  tone: 'info' },
    { label: t('internalOverview.kpi.mailboxes') || 'Mailboxes', value: n(data.mailbox_count),  tone: 'good' },
    { label: t('internalOverview.kpi.queue')     || 'Queue',     value: data.queue ? fmtNumber((data.queue.pending || 0) + (data.queue.deferred || 0) + (data.queue.failed || 0)) : '—', tone: 'warn' },
    { label: t('internalOverview.kpi.failed')    || 'Failed logins', value: data.security ? n(data.security.failed_logins) : '—', tone: 'bad' },
  ]);

  // Platform status banner
  const platform = data.platform || {};
  if (platform.status || platform.generated_at) {
    const state = (platform.status || '').toLowerCase();
    const kind = state === 'ok' ? 'good' : state === 'degraded' ? 'warn' : 'neutral';
    const banner = el('div', { class: 'platform-banner panel' }, [
      el('header', { class: 'panel-head' }, [
        el('h3', null, [el('span', { class: 'panel-icon', text: 'P' }),
                         document.createTextNode(' Platform')]),
        el('span', { class: 'panel-head-meta' }, platform.status
          ? [badge(platform.status, kind)]
          : []),
      ]),
      el('div', { class: 'panel-body' }, platform.generated_at
        ? [el('div', { class: 'kv-row' }, [
            el('span', { class: 'kv-k', text: t('internalOverview.generated') || 'Generated' }),
            el('span', { class: 'kv-v', text: fmtShortDate(platform.generated_at) }),
          ])]
        : []),
    ]);
    root.appendChild(banner);
  }

  // Queue detail
  if (data.queue) {
    const queue = data.queue;
    const queuePanel = panel(root, {
      title: t('internalOverview.queueTitle') || 'Queue detail',
      meta:  '',
      body:  '',
    });
    const dl = el('dl', { class: 'kv' });
    [
      [t('internalOverview.queue.pending') || 'Pending',  n(queue.pending)],
      [t('internalOverview.queue.deferred') || 'Deferred', n(queue.deferred)],
      [t('internalOverview.queue.failed') || 'Failed',     n(queue.failed)],
    ].forEach(([k, v]) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
    });
    queuePanel.appendChild(dl);
  }

  // Recent audit events
  const audit = Array.isArray(data.recent_audit_events) ? data.recent_audit_events : [];
  const auditPanel = panel(root, {
    title: t('internalOverview.auditTitle') || 'Recent audit events',
    meta:  audit.length ? (audit.length + ' rows') : '',
    body:  '',
  });
  if (audit.length === 0) {
    auditPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalOverview.auditEmpty') || 'No recent audit events.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalOverview.col.timestamp') || 'Timestamp' }),
      el('th', { text: t('internalOverview.col.actor')    || 'Actor' }),
      el('th', { text: t('internalOverview.col.action')   || 'Action' }),
      el('th', { text: t('internalOverview.col.target')   || 'Target' }),
      el('th', { text: t('internalOverview.col.result')   || 'Result' }),
    ])));
    const tb = el('tbody');
    audit.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: fmtShortDate(row.timestamp || '') }),
        el('td', { class: 'kv-v', text: row.actor || '-' }),
        el('td', { class: 'kv-v', text: row.action || '-' }),
        el('td', { class: 'kv-v', text: row.target || '-' }),
        el('td', { class: 'kv-v' },
          badge(row.result || 'unknown', statusKind(row.result || 'unknown'))),
      ]));
    });
    tbl.appendChild(tb);
    auditPanel.appendChild(tbl);
  }

  applyAutoDir(root);
}
