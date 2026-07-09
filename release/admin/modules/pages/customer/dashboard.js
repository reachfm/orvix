import { pageHeader, panel, kpiRow, emptyState, apiGet, t, el, fmtNumber } from '../saas-shared.js';
import { fmtDate } from '../../components.js';
import { getProfile } from '../../state.js';

export async function render(root) {
  root.innerHTML = '';
  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title: t('customerDashboard.title'),
    subtitle: t('customerDashboard.subtitle'),
  });

  const summaryHost = el('div', { class: 'dashboard-kpi-row' });
  const reportsHost = el('div', { class: 'dashboard-band' });
  const eventsHost  = el('div', { class: 'dashboard-band' });
  root.appendChild(summaryHost);
  root.appendChild(reportsHost);
  root.appendChild(eventsHost);

  // Seed the KPI strip with "Not reported" so the page never renders blank
  // even when the backend has not yet been migrated.
  function placeholders() {
    summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
      el('div', { class: 'kpi-pro-label', text: t('customerDashboard.kpi.domains') }),
      el('div', { class: 'kpi-pro-value', text: '—' }),
    ]));
    summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
      el('div', { class: 'kpi-pro-label', text: t('customerDashboard.kpi.mailboxes') }),
      el('div', { class: 'kpi-pro-value', text: '—' }),
    ]));
    summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
      el('div', { class: 'kpi-pro-label', text: t('customerDashboard.kpi.storage') }),
      el('div', { class: 'kpi-pro-value', text: '—' }),
    ]));
    summaryHost.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
      el('div', { class: 'kpi-pro-label', text: t('customerDashboard.kpi.delivery') }),
      el('div', { class: 'kpi-pro-value', text: '—' }),
    ]));
  }
  placeholders();

  let summary = null;
  let report  = null;
  try { summary = await apiGet('/api/v1/admin/summary'); } catch (_) {}
  try { report  = await apiGet('/api/v1/admin/reports'); } catch (_) {}

  summaryHost.innerHTML = '';
  const kpis = [
    { label: t('customerDashboard.kpi.domains'),   value: summary && summary.domains ? fmtNumber((summary.domains.total || 0)) : '—', tone: 'good' },
    { label: t('customerDashboard.kpi.mailboxes'), value: summary && summary.mailboxes ? fmtNumber((summary.mailboxes.total || 0)) : '—', tone: 'info' },
    { label: t('customerDashboard.kpi.storage'),   value: report ? formatBytes(report.storage_usage_bytes) : '—', tone: 'info' },
    { label: t('customerDashboard.kpi.delivery'),  value: report ? fmtNumber((report.sent_count || 0) + (report.received_count || 0)) : '—', tone: 'good' },
  ];
  kpiRow(summaryHost, kpis);

  const summaryPanel = panel(root, {
    title: t('customerDashboard.summaryTitle'),
    meta: report ? t('customerDashboard.scope', { scope: report.scope && report.scope.tenant_id ? '#' + report.scope.tenant_id : '—' }) : '',
    body: '',
  });
  if (!report) {
    summaryPanel.appendChild(emptyState(document.createDocumentFragment(), {
      title: t('customerDashboard.noData.title'),
      body: t('customerDashboard.noData.body'),
    }));
  } else {
    const stats = el('div', { class: 'dashboard-band' });
    [
      { label: t('customerDashboard.metric.sent'),     value: fmtNumber(report.sent_count || 0),         tone: 'good' },
      { label: t('customerDashboard.metric.received'), value: fmtNumber(report.received_count || 0),     tone: 'good' },
      { label: t('customerDashboard.metric.deferred'), value: fmtNumber(report.deferred_count || 0),     tone: 'warn' },
      { label: t('customerDashboard.metric.rejected'), value: fmtNumber(report.rejected_count || 0),     tone: 'bad'  },
      { label: t('customerDashboard.metric.bounced'),  value: fmtNumber(report.bounced_count || 0),      tone: 'bad'  },
      { label: t('customerDashboard.metric.failed'),   value: fmtNumber(report.login_failures || 0),     tone: 'warn' },
    ].forEach((it) => {
      const card = el('div', { class: 'kpi-pro', 'data-tone': it.tone });
      card.appendChild(el('div', { class: 'kpi-pro-label', text: it.label }));
      card.appendChild(el('div', { class: 'kpi-pro-value', text: it.value }));
      stats.appendChild(card);
    });
    summaryPanel.appendChild(stats);
  }

  const eventsBody = panel(root, { title: t('customerDashboard.recentTitle'), body: '' });
  if (summary && Array.isArray(summary.recent_activity) && summary.recent_activity.length > 0) {
    const list = el('ul', { class: 'dashboard-events' });
    summary.recent_activity.slice(0, 8).forEach((e) => {
      list.appendChild(el('li', {}, [
        el('span', { class: 'dashboard-event-action', text: e.action || '' }),
        el('span', { class: 'dashboard-event-target', text: e.target || '' }),
        el('span', { class: 'dashboard-event-time',   text: e.timestamp ? fmtDate(e.timestamp) : '' }),
      ]));
    });
    eventsBody.appendChild(list);
  } else {
    eventsBody.appendChild(emptyState(document.createDocumentFragment(), {
      title: t('customerDashboard.eventsEmpty'),
      body: t('customerDashboard.eventsHint'),
    }));
  }
}

function formatBytes(n) {
  if (n == null || isNaN(Number(n))) return '—';
  n = Number(n);
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}
