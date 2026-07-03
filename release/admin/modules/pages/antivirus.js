/* =====================================================================
   pages/antivirus.js — Antivirus / AntiSpam status.

   Wires:
     GET /api/v1/admin/security/antivirus  → engine status

   The endpoint probes the configured ClamAV daemon.
   engine_active is true only when the local daemon
   responds with PONG. No scanning is claimed unless
   that probe succeeds.
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
        text: 'Honest status of the local ClamAV daemon + policy / routing / incoming rule engines.' }),
    ]),
  ]));
  root.appendChild(wrap);

  try {
    const r = await apiGet('/api/v1/admin/security/antivirus');
    paintAV(wrap, r);
  } catch (e) {
    wrap.appendChild(el('div', { class: 'error', text: 'Failed to load status: ' + (e.message || e) }));
  }
  applyAutoDir(wrap);
}

function paintAV(wrap, r) {
  const avCard = el('section', { class: 'panel' });
  avCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Antivirus engine (ClamAV)' }),
    el('span', { class: 'badge tag ' + (r.engine_active ? 'good' : 'warn'),
      text: r.engine_active ? 'active' : 'inactive / unknown' }),
  ]));
  const avBody = el('div', { class: 'panel-body' });
  avBody.appendChild(kvTable([
    ['Engine', r.engine],
    ['Configured', r.engine_configured ? 'yes' : 'no'],
    ['Reachable', r.engine_reachable ? 'yes' : 'no'],
    ['Active', r.engine_active ? 'yes' : 'no'],
    ['Host', r.clamav_host || '-'],
    ['Port', r.clamav_port || '-'],
    ['Last probe response', r.clamav_response || '-'],
  ]));
  if (r.engine_active) {
    avBody.appendChild(el('p', { class: 'subtle',
      text: 'Active means the configured ClamAV daemon responded with PONG.' }));
  } else {
    avBody.appendChild(el('div', { class: 'banner banner-warn' },
      el('span', { class: 'banner-text',
        text: 'No active scanning is reported. To activate, run clamav-daemon on the configured host/port.' })));
  }
  avCard.appendChild(avBody);
  wrap.appendChild(avCard);

  const asCard = el('section', { class: 'panel' });
  asCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Anti-spam engine' }),
    el('span', { class: 'badge tag ' + (r.antispam_active ? 'good' : 'warn'),
      text: r.antispam_active ? 'active' : 'not wired' }),
  ]));
  const asBody = el('div', { class: 'panel-body' });
  asBody.appendChild(kvTable([
    ['Engine', r.antispam_engine],
    ['Active', r.antispam_active ? 'yes' : 'no'],
  ]));
  asBody.appendChild(el('p', { class: 'subtle',
    text: 'Rspamd is not wired in this build. Use per-mailbox / per-domain spam controls via webmail settings.' }));
  asCard.appendChild(asBody);
  wrap.appendChild(asCard);

  const roCard = el('section', { class: 'panel' });
  roCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Acceptance & routing engine' }),
    el('span', { class: 'badge tag ' + (r.routing_active ? 'good' : 'warn'),
      text: r.routing_active ? 'active' : 'not wired' }),
  ]));
  const roBody = el('div', { class: 'panel-body' });
  roBody.appendChild(kvTable([
    ['Engine', r.routing_engine],
    ['Active', r.routing_active ? 'yes' : 'no'],
  ]));
  roBody.appendChild(el('p', { class: 'subtle',
    text: 'Rules can be created and audited at /api/v1/admin/acceptance-rules. The runtime walker hooks them up once the engine integration lands.' }));
  roCard.appendChild(roBody);
  wrap.appendChild(roCard);

  const irCard = el('section', { class: 'panel' });
  irCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Incoming message rules engine' }),
    el('span', { class: 'badge tag ' + (r.incoming_msg_active ? 'good' : 'warn'),
      text: r.incoming_msg_active ? 'active' : 'stored only' }),
  ]));
  const irBody = el('div', { class: 'panel-body' });
  irBody.appendChild(kvTable([
    ['Engine', r.incoming_msg_rules],
    ['Active', r.incoming_msg_active ? 'yes' : 'no'],
  ]));
  irBody.appendChild(el('p', { class: 'subtle',
    text: 'Rules can be created and audited at /api/v1/admin/incoming-msg-rules. Per-mailbox webmail rules at /api/v1/webmail/rules are the active per-mailbox filter pipeline in this build.' }));
  irCard.appendChild(irBody);
  wrap.appendChild(irCard);

  const honest = el('div', { class: 'panel' });
  honest.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Honest notes' })));
  const hb = el('div', { class: 'panel-body' });
  (r.honest_notes || []).forEach((n) => hb.appendChild(el('p', { text: '• ' + n })));
  honest.appendChild(hb);
  wrap.appendChild(honest);
}

function kvTable(rows) {
  const tbl = el('table', { class: 'kv-table' });
  rows.forEach(([k, v]) => {
    tbl.appendChild(el('tr', null, [
      el('th', { text: k }),
      el('td', { class: 'kv-v', text: String(v == null ? '-' : v) }),
    ]));
  });
  return tbl;
}
