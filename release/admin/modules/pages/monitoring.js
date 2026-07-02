/* =====================================================================
   pages/monitoring.js — Real monitoring dashboard.

   Wires:
     GET /api/v1/monitoring/health        → subsystem health map
     GET /api/v1/monitoring/alerts        → active alerts
     GET /api/v1/monitoring/capacity      → capacity snapshot
     POST /api/v1/monitoring/alerts/:id/resolve  (CSRF)

   The page never fabricates chart data. If a backend field is
   missing the panel renders an honest empty state.
   ===================================================================== */

import { el, table, badge, fmtShortDate, openModal, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderMonitoringPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('monitoring.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'Live subsystem health and active alerts.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // 2x grid: subsystem health, capacity, alerts table.
  const grid = el('div', { class: 'monitor-grid' });
  wrap.appendChild(grid);

  grid.appendChild(buildHealthCard());
  grid.appendChild(buildCapacityCard());
  wrap.appendChild(buildAlertsCard());

  applyAutoDir(wrap);
}

function buildHealthCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: t('monitoring.health') })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/health').then((data) => {
    body.innerHTML = '';
    const subs = (data && (data.checks || data.components || data)) || {};
    if (Array.isArray(subs)) {
      renderList(body, subs);
      return;
    }
    const keys = Object.keys(subs);
    if (keys.length === 0) {
      body.appendChild(el('div', { class: 'empty', text: t('common.empty') }));
      return;
    }
    const ul = el('ul', { class: 'kv-list' });
    keys.forEach((k) => {
      const v = subs[k];
      const state = (typeof v === 'string' ? v : (v.state || v.status || 'unknown')).toLowerCase();
      const kind = state === 'healthy' || state === 'ok' ? 'good' : (state === 'degraded' || state === 'warn' ? 'warn' : (state === 'failed' || state === 'error' ? 'bad' : 'neutral'));
      ul.appendChild(el('li', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: k }),
        el('span', { class: 'kv-v' }, badge(state, kind)),
        el('span', { class: 'kv-d', text: (typeof v === 'object' && v && v.message) || '' }),
      ]));
    });
    body.appendChild(ul);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load subsystem health.' }));
  });
  return card;
}

function renderList(body, list) {
  const ul = el('ul', { class: 'kv-list' });
  list.forEach((row) => {
    const state = (row.state || row.status || 'unknown').toLowerCase();
    const kind = state === 'healthy' || state === 'ok' ? 'good' : (state === 'degraded' || state === 'warn' ? 'warn' : (state === 'failed' || state === 'error' ? 'bad' : 'neutral'));
    ul.appendChild(el('li', { class: 'kv-row' }, [
      el('span', { class: 'kv-k', text: row.name || row.id || '-' }),
      el('span', { class: 'kv-v' }, badge(state, kind)),
      el('span', { class: 'kv-d', text: row.message || '' }),
    ]));
  });
  body.appendChild(ul);
}

function buildCapacityCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: t('monitoring.capacity') })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/capacity').then((data) => {
    body.innerHTML = '';
    if (!data) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
    const dl = el('dl', { class: 'kv' });
    Object.keys(data).forEach((k) => {
      // Guard: nested objects are rendered with a small formatter
      // so a stray { ... } is never stringified as a generic Object.
      // The static-analysis test (TestAdminNoObjectObjectInMonitoringCapacity)
      // requires both disk data extraction (cap.disk_bytes /
      // cap.bytes_used) and queue data extraction (cap.queue_count /
      // cap.queue_size) so the dashboard surfaces a meaningful
      // number, not a stringified object.
      const cap = data[k];
      const v = typeof cap === 'object' && cap !== null
        ? (cap.disk_bytes != null ? fmtBytes(cap.disk_bytes)
            : cap.bytes_used != null ? fmtBytes(cap.bytes_used)
            : cap.queue_count != null ? String(cap.queue_count) + ' messages'
            : cap.queue_size != null ? fmtBytes(cap.queue_size)
            : Object.keys(cap).slice(0, 3).map((kk) => kk + ': ' + String(cap[kk])).join(', '))
        : String(cap);
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: v }));
    });
    body.appendChild(dl);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load capacity.' }));
  });
  return card;
}

function buildAlertsCard() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: t('monitoring.alerts') })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/alerts').then((data) => {
    body.innerHTML = '';
    const list = (data && (data.alerts || data)) || [];
    if (!list.length) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
    const tbl = table({
      columns: [
        { name: 'sev', label: t('logs.severity'), render: (r) => {
          const sev = (r.severity || 'warn').toLowerCase();
          const k = sev === 'critical' || sev === 'high' || sev === 'error' ? 'bad' : sev === 'warn' ? 'warn' : 'neutral';
          return badge(sev, k);
        } },
        { name: 'msg', label: 'Message', render: (r) => r.message || r.title || r.code || '-' },
        { name: 'sub', label: 'Source',  render: (r) => r.source || r.component || '-' },
        { name: 't',   label: 'Detected', render: (r) => fmtShortDate(r.created_at || r.detected_at || '') },
        { name: 'a',   label: '', cellClass: 'actions', render: (r) => {
          if (!r.id) return null;
          return el('button', { class: 'btn xs ghost', type: 'button', text: t('monitoring.resolve'),
            onclick: () => resolveAlert(r.id) });
        } },
      ],
      rows: list,
    });
    body.appendChild(tbl);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load alerts.' }));
  });
  return card;
}

async function resolveAlert(id) {
  const ok = await confirmDanger({
    title: 'Resolve alert',
    message: 'Mark this alert as resolved?',
    confirmLabel: 'Resolve',
  });
  if (!ok) return;
  try {
    await apiPost('/api/v1/monitoring/alerts/' + encodeURIComponent(id) + '/resolve', {});
    toast('Alert resolved', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (err) {
    toast((err && err.message) || 'Resolve failed', 'error', 6000);
  }
}
