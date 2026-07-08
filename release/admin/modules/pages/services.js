/* =====================================================================
   pages/services.js — Services overview.

   Aggregates /api/v1/admin/runtime (listener state) and renders
   one card per kind. Honest representation: a skipped port is
   shown as a danger badge; we never claim a secure port is
   active when the registry says "skipped".
   ===================================================================== */

import { el, badge } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

const STATE_KIND = {
  active: 'good', skipped: 'bad', failed: 'bad', unknown: 'neutral', degraded: 'warn', starting: 'warn',
};

const SERVICE_LABELS = {
  'smtp': 'SMTP 25 inbound', 'submission': 'SMTP 587 submission', 'smtps': 'SMTPS 465',
  'imap': 'IMAP 143', 'imaps': 'IMAPS 993',
  'pop3': 'POP3 110', 'pop3s': 'POP3S 995',
  'jmap': 'JMAP 8081',
};

export async function renderServicesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Services' }),
      el('p', { class: 'page-subtitle subtle', text: 'Live listener state per protocol. Skipped / failed ports are visible.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'service-grid' });
  wrap.appendChild(grid);
  let data;
  try { data = await apiGet('/api/v1/admin/runtime'); }
  catch (e) { grid.appendChild(el('div', { class: 'error', text: (e && e.message) || 'Could not load runtime.' })); applyAutoDir(wrap); return; }
  const listeners = (data && data.listeners) || (data && data.services ? Object.entries(data.services).map(([k, v]) => ({ kind: k, status: v.status, state: v.state, detail: v.detail, port: v.port })).filter((l) => l.kind !== 'api' && l.kind !== 'database' && l.kind !== 'queue') : []);
  if (!listeners.length) {
    grid.appendChild(el('div', { class: 'empty', text: 'No listener data available.' }));
    applyAutoDir(wrap); return;
  }
  listeners.forEach((l) => {
    const kind = (l.kind || '').toLowerCase();
    const state = (l.state || l.status || 'unknown').toLowerCase();
    const label = SERVICE_LABELS[kind] || kind;
    const card = el('div', { class: 'service-card' }, [
      el('div', { class: 'service-card-head' }, [
        el('span', { class: 'service-card-name', text: label }),
        badge(state, STATE_KIND[state] || 'neutral'),
      ]),
      el('div', { class: 'service-card-body', text: l.detail || 'No detail reported.' }),
      el('div', { class: 'service-card-foot', text: l.port ? 'port ' + l.port : '—' }),
    ]);
    grid.appendChild(card);
  });
  applyAutoDir(wrap);
}
