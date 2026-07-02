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

const SERVICES = [
  { id: 'smtp',        label: 'SMTP 25 inbound',  kind: 'smtp' },
  { id: 'smtp-submission', label: 'SMTP 587 submission', kind: 'smtp-submission' },
  { id: 'smtps',       label: 'SMTPS 465',        kind: 'smtps' },
  { id: 'imap',        label: 'IMAP 143',         kind: 'imap' },
  { id: 'imaps',       label: 'IMAPS 993',        kind: 'imaps' },
  { id: 'pop3',        label: 'POP3 110',         kind: 'pop3' },
  { id: 'pop3s',       label: 'POP3S 995',        kind: 'pop3s' },
  { id: 'jmap',        label: 'JMAP 8081',        kind: 'jmap' },
];

export async function renderServicesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
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
  const listeners = (data && data.listeners) || [];
  const byKind = new Map();
  listeners.forEach((l) => byKind.set((l.kind || '').toLowerCase(), l));
  SERVICES.forEach((s) => {
    const l = byKind.get(s.kind) || byKind.get(s.id);
    const state = (l && l.state || 'unknown').toLowerCase();
    const kind = STATE_KIND[state] || 'neutral';
    const card = el('div', { class: 'service-card' }, [
      el('div', { class: 'service-card-head' }, [
        el('span', { class: 'service-card-name', text: s.label }),
        badge(state, kind),
      ]),
      el('div', { class: 'service-card-body', text: l && l.detail ? l.detail : 'No detail reported.' }),
      el('div', { class: 'service-card-foot', text: l && l.port ? 'port ' + l.port : '—' }),
    ]);
    grid.appendChild(card);
  });
  applyAutoDir(wrap);
}
