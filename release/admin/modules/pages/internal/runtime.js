/* =====================================================================
   modules/pages/internal/runtime.js — Internal Ops · Runtime listeners.

   Source: GET /api/v1/admin/runtime (handler GetAdminRuntime).
   The handler returns a runtime.Telemetry JSON object with:

     { status, version, commit, build_time, hostname, started_at,
       uptime_seconds, services: {smtp|imap|pop3|jmap: {...}},
       listeners: [{protocol, kind, port, status, state, detail}],
       capacity: { disk: {label, total_bytes, used_bytes, free_bytes, used_percent} },
       queue:    {pending, deferred, bounced, delivered},
       license:  {mode, public_key_state, validation_state, status, tier, expires_at},
       warnings: [{level, code, message}]
     }

   This page is also routed for the legacy path 'runtime-listeners'
   so that older external links still resolve.  Both routes point
   at the same handler with identical UX.

   Honesty contract:
     - Per-listener `state` is shown verbatim. "active" means a
       real bind succeeded; "skipped" means intentionally not
       listening (TLS material missing, etc.); "failed" means
       bind error.
     - Uptime, started_at and disk values come from the server.
       We never recompute uptime client-side.
   ===================================================================== */

import { pageHeader, kpiRow, panel, apiGet, fmtBytes, fmtShortDate, fmtNumber, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

const STATE_KIND = {
  active:    'good',
  skipped:   'neutral', // muted: an intentional skip is not an outage
  failed:    'bad',
  unknown:   'neutral',
  degraded:  'warn',
  starting:  'warn',
};
const STATE_LABEL = {
  active:    'active',
  skipped:   'skipped',
  failed:    'failed',
  unknown:   'not monitored',
  degraded:  'degraded',
  starting:  'starting',
};

function fmtUptime(seconds) {
  if (seconds == null || !Number.isFinite(Number(seconds))) return '—';
  let s = Number(seconds);
  if (s < 0) return '—';
  const d = Math.floor(s / 86400); s -= d * 86400;
  const h = Math.floor(s / 3600);  s -= h * 3600;
  const m = Math.floor(s / 60);
  if (d > 0) return d + 'd ' + h + 'h';
  if (h > 0) return h + 'h ' + m + 'm';
  if (m > 0) return m + 'm';
  return 'just started';
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalRuntime.title') || 'Runtime listeners',
    subtitle: t('internalRuntime.subtitle')
      || 'Live bind state for every configured listener, taken straight from /api/v1/admin/runtime.',
  });

  const summary = el('div', { class: 'dashboard-kpi-row' });
  root.appendChild(summary);
  function placeholders() {
    summary.innerHTML = '';
    [t('internalRuntime.kpi.active')    || 'Active',
     t('internalRuntime.kpi.skipped')   || 'Skipped',
     t('internalRuntime.kpi.failed')    || 'Failed',
     t('internalRuntime.kpi.degraded')  || 'Degraded',
     t('internalRuntime.kpi.unknown')   || 'Unknown',
     t('internalRuntime.kpi.total')     || 'Total'].forEach((label) => {
      summary.appendChild(el('div', { class: 'kpi-pro', 'data-tone': 'info' }, [
        el('div', { class: 'kpi-pro-label', text: label }),
        el('div', { class: 'kpi-pro-value', text: '—' }),
      ]));
    });
  }
  placeholders();

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/admin/runtime'); }
  catch (err) { loadErr = err; }

  if (loadErr) {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'error',
      text: (t('internalRuntime.error') || 'Runtime telemetry lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    applyAutoDir(root);
    return;
  }
  if (!data || typeof data !== 'object') {
    summary.innerHTML = '';
    root.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'R' }),
      el('div', { class: 'empty-title', text: t('internalRuntime.emptyTitle') || 'No runtime telemetry' }),
      el('div', { class: 'empty-hint',
        text: t('internalRuntime.emptyBody')
          || 'The runtime endpoint returned no payload.' }),
    ]));
    applyAutoDir(root);
    return;
  }

  const listeners = Array.isArray(data.listeners) ? data.listeners : [];
  const counts = { active: 0, skipped: 0, failed: 0, degraded: 0, starting: 0, unknown: 0 };
  listeners.forEach((l) => {
    const s = (l.state || 'unknown').toLowerCase();
    if (counts[s] != null) counts[s] += 1; else counts.unknown += 1;
  });
  summary.innerHTML = '';
  kpiRow(summary, [
    { label: t('internalRuntime.kpi.active')    || 'Active',    value: fmtNumber(counts.active),    tone: 'good' },
    { label: t('internalRuntime.kpi.skipped')   || 'Skipped',   value: fmtNumber(counts.skipped),   tone: counts.skipped > 0 ? 'warn' : 'good' },
    { label: t('internalRuntime.kpi.failed')    || 'Failed',    value: fmtNumber(counts.failed),    tone: counts.failed  > 0 ? 'bad'  : 'good' },
    { label: t('internalRuntime.kpi.degraded')  || 'Degraded',  value: fmtNumber(counts.degraded),  tone: counts.degraded > 0 ? 'warn' : 'good' },
    { label: t('internalRuntime.kpi.unknown')   || 'Unknown',   value: fmtNumber(counts.unknown),   tone: 'info' },
    { label: t('internalRuntime.kpi.total')     || 'Total',     value: fmtNumber(listeners.length), tone: 'info' },
  ]);

  // Identity strip
  const identity = el('div', { class: 'ops-grid runtime-identity' });
  root.appendChild(identity);

  const summaryPanel = panel(identity, {
    title: t('internalRuntime.identityTitle') || 'Runtime',
    body:  '',
  });
  const dl = el('dl', { class: 'kv' });
  [
    [t('internalRuntime.col.hostname') || 'Hostname',       data.hostname || '—'],
    [t('internalRuntime.col.version')  || 'Version',        data.version || 'unknown'],
    [t('internalRuntime.col.commit')   || 'Commit',         data.commit || 'not reported'],
    [t('internalRuntime.col.started')  || 'Started',        data.started_at ? fmtShortDate(data.started_at) : '—'],
    [t('internalRuntime.col.uptime')   || 'Uptime',         fmtUptime(data.uptime_seconds)],
    [t('internalRuntime.col.status')   || 'Status',         data.status || 'unknown'],
  ].forEach(([k, v]) => {
    dl.appendChild(el('dt', { text: k }));
    dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
  });
  summaryPanel.appendChild(dl);

  // Capacity (disk)
  const capPanel = panel(identity, { title: t('internalRuntime.capacityTitle') || 'Capacity' });
  const disk = (data.capacity && data.capacity.disk) || {};
  if (disk && (disk.total_bytes || disk.used_bytes)) {
    const capDl = el('dl', { class: 'kv' });
    [
      [t('internalRuntime.col.diskLabel') || 'Path',      disk.label || '—'],
      [t('internalRuntime.col.diskUsed')  || 'Used',      disk.used_bytes  != null ? fmtBytes(disk.used_bytes)  : '—'],
      [t('internalRuntime.col.diskFree')  || 'Free',      disk.free_bytes  != null ? fmtBytes(disk.free_bytes)  : '—'],
      [t('internalRuntime.col.diskTotal') || 'Total',     disk.total_bytes != null ? fmtBytes(disk.total_bytes) : '—'],
      [t('internalRuntime.col.diskPct')   || 'Used %',    disk.used_percent != null ? fmtNumber(disk.used_percent) + ' %' : '—'],
    ].forEach(([k, v]) => {
      capDl.appendChild(el('dt', { text: k }));
      capDl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
    });
    capPanel.appendChild(capDl);
  } else {
    capPanel.appendChild(el('div', { class: 'empty-hint',
      text: t('internalRuntime.capacityEmpty') || 'Capacity telemetry not yet wired on this build.' }));
  }

  // Queue + license at the same level
  const aux = el('div', { class: 'ops-grid runtime-aux' });
  root.appendChild(aux);
  const queuePanel = panel(aux, { title: t('internalRuntime.queueTitle') || 'Queue' });
  const q = data.queue || {};
  const qDl = el('dl', { class: 'kv' });
  [
    ['Pending',   q.pending   != null ? fmtNumber(q.pending)   : '—'],
    ['Deferred',  q.deferred  != null ? fmtNumber(q.deferred)  : '—'],
    ['Bounced',   q.bounced   != null ? fmtNumber(q.bounced)   : '—'],
    ['Delivered', q.delivered != null ? fmtNumber(q.delivered) : '—'],
  ].forEach(([k, v]) => {
    qDl.appendChild(el('dt', { text: k }));
    qDl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
  });
  queuePanel.appendChild(qDl);

  const license = data.license || {};
  const licPanel = panel(aux, { title: t('internalRuntime.licenseTitle') || 'License' });
  const licDl = el('dl', { class: 'kv' });
  [
    ['Mode',              license.mode || '—'],
    ['Public key',        license.public_key_state || '—'],
    ['Validation',        license.validation_state || '—'],
    ['Status',            license.status || '—'],
    ['Tier',              license.tier || '—'],
    ['Expires',           license.expires_at ? fmtShortDate(license.expires_at) : '—'],
  ].forEach(([k, v]) => {
    licDl.appendChild(el('dt', { text: k }));
    licDl.appendChild(el('dd', { class: 'kv-v',
      text: String(v) }));
  });
  licPanel.appendChild(licDl);

  // Listeners table
  const listPanel = panel(root, {
    title: t('internalRuntime.listenersTitle') || 'Listeners',
    meta:  listeners.length ? (listeners.length + ' rows') : '',
    body:  '',
  });
  if (listeners.length === 0) {
    listPanel.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: '⌁' }),
      el('div', { class: 'empty-title', text: t('internalRuntime.listenersEmpty') || 'No listener telemetry reported' }),
      el('div', { class: 'empty-hint',
        text: t('internalRuntime.listenersHint')
          || 'The runtime has not wired any listener state in this build.' }),
    ]));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('internalRuntime.col.protocol') || 'Protocol' }),
      el('th', { text: t('internalRuntime.col.kind')     || 'Kind' }),
      el('th', { text: t('internalRuntime.col.port')     || 'Port' }),
      el('th', { text: t('internalRuntime.col.state')    || 'State' }),
      el('th', { text: t('internalRuntime.col.status')   || 'Health' }),
      el('th', { text: t('internalRuntime.col.detail')   || 'Detail' }),
    ])));
    const tb = el('tbody');
    listeners.forEach((l) => {
      const state = (l.state || 'unknown').toLowerCase();
      const kind = STATE_KIND[state] || 'neutral';
      tb.appendChild(el('tr', { 'data-search': ((l.protocol || '') + ' ' + (l.kind || '')).toLowerCase() }, [
        el('td', { class: 'kv-v', text: l.protocol || '-' }),
        el('td', { class: 'kv-v', text: l.kind     || '-' }),
        el('td', { class: 'kv-v', text: l.port     != null ? fmtNumber(l.port) : '—' }),
        el('td', { class: 'kv-v' }, badge(STATE_LABEL[state] || state, kind)),
        el('td', { class: 'kv-v' }, badge(l.status || 'unknown', statusKind(l.status || 'unknown'))),
        el('td', { class: 'kv-v', text: l.detail || '—' }),
      ]));
    });
    tbl.appendChild(tb);
    listPanel.appendChild(tbl);
  }

  // Warnings
  const warnings = Array.isArray(data.warnings) ? data.warnings : [];
  if (warnings.length > 0) {
    const warnPanel = panel(root, { title: t('internalRuntime.warningsTitle') || 'Warnings' });
    const ul = el('ul', { class: 'kv-list' });
    warnings.forEach((w) => {
      const level = (w.level || '').toLowerCase();
      const kind = level === 'error' ? 'bad' : level === 'warn' ? 'warn' : 'neutral';
      ul.appendChild(el('li', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: w.code || '-' }),
        el('span', { class: 'kv-v' }, badge(w.level || 'info', kind)),
        el('span', { class: 'kv-d', text: w.message || '' }),
      ]));
    });
    warnPanel.appendChild(ul);
  }

  applyAutoDir(root);
}
