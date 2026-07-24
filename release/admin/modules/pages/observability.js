/* =====================================================================
   pages/observability.js — Operator observability tab.

   Wires:
     GET /api/v1/monitoring/snapshot
     GET /api/v1/monitoring/health
     GET /api/v1/monitoring/capacity
     GET /api/v1/admin/runtime
     GET /api/v1/admin/queue/summary

   Audit doc reference: docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md
     §2.5 rows 21–25
     §3 deliverable B (Observability page)

   Honesty:
     - Every counter renders the literal number returned by the
       /monitoring/snapshot endpoint. We DO NOT invent trend
       lines: history retention is not in scope for this build,
       so trend charts are replaced by the absolute snapshot.
     - Each panel reports a redacted "missing field" hint when its
       backend slice returns no data.
   ===================================================================== */

import { el, badge, fmtBytes, fmtNumber, fmtShortDate } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderObservabilityPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('observability.heading') || 'Observability' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Per-subsystem counters, capacity snapshot, queue health, and protocol listener state.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'obs-grid' });
  wrap.appendChild(grid);

  grid.appendChild(buildSnapshotCard());
  grid.appendChild(buildHealthCard());
  grid.appendChild(buildCapacityCard());
  grid.appendChild(buildListenerCard());

  applyAutoDir(wrap);
}

function buildSnapshotCard() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Counters snapshot' }),
    el('span', { class: 'badge tag', text: 'observability.metrics' }),
  ]));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/snapshot').then((data) => {
    body.innerHTML = '';
    const m = (data && (data.metrics || data.counters || data)) || {};
    const keys = Object.keys(m);
    if (!keys.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No counters returned. The observability service is alive but no events have been recorded yet.' }));
      return;
    }
    const dl = el('dl', { class: 'kv kv-grid' });
    keys.sort();
    keys.forEach((k) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: fmtNumber(m[k]) }));
    });
    body.appendChild(dl);
    // Honest note about history retention.
    body.appendChild(el('p', { class: 'subtle ops-honest-note',
      text: 'Trend lines require series retention that this build does not retain; the table above is the live snapshot.' }));
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Snapshot unavailable: ' + ((err && err.message) || err) }));
  });
  return card;
}

function buildHealthCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Subsystem health' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
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
      body.appendChild(el('div', { class: 'empty', text: 'No health data.' }));
      return;
    }
    const ul = el('ul', { class: 'kv-list' });
    keys.forEach((k) => {
      const v = subs[k];
      const state = (typeof v === 'string' ? v : (v.state || v.status || 'unknown')).toLowerCase();
      const kind = state === 'healthy' || state === 'ok' ? 'good'
                 : state === 'degraded' || state === 'warn' ? 'warn'
                 : (state === 'failed' || state === 'error' ? 'bad' : 'neutral');
      ul.appendChild(el('li', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: k }),
        el('span', { class: 'kv-v' }, badge(state, kind)),
        el('span', { class: 'kv-d', text: (typeof v === 'object' && v && v.message) || '' }),
      ]));
    });
    body.appendChild(ul);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Health unavailable: ' + ((err && err.message) || err) }));
  });
  return card;
}

function renderList(body, list) {
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No health data.' }));
    return;
  }
  const ul = el('ul', { class: 'kv-list' });
  list.forEach((row) => {
    const state = (row.state || row.status || 'unknown').toLowerCase();
    const kind = state === 'healthy' || state === 'ok' ? 'good'
               : state === 'degraded' || state === 'warn' ? 'warn'
               : (state === 'failed' || state === 'error' ? 'bad' : 'neutral');
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
  card.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Capacity' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/capacity').then((data) => {
    body.innerHTML = '';
    if (!data) {
      body.appendChild(el('div', { class: 'empty', text: 'No capacity data.' }));
      return;
    }
    const dl = el('dl', { class: 'kv' });
    Object.keys(data).forEach((k) => {
      const v = data[k];
      const cap = typeof v === 'object' && v !== null
        ? (v.disk_bytes != null ? fmtBytes(v.disk_bytes)
          : v.bytes_used != null ? fmtBytes(v.bytes_used)
          : v.queue_count != null ? String(v.queue_count) + ' messages'
          : v.queue_size != null ? fmtBytes(v.queue_size)
          : v.count != null ? String(v.count)
          : v.message_count != null ? fmtNumber(v.message_count) + ' messages'
          : v.mailbox_count != null ? fmtNumber(v.mailbox_count) + ' mailboxes'
          : Object.keys(v).slice(0, 3).map((kk) => kk + ': ' + String(v[kk])).join(', '))
        : (typeof v === 'number' ? fmtNumber(v) : String(v));
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: cap }));
    });
    body.appendChild(dl);
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Capacity unavailable: ' + ((err && err.message) || err) }));
  });
  return card;
}

function buildListenerCard() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Listeners' }),
    el('span', { class: 'badge tag', text: 'admin.runtime' }),
  ]));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  card.appendChild(body);
  apiGet('/api/v1/admin/runtime').then((data) => {
    body.innerHTML = '';
    const ls = (data && (data.listener_snapshot || data.listenerSnapshot || data.listeners)) || {};
    const keys = Object.keys(ls);
    if (!keys.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No listener snapshot returned. The runtime has not yet wired any listener state.' }));
      return;
    }
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'Listener' }),
      el('th', { text: 'State' }),
      el('th', { text: 'Address' }),
      el('th', { text: 'Detail' }),
    ])));
    const tb = el('tbody');
    keys.forEach((k) => {
      const v = ls[k] || {};
      const state = (v.state || v.status || 'unknown').toLowerCase();
      const kind = state === 'active' ? 'good'
                 : state === 'failed' || state === 'skipped' ? 'bad'
                 : state === 'degraded' || state === 'starting' ? 'warn' : 'neutral';
      tb.appendChild(el('tr', null, [
        el('td', { text: k }),
        el('td', { class: 'kv-v' }, badge(state, kind)),
        el('td', { class: 'kv-v', text: v.address || v.host || '-' }),
        el('td', { class: 'kv-v', text: v.detail || v.reason || '-' }),
      ]));
    });
    tbl.appendChild(tb);
    body.appendChild(tbl);
    body.appendChild(el('p', { class: 'subtle', text: 'Started: ' + fmtShortDate((data && data.started_at) || '') }));
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Runtime telemetry unavailable: ' + ((err && err.message) || err) }));
  });
  return card;
}
