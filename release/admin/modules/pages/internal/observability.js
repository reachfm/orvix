/* =====================================================================
   modules/pages/internal/observability.js — Internal Ops · Observability.

   Aggregated observability panel. Pulls:

     GET /api/v1/monitoring/snapshot      (counters)
     GET /api/v1/monitoring/health        (per-component state)
     GET /api/v1/monitoring/capacity      (capacity snapshot)
     GET /api/v1/admin/runtime            (listener state)

   Also routed for the legacy path 'observability' (sidebar pre-2C)
   so older direct links still resolve.

   Honesty contract:
     - We never fabricate trend lines. The build does not retain
       series; only the literal snapshot is shown.
     - The "missing field" pattern is consistent across panels: a
       panel whose backend slice is empty shows an explicit hint
       rather than a fabricated zero.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, fmtBytes, fmtShortDate, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalObservability.title') || 'Observability',
    subtitle: t('internalObservability.subtitle')
      || 'Per-subsystem counters, health snapshot, capacity, and listener state. The page never invents trend lines — only the literal snapshot.',
  });

  const grid = el('div', { class: 'obs-grid' });
  root.appendChild(grid);

  grid.appendChild(buildCountersCard());
  grid.appendChild(buildHealthCard());
  grid.appendChild(buildCapacityCard());
  grid.appendChild(buildListenerCard());

  applyAutoDir(root);
}

function buildCountersCard() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: t('internalObservability.countersTitle') || 'Counters snapshot' }),
    el('span', { class: 'badge tag', text: 'monitoring.snapshot' }),
  ]));
  const body = el('div', { class: 'panel-body', text: t('common.loading') || 'Loading…' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/snapshot').then((data) => {
    body.innerHTML = '';
    const m = (data && (data.metrics || data.counters || data)) || {};
    const keys = Object.keys(m);
    if (!keys.length) {
      body.appendChild(el('div', { class: 'empty-hint',
        text: t('internalObservability.countersEmpty')
          || 'No counters returned. The observability service is alive but no events have been recorded yet.' }));
      return;
    }
    const dl = el('dl', { class: 'kv kv-grid' });
    keys.sort();
    keys.forEach((k) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: fmtNumber(m[k]) }));
    });
    body.appendChild(dl);
    body.appendChild(el('p', { class: 'subtle ops-honest-note',
      text: t('internalObservability.countersNote')
        || 'Trend lines require series retention that this build does not retain; the table above is the live snapshot.' }));
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: (t('internalObservability.countersError') || 'Snapshot unavailable: ') + ((err && err.message) || err) }));
  });
  return card;
}

function buildHealthCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: t('internalObservability.healthTitle') || 'Subsystem health' })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') || 'Loading…' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/health').then((data) => {
    body.innerHTML = '';
    const subs = (data && (data.checks || data.components || data)) || {};
    if (Array.isArray(subs)) {
      renderHealthList(body, subs);
      return;
    }
    const keys = Object.keys(subs);
    if (keys.length === 0) {
      body.appendChild(el('div', { class: 'empty-hint',
        text: t('internalObservability.healthEmpty') || 'No health data.' }));
      return;
    }
    const ul = el('ul', { class: 'kv-list' });
    keys.forEach((k) => {
      const v = subs[k];
      const state = (typeof v === 'string' ? v : (v.state || v.status || 'unknown')).toLowerCase();
      ul.appendChild(el('li', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: k }),
        el('span', { class: 'kv-v' }, badge(state, statusKind(state))),
        el('span', { class: 'kv-d', text: (typeof v === 'object' && v && v.message) || '' }),
      ]));
    });
    body.appendChild(ul);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: (t('internalObservability.healthError') || 'Health unavailable: ') + ((err && err.message) || err) }));
  });
  return card;
}

function renderHealthList(body, list) {
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty-hint',
      text: t('internalObservability.healthEmpty') || 'No health data.' }));
    return;
  }
  const ul = el('ul', { class: 'kv-list' });
  list.forEach((row) => {
    const state = (row.state || row.status || 'unknown').toLowerCase();
    ul.appendChild(el('li', { class: 'kv-row' }, [
      el('span', { class: 'kv-k', text: row.name || row.id || '-' }),
      el('span', { class: 'kv-v' }, badge(state, statusKind(state))),
      el('span', { class: 'kv-d', text: row.message || '' }),
    ]));
  });
  body.appendChild(ul);
}

function buildCapacityCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: t('internalObservability.capacityTitle') || 'Capacity' })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') || 'Loading…' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/capacity').then((data) => {
    body.innerHTML = '';
    if (!data) {
      body.appendChild(el('div', { class: 'empty-hint',
        text: t('internalObservability.capacityEmpty') || 'No capacity data.' }));
      return;
    }
    const dl = el('dl', { class: 'kv' });
    Object.keys(data).forEach((k) => {
      const v = data[k];
      const cap = typeof v === 'object' && v !== null
        ? (v.disk_bytes != null ? fmtBytes(v.disk_bytes)
          : v.bytes_used != null ? fmtBytes(v.bytes_used)
          : v.queue_count != null ? fmtNumber(v.queue_count) + ' messages'
          : v.queue_size  != null ? fmtBytes(v.queue_size)
          : v.count       != null ? fmtNumber(v.count)
          : v.message_count != null ? fmtNumber(v.message_count) + ' messages'
          : v.mailbox_count  != null ? fmtNumber(v.mailbox_count) + ' mailboxes'
          : Object.keys(v).slice(0, 3).map((kk) => kk + ': ' + String(v[kk])).join(', '))
        : (typeof v === 'number' ? fmtNumber(v) : String(v));
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: cap }));
    });
    body.appendChild(dl);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: (t('internalObservability.capacityError') || 'Capacity unavailable: ') + ((err && err.message) || err) }));
  });
  return card;
}

function buildListenerCard() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: t('internalObservability.listenersTitle') || 'Listeners' }),
    el('span', { class: 'badge tag', text: 'admin.runtime' }),
  ]));
  const body = el('div', { class: 'panel-body', text: t('common.loading') || 'Loading…' });
  card.appendChild(body);
  apiGet('/api/v1/admin/runtime').then((data) => {
    body.innerHTML = '';
    const listeners = Array.isArray(data && data.listeners) ? data.listeners : [];
    if (listeners.length === 0) {
      body.appendChild(el('div', { class: 'empty-hint',
        text: t('internalObservability.listenersEmpty')
          || 'No listener snapshot returned.' }));
      return;
    }
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalObservability.col.protocol') || 'Protocol' }),
      el('th', { text: t('internalObservability.col.port')     || 'Port' }),
      el('th', { text: t('internalObservability.col.state')    || 'State' }),
      el('th', { text: t('internalObservability.col.status')   || 'Health' }),
      el('th', { text: t('internalObservability.col.detail')   || 'Detail' }),
    ])));
    const tb = el('tbody');
    listeners.forEach((l) => {
      const state = (l.state || 'unknown').toLowerCase();
      tb.appendChild(el('tr', { 'data-search': (l.protocol || '').toLowerCase() }, [
        el('td', { class: 'kv-v', text: l.protocol || '-' }),
        el('td', { class: 'kv-v', text: l.port     != null ? fmtNumber(l.port) : '—' }),
        el('td', { class: 'kv-v' }, badge(state, statusKind(state))),
        el('td', { class: 'kv-v' }, badge(l.status || 'unknown', statusKind(l.status || 'unknown'))),
        el('td', { class: 'kv-v', text: l.detail || '—' }),
      ]));
    });
    tbl.appendChild(tb);
    body.appendChild(tbl);
    body.appendChild(el('p', { class: 'subtle',
      text: t('internalObservability.started')
        ? t('internalObservability.started', { when: fmtShortDate((data && data.started_at) || '') })
        : 'Started: ' + fmtShortDate((data && data.started_at) || '') }));
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: (t('internalObservability.listenersError') || 'Runtime telemetry unavailable: ') + ((err && err.message) || err) }));
  });
  return card;
}
