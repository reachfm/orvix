/* =====================================================================
   pages/runtime-listeners.js — Premium listener telemetry view.

   The page renders each listener as its own health-block card so
   the operator can read the bind state, port, and TLS posture
   at a glance. The runtime never fabricates "online": "skipped"
   means intentionally not listening (e.g. no TLS material yet),
   "failed" means bind error / port-in-use, "active" means bind
   succeeded with the listener reporting healthy.
   ===================================================================== */

import { el, badge, fmtShortDate, $ } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { setRuntime, getRuntime } from '../state.js';
import { applyAutoDir } from '../rtl.js';

const STATE_KIND = {
  active:    'good',
  skipped:   'bad',
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

const STATE_HINT = {
  active:   'Real bind succeeded; listener is accepting traffic.',
  skipped:  'Intentionally not listening. Common cause: TLS material or related dependency not yet configured.',
  failed:   'Bind error (port-in-use, missing cert, permission issue). Inspect the listener detail.',
  degraded: 'Bound but impaired (e.g. STARTTLS unavailable). Connections accepted, posture is reduced.',
  unknown:  'Listener state not yet reported by the runtime.',
  starting: 'Listener is initialising; expected to transition to active.',
};

// TLS ports the runtime considers "secure" — used to mark
// listeners with a clear TLS / clear-text visual cue.
const TLS_PORTS = new Set([465, 587, 993, 995]);

export async function renderRuntimeListenersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('runtime.heading') }),
      el('p', { class: 'page-subtitle subtle',
        text: t('runtime.subtitle') }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderRuntimeListenersPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const stamp = el('span', { class: 'subtle', id: 'runtime-stamp', text: 'Loading\u2026' });
  const summaryCard = el('section', { class: 'ops-section' });
  summaryCard.appendChild(el('header', null, [
    el('h3', { text: 'Listener overview' }),
    stamp,
  ]));
  const summaryBody = el('div', { class: 'panel-body', id: 'runtime-summary' });
  summaryCard.appendChild(summaryBody);
  wrap.appendChild(summaryCard);

  const sectionHead = el('div', { class: 'health-section-head', text: 'Per-listener health' });
  wrap.appendChild(sectionHead);

  const gridHost = el('div', { class: 'health-grid', id: 'runtime-grid' });
  wrap.appendChild(gridHost);

  let data, err;
  try { data = await apiGet('/api/v1/admin/runtime'); }
  catch (e) { err = e; }
  if (err) {
    summaryBody.innerHTML = '';
    summaryBody.appendChild(el('div', { class: 'error',
      text: 'Could not load runtime telemetry: ' + (err && err.message || 'network error') }));
    if (stamp.parentNode) stamp.textContent = 'failed to load';
    applyAutoDir(wrap);
    return;
  }
  setRuntime(data || {});
  if (stamp && stamp.parentNode) stamp.textContent = 'updated ' + fmtShortDate((data && data.collected_at) || new Date().toISOString());

  const listeners = (data && data.listeners) || [];
  paintSummary(summaryBody, listeners);
  paintGrid(gridHost, listeners);

  if (!listeners.length) {
    gridHost.appendChild(el('div', { class: 'empty-state-strong', style: 'grid-column: 1 / -1;' }, [
      el('h4', { text: 'No listener telemetry reported' }),
      el('p', { text: 'Runtime did not declare any listeners in this build. The dashboard cannot show bind state without telemetry.' }),
    ]));
  }

  applyAutoDir(wrap);
}

function paintSummary(body, listeners) {
  body.innerHTML = '';
  const counts = { active: 0, skipped: 0, failed: 0, degraded: 0, starting: 0, unknown: 0 };
  listeners.forEach((l) => {
    const s = (l.state || 'unknown').toLowerCase();
    if (counts[s] == null) counts.unknown += 1;
    else counts[s] += 1;
  });

  const kpis = el('div', { class: 'kpi-hero' });
  kpis.appendChild(kpiBox('Active', counts.active, 'good'));
  kpis.appendChild(kpiBox('Skipped', counts.skipped, 'bad'));
  kpis.appendChild(kpiBox('Failed', counts.failed, 'bad'));
  kpis.appendChild(kpiBox('Degraded', counts.degraded, 'warn'));
  kpis.appendChild(kpiBox('Starting', counts.starting, 'warn'));
  body.appendChild(kpis);

  if (listeners.length > 0) {
    const meta = el('div', { class: 'dashboard-band-meta', style: 'margin-top:10px' });
    meta.appendChild(el('span', { class: 'chip', text: listeners.length + ' listeners reported' }));
    if (counts.failed > 0) {
      meta.appendChild(el('span', { class: 'chip', style: 'border-color:var(--bad); color:var(--bad);', text: counts.failed + ' bind failure(s) \u2014 see detail' }));
    }
    body.appendChild(meta);
  }
}

function kpiBox(label, value, kind) {
  return el('article', { class: 'kpi', 'data-trend': kind }, [
    el('div', { class: 'kpi-head' }, [
      el('span', { class: 'kpi-label', text: label }),
      el('span', { class: 'kpi-trend ' + (kind === 'good' ? 'up' : kind === 'bad' ? 'down' : 'flat'),
        text: kind === 'good' ? '\u25b2 online' : kind === 'bad' ? '\u25bc error' : kind === 'warn' ? '\u2026 starting' : '\u2014' }),
    ]),
    el('div', { class: 'kpi-value', text: String(value) }),
  ]);
}

function paintGrid(host, listeners) {
  host.innerHTML = '';
  listeners.forEach((l) => {
    const state = (l.state || 'unknown').toLowerCase();
    const kind = STATE_KIND[state] || 'neutral';
    const tls = TLS_PORTS.has(Number(l.port));
    const block = el('article', { class: 'health-block', 'data-state': state });
    block.appendChild(el('header', { class: 'health-head' }, [
      el('h4', { class: 'health-title', text: ((l.label || l.kind || l.protocol || 'listener').toString().toUpperCase()) + (l.port ? ' :' + l.port : '') }),
      el('span', { class: 'health-state ' + kind, text: STATE_LABEL[state] || state }),
    ]));
    const dl = el('dl', { class: 'health-kv' });
    kv(dl, 'Protocol',  l.protocol || l.kind || '-');
    kv(dl, 'Bind addr', l.bind || l.address || (l.host ? (l.host + ':' + (l.port || '-')) : '-'));
    kv(dl, 'TLS',       tls ? 'yes' : (l.tls ? 'yes' : 'no'));
    if (l.realm) kv(dl, 'Realm',  l.realm);
    if (l.last_change_at) kv(dl, 'Last change', fmtShortDate(l.last_change_at));
    block.appendChild(dl);

    if (l.detail) {
      block.appendChild(el('p', { class: 'health-note', text: l.detail }));
    } else {
      block.appendChild(el('p', { class: 'health-note', text: STATE_HINT[state] || STATE_HINT.unknown }));
    }
    if (state === 'failed' && (l.error || l.last_error)) {
      block.appendChild(el('p', { class: 'health-note', style: 'color: var(--bad);',
        text: 'Error: ' + (l.error || l.last_error) }));
    }
    host.appendChild(block);
  });
}

function kv(dl, k, v) {
  dl.appendChild(el('dt', { class: 'k', text: k }));
  dl.appendChild(el('dd', { class: 'v', text: String(v == null || v === '' ? '-' : v) }));
}
