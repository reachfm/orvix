/* =====================================================================
   pages/antivirus.js — Antivirus / AntiSpam / Rule engine status.

   Premium 2-column health-block presentation. The ClamAV probe
   reports engine_active=true ONLY when the daemon responds with
   PONG; no scanning is claimed unless the probe actually ran.
   Antispam / routing / incoming rule engines are honest about
   their active state instead of synthesising "OK" badges.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAntivirusPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Antivirus / anti-spam' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Honest status of the local ClamAV daemon + the anti-spam, acceptance / routing, and incoming message rule engines. No engine is reported as active unless its presence probe succeeds.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderAntivirusPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  let r = null, err = null;
  try { r = await apiGet('/api/v1/admin/security/antivirus'); }
  catch (e) { err = e; }

  if (err && !r) {
    wrap.appendChild(el('div', { class: 'error',
      text: 'Failed to load antivirus status: ' + (err && err.message || 'network error') }));
    applyAutoDir(wrap);
    return;
  }

  wrap.appendChild(renderKpis(r || {}));
  wrap.appendChild(renderSection('Antivirus engine (ClamAV)', [
    ['Engine',                r.engine || '-'],
    ['Configured',            r.engine_configured ? 'yes' : 'no'],
    ['Reachable',             r.engine_reachable ? 'yes' : 'no'],
    ['Active',                r.engine_active ? 'yes' : 'no'],
    ['Host',                  r.clamav_host || '-'],
    ['Port',                  r.clamav_port || '-'],
    ['Last probe response',   r.clamav_response || '-'],
    ['Last probe at',         r.last_probe_at || '-'],
  ], {
    state: r.engine_active ? 'good' : (r.engine_configured ? 'warn' : 'neutral'),
    stateLabel: r.engine_active ? 'active' : (r.engine_configured ? 'configured / not active' : 'not configured'),
    note: r.engine_active
      ? 'Active means the configured ClamAV daemon responded with PONG at the last probe.'
      : 'No active scanning is reported. To activate, run clamav-daemon on the configured host/port.',
  }));

  wrap.appendChild(renderSection('Anti-spam engine', [
    ['Engine',           r.antispam_engine || '-'],
    ['Active',           r.antispam_active ? 'yes' : 'no'],
    ['Reachability',     r.antispam_reachable ? 'yes' : 'no'],
    ['Last probe',       r.antispam_response || '-'],
  ], {
    state: r.antispam_active ? 'good' : 'neutral',
    stateLabel: r.antispam_active ? 'active' : 'not wired',
    note: 'Rspamd / SpamAssassin is not wired in this build. Use per-mailbox or per-domain spam controls via webmail Settings \u2192 Filters.',
  }));

  wrap.appendChild(renderSection('Acceptance & routing engine', [
    ['Engine',           r.routing_engine || '-'],
    ['Active',           r.routing_active ? 'yes' : 'no'],
  ], {
    state: r.routing_active ? 'good' : 'neutral',
    stateLabel: r.routing_active ? 'active' : 'rules stored; walker on next integration',
    note: 'Rules can be created + audited at Acceptance & routing. The runtime walker hooks them up once the engine integration lands.',
  }));

  wrap.appendChild(renderSection('Incoming message rules engine', [
    ['Engine',           r.incoming_msg_rules || '-'],
    ['Active',           r.incoming_msg_active ? 'yes' : 'no'],
  ], {
    state: r.incoming_msg_active ? 'good' : 'neutral',
    stateLabel: r.incoming_msg_active ? 'active' : 'rules stored',
    note: 'Rules can be created + audited at Incoming message rules. Per-mailbox webmail rules are the active per-mailbox filter pipeline in this build.',
  }));

  // Honest notes
  if (Array.isArray(r.honest_notes) && r.honest_notes.length) {
    const noteBlock = el('section', { class: 'ops-section' });
    noteBlock.appendChild(el('header', null, el('h3', { text: 'Honest notes' })));
    const nb = el('div', { class: 'panel-body' });
    r.honest_notes.forEach((n) => nb.appendChild(el('p', { class: 'health-note', text: '\u2022 ' + n })));
    noteBlock.appendChild(nb);
    wrap.appendChild(noteBlock);
  }

  applyAutoDir(wrap);
}

function renderKpis(r) {
  const k = el('div', { class: 'kpi-hero' });
  k.appendChild(kpi('Antivirus engine',
    r.engine_active ? 'active' : (r.engine_configured ? 'configured' : 'not configured'),
    r.engine_active ? 'good' : (r.engine_configured ? 'warn' : 'neutral')));
  k.appendChild(kpi('Antivirus probe',
    r.last_probe_at ? formatTs(r.last_probe_at) : 'never', 'neutral'));
  k.appendChild(kpi('Anti-spam',
    r.antispam_active ? 'active' : 'not wired',
    r.antispam_active ? 'good' : 'neutral'));
  k.appendChild(kpi('Routing engine',
    r.routing_active ? 'active' : 'rules stored',
    r.routing_active ? 'good' : 'warn'));
  return k;
}

function kpi(label, value, kind) {
  return el('article', { class: 'kpi', 'data-trend': kind }, [
    el('div', { class: 'kpi-head' }, [el('span', { class: 'kpi-label', text: label })]),
    el('div', { class: 'kpi-value', text: value }),
  ]);
}

function renderSection(title, rows, opts) {
  opts = opts || {};
  const sec = el('section', { class: 'ops-section' });
  sec.appendChild(el('header', null, [
    el('h3', { text: title }),
    el('span', { class: 'health-state ' + (opts.state || 'neutral'), text: opts.stateLabel || '\u2014' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'health-kv' });
  rows.forEach(([k, v]) => {
    dl.appendChild(el('dt', { class: 'k', text: k }));
    dl.appendChild(el('dd', { class: 'v', text: String(v == null || v === '' ? '-' : v) }));
  });
  body.appendChild(dl);
  if (opts.note) body.appendChild(el('p', { class: 'health-note', text: opts.note }));
  sec.appendChild(body);
  return sec;
}

function formatTs(s) {
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return String(s);
    return d.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  } catch (_) { return String(s); }
}
