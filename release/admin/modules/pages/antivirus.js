/* =====================================================================
   pages/antivirus.js — Antivirus / AntiSpam / Rule engine status (v2).

   Premium health-card presentation. The ClamAV probe reports
   engine_active=true ONLY when the daemon responds with PONG;
   no scanning is claimed unless the probe actually ran. Anti-spam,
   acceptance/routing, and incoming rule engines are honest about
   their active state.
   ===================================================================== */

import { el, fmtShortDate } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';
import { i } from '../icons.js';

export async function renderAntivirusPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  root.appendChild(wrap);

  // ---------- Page hero ----------
  const hero = el('div', { class: 'ops-hero', 'data-tone': 'good' });
  hero.appendChild(el('div', { class: 'ops-hero-main' }, [
    el('span', { class: 'ops-hero-eyebrow', text: 'Security engines' }),
    el('h2', { class: 'ops-hero-title', text: 'Antivirus / anti-spam' }),
    el('p', { class: 'ops-hero-sub',
      text: 'Honest status of the local ClamAV daemon + the anti-spam, acceptance/routing, and incoming message rule engines. No engine is reported as active unless its presence probe succeeds.' }),
  ]));
  const heroActions = el('div', { class: 'ops-hero-actions' });
  heroActions.appendChild(el('button', {
    class: 'btn ghost', type: 'button',
    onclick: () => renderAntivirusPage(root),
  }, [
    el('span', { html: i('refresh', { size: 14 }) }),
    document.createTextNode(' Refresh'),
  ]));
  hero.appendChild(heroActions);
  wrap.appendChild(hero);

  let r = null, err = null;
  try { r = await apiGet('/api/v1/admin/security/antivirus'); }
  catch (e) { err = e; }

  if (err && !r) {
    wrap.appendChild(el('div', { class: 'empty-state' }, [
      el('div', { class: 'empty-illustration', text: '!' }),
      el('div', { class: 'empty-title', text: 'Failed to load antivirus status' }),
      el('div', { class: 'empty-hint', text: (err && err.message) || 'network error' }),
    ]));
    applyAutoDir(wrap);
    return;
  }

  // ---------- KPI strip ----------
  const kpi = el('section', { class: 'dashboard-kpi-row' });
  kpi.appendChild(kpiPro('Antivirus engine',
    r.engine_active ? 'active' : (r.engine_configured ? 'configured' : 'not configured'),
    r.engine_active ? 'good' : (r.engine_configured ? 'warn' : 'neutral'),
    'shield'));
  kpi.appendChild(kpiPro('Last probe',
    r.last_probe_at ? fmtShortDate(r.last_probe_at) : 'never',
    r.last_probe_at ? 'info' : 'neutral', 'refresh'));
  kpi.appendChild(kpiPro('Anti-spam',
    r.antispam_active ? 'active' : 'not wired',
    r.antispam_active ? 'good' : 'neutral', 'spam'));
  kpi.appendChild(kpiPro('Routing engine',
    r.routing_active ? 'active' : 'rules stored',
    r.routing_active ? 'good' : 'warn', 'routing'));
  kpi.appendChild(kpiPro('Incoming rules',
    r.incoming_msg_active ? 'active' : 'rules stored',
    r.incoming_msg_active ? 'good' : 'warn', 'rules'));
  wrap.appendChild(kpi);

  // ---------- Section head ----------
  wrap.appendChild(sectionHead('Engines', 'Per-engine health blocks'));

  // ---------- Health grid ----------
  const grid = el('div', { class: 'health-grid' });
  grid.appendChild(engineCard('Antivirus engine (ClamAV)', {
    state: r.engine_active ? 'active' : (r.engine_configured ? 'degraded' : 'unknown'),
    stateLabel: r.engine_active ? 'active' : (r.engine_configured ? 'configured' : 'not configured'),
    icon: 'shield',
    meta: [
      ['Engine', r.engine || '-'],
      ['Configured', r.engine_configured ? 'yes' : 'no'],
      ['Reachable', r.engine_reachable ? 'yes' : 'no'],
      ['Active', r.engine_active ? 'yes' : 'no'],
      ['Host', r.clamav_host || '-'],
      ['Port', r.clamav_port || '-'],
      ['Last probe response', r.clamav_response || '-'],
      ['Last probe at', r.last_probe_at ? fmtShortDate(r.last_probe_at) : '-'],
    ],
    note: r.engine_active
      ? 'Active means the configured ClamAV daemon responded with PONG at the last probe.'
      : 'No active scanning is reported. To activate, run clamav-daemon on the configured host/port.',
  }));
  grid.appendChild(engineCard('Anti-spam engine', {
    state: r.antispam_active ? 'active' : 'unknown',
    stateLabel: r.antispam_active ? 'active' : 'not wired',
    icon: 'spam',
    meta: [
      ['Engine', r.antispam_engine || '-'],
      ['Active', r.antispam_active ? 'yes' : 'no'],
      ['Reachability', r.antispam_reachable ? 'yes' : 'no'],
      ['Last probe', r.antispam_response || '-'],
    ],
    note: 'Rspamd / SpamAssassin is not wired in this build. Use per-mailbox or per-domain spam controls via webmail Settings → Filters.',
  }));
  grid.appendChild(engineCard('Acceptance & routing engine', {
    state: r.routing_active ? 'active' : 'degraded',
    stateLabel: r.routing_active ? 'active' : 'rules stored',
    icon: 'routing',
    meta: [
      ['Engine', r.routing_engine || '-'],
      ['Active', r.routing_active ? 'yes' : 'no'],
    ],
    note: 'Rules can be created + audited at Acceptance & routing. The runtime walker hooks them up once the engine integration lands.',
  }));
  grid.appendChild(engineCard('Incoming message rules engine', {
    state: r.incoming_msg_active ? 'active' : 'degraded',
    stateLabel: r.incoming_msg_active ? 'active' : 'rules stored',
    icon: 'rules',
    meta: [
      ['Engine', r.incoming_msg_rules || '-'],
      ['Active', r.incoming_msg_active ? 'yes' : 'no'],
    ],
    note: 'Rules can be created + audited at Incoming message rules. Per-mailbox webmail rules are the active per-mailbox filter pipeline in this build.',
  }));
  wrap.appendChild(grid);

  // ---------- Honest notes ----------
  if (Array.isArray(r.honest_notes) && r.honest_notes.length) {
    wrap.appendChild(sectionHead('Honest notes', 'Documented limitations in this build'));
    const noteBlock = el('div', { class: 'panel' });
    noteBlock.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: 'Limitations' }),
    ]));
    const nb = el('div', { class: 'panel-body' });
    r.honest_notes.forEach((n) => nb.appendChild(el('p', { class: 'subtle', style: 'margin: 6px 0;', text: '\u2022 ' + n })));
    noteBlock.appendChild(nb);
    wrap.appendChild(noteBlock);
  }

  applyAutoDir(wrap);
}

function kpiPro(label, value, tone, iconName) {
  return el('article', { class: 'kpi-pro', 'data-tone': tone }, [
    el('div', { class: 'kpi-pro-label' }, [
      el('span', { html: i(iconName, { size: 12 }) }),
      document.createTextNode(' ' + label),
    ]),
    el('div', { class: 'kpi-pro-value is-text', text: value }),
    el('div', { class: 'kpi-pro-foot' }, [
      el('span', { class: 'subtle', text: tone === 'good' ? '\u25b2 healthy' : tone === 'warn' ? '\u2026 degraded' : tone === 'bad' ? '\u25bc error' : '\u2014 n/a' }),
    ]),
  ]);
}

function sectionHead(title, sub) {
  const h = el('div', { class: 'ops-section-head' });
  h.appendChild(el('h3', { text: title }));
  if (sub) h.appendChild(el('span', { class: 'subtle', text: sub }));
  return h;
}

function engineCard(title, opts) {
  const card = el('article', { class: 'health-card', 'data-state': opts.state });
  card.appendChild(el('div', { class: 'health-card-head' }, [
    el('div', { class: 'health-card-name' }, [
      el('span', { class: 'proto-glyph', html: i(opts.icon, { size: 12 }) }),
      document.createTextNode(' ' + title),
    ]),
    el('span', { class: 'health-card-state ' + (opts.state === 'active' ? 'good' : opts.state === 'degraded' ? 'warn' : opts.state === 'failed' ? 'bad' : 'muted'),
      text: opts.stateLabel }),
  ]));
  const meta = el('div', { class: 'health-card-meta' });
  (opts.meta || []).forEach(([k, v]) => {
    meta.appendChild(el('div', { class: 'health-card-meta-row' }, [
      el('span', { class: 'k', text: k }),
      el('span', { class: 'v', text: String(v == null || v === '' ? '-' : v) }),
    ]));
  });
  card.appendChild(meta);
  if (opts.note) card.appendChild(el('p', { class: 'subtle', style: 'margin: 4px 0 0; line-height: 1.4;', text: opts.note }));
  return card;
}
