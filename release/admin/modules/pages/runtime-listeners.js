/* =====================================================================
   pages/runtime-listeners.js — Premium listener telemetry view (v2).

   Each listener is rendered as a health card. The runtime never
   fabricates "online": "skipped" means intentionally not listening
   (no TLS material yet), "failed" means bind error / port-in-use,
   "active" means bind succeeded and the listener is healthy.
   ===================================================================== */

import { el, badge, fmtShortDate, $ } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { setRuntime, getRuntime } from '../state.js';
import { applyAutoDir } from '../rtl.js';
import { i } from '../icons.js';

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

const TLS_PORTS = new Set([465, 587, 993, 995]);

const PROTO_ICON = {
  smtp: 'smtp', smtps: 'smtp', submission: 'smtp',
  imap: 'imap', imaps: 'imap',
  pop3: 'pop3', pop3s: 'pop3',
  jmap: 'mail', webmail: 'mail',
  http: 'globe', https: 'globe',
};

export async function renderRuntimeListenersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });

  // ---------- Page hero ----------
  const hero = el('div', { class: 'ops-hero', id: 'runtime-hero' });
  hero.appendChild(el('div', { class: 'ops-hero-main' }, [
    el('span', { class: 'ops-hero-eyebrow', text: 'Operational telemetry' }),
    el('h2', { class: 'ops-hero-title', text: t('runtime.heading') }),
    el('p', { class: 'ops-hero-sub', text: t('runtime.subtitle') }),
  ]));
  const heroActions = el('div', { class: 'ops-hero-actions' });
  heroActions.appendChild(el('button', {
    class: 'btn ghost', type: 'button',
    onclick: () => renderRuntimeListenersPage(root),
  }, [
    el('span', { html: i('refresh', { size: 14 }) }),
    document.createTextNode(' Refresh'),
  ]));
  hero.appendChild(heroActions);
  wrap.appendChild(hero);

  // ---------- KPI band ----------
  const kpiStrip = el('section', { class: 'dashboard-kpi-row', id: 'runtime-kpi' });
  wrap.appendChild(kpiStrip);

  // ---------- Per-listener health grid ----------
  const head = el('div', { class: 'ops-section-head' });
  head.appendChild(el('h3', { text: 'Listener overview' }));
  head.appendChild(el('span', { class: 'subtle', id: 'runtime-stamp', text: 'Loading…' }));
  wrap.appendChild(head);

  const grid = el('div', { class: 'health-grid', id: 'runtime-grid' });
  wrap.appendChild(grid);

  root.appendChild(wrap);

  let data, err;
  try { data = await apiGet('/api/v1/admin/runtime'); }
  catch (e) { err = e; }
  if (err) {
    grid.appendChild(el('div', { class: 'empty-state', style: 'grid-column: 1 / -1;' }, [
      el('div', { class: 'empty-illustration', text: '!' }),
      el('div', { class: 'empty-title', text: 'Could not load runtime telemetry' }),
      el('div', { class: 'empty-hint', text: (err && err.message) || 'network error' }),
    ]));
    applyAutoDir(wrap);
    return;
  }
  setRuntime(data || {});

  const stamp = $('runtime-stamp');
  if (stamp) stamp.textContent = 'updated ' + fmtShortDate((data && data.collected_at) || new Date().toISOString());

  const listeners = (data && data.listeners) || [];
  paintKpi(kpiStrip, listeners);
  paintGrid(grid, listeners);

  if (!listeners.length) {
    grid.appendChild(el('div', { class: 'empty-state', style: 'grid-column: 1 / -1;' }, [
      el('div', { class: 'empty-illustration', text: '⌁' }),
      el('div', { class: 'empty-title', text: 'No listener telemetry reported' }),
      el('div', { class: 'empty-hint', text: 'Runtime did not declare any listeners in this build. The dashboard cannot show bind state without telemetry.' }),
    ]));
  }

  applyAutoDir(wrap);
}

function paintKpi(host, listeners) {
  host.innerHTML = '';
  const counts = { active: 0, skipped: 0, failed: 0, degraded: 0, starting: 0, unknown: 0 };
  listeners.forEach((l) => {
    const s = (l.state || 'unknown').toLowerCase();
    if (counts[s] == null) counts.unknown += 1;
    else counts[s] += 1;
  });
  host.appendChild(kpiPro('Active', counts.active, 'good', 'monitoring'));
  host.appendChild(kpiPro('Skipped', counts.skipped, counts.skipped > 0 ? 'bad' : 'good', 'pause'));
  host.appendChild(kpiPro('Failed', counts.failed, counts.failed > 0 ? 'bad' : 'good', 'alert'));
  host.appendChild(kpiPro('Degraded', counts.degraded, counts.degraded > 0 ? 'warn' : 'good', 'warn'));
  host.appendChild(kpiPro('Starting', counts.starting, counts.starting > 0 ? 'warn' : 'good', 'refresh'));
  host.appendChild(kpiPro('Total', listeners.length, 'info', 'queue'));
}

function kpiPro(label, value, tone, iconName) {
  const trendText = tone === 'good' ? 'online' : tone === 'bad' ? 'error' : tone === 'warn' ? 'starting' : 'live';
  const arrow = tone === 'good' ? '\u25b2 ' : tone === 'bad' ? '\u25bc ' : '';
  return el('article', { class: 'kpi-pro', 'data-tone': tone }, [
    el('div', { class: 'kpi-pro-label' }, [
      el('span', { html: i(iconName, { size: 12 }) }),
      document.createTextNode(' ' + label),
    ]),
    el('div', { class: 'kpi-pro-value', text: String(value) }),
    el('div', { class: 'kpi-pro-foot' }, [
      el('span', { class: 'stat-card-trend ' + (tone === 'good' ? 'up' : tone === 'bad' ? 'down' : 'flat'),
        text: arrow + trendText }),
    ]),
  ]);
}

function paintGrid(host, listeners) {
  host.innerHTML = '';
  listeners.forEach((l) => {
    const state = (l.state || 'unknown').toLowerCase();
    const kind = STATE_KIND[state] || 'neutral';
    const tls = TLS_PORTS.has(Number(l.port));
    const proto = (l.protocol || l.kind || '').toString().toLowerCase();
    const iconName = PROTO_ICON[proto] || 'cpu';
    const card = el('article', { class: 'health-card', 'data-state': state });
    card.appendChild(el('div', { class: 'health-card-head' }, [
      el('div', { class: 'health-card-name' }, [
        el('span', { class: 'proto-glyph', html: i(iconName, { size: 12 }) }),
        document.createTextNode(' ' + ((l.label || l.kind || l.protocol || 'listener').toString().toUpperCase()) + (l.port ? ' :' + l.port : '') + (tls ? '  ·  TLS' : '')),
      ]),
      el('span', { class: 'health-card-state ' + (kind === 'good' ? 'good' : kind === 'warn' ? 'warn' : kind === 'bad' ? 'bad' : 'muted'),
        text: STATE_LABEL[state] || state }),
    ]));
    const meta = el('div', { class: 'health-card-meta' });
    meta.appendChild(kvRow('Protocol', l.protocol || l.kind || '-'));
    meta.appendChild(kvRow('Bind', l.bind || l.address || (l.host ? (l.host + ':' + (l.port || '-')) : '-')));
    meta.appendChild(kvRow('TLS', tls ? 'yes' : (l.tls ? 'yes' : 'no')));
    if (l.realm) meta.appendChild(kvRow('Realm', l.realm));
    if (l.last_change_at) meta.appendChild(kvRow('Last change', fmtShortDate(l.last_change_at)));
    card.appendChild(meta);

    card.appendChild(el('p', { class: 'subtle', style: 'margin-top: 4px; line-height: 1.4;',
      text: l.detail || STATE_HINT[state] || STATE_HINT.unknown }));
    if (state === 'failed' && (l.error || l.last_error)) {
      card.appendChild(el('p', { class: 'text-bad', style: 'font-size: 12px; margin: 4px 0 0;',
        text: 'Error: ' + (l.error || l.last_error) }));
    }
    host.appendChild(card);
  });
}

function kvRow(k, v) {
  return el('div', { class: 'health-card-meta-row' }, [
    el('span', { class: 'k', text: k }),
    el('span', { class: 'v', text: String(v == null || v === '' ? '-' : v) }),
  ]);
}
