/* =====================================================================
   pages/audit-log.js — Audit log viewer UI.

   Read-only view of /api/v1/admin/audit-logs. Filterable by
   actor and action. The page surfaces every admin mutation
   the system has recorded; sensitive targets (passwords,
   DKIM private keys, sessions) are NEVER written to the
   audit log and so never appear here.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAuditLogPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Audit log' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Every admin mutation is recorded here.' }),
    ]),
  ]));

  const filters = el('div', { class: 'filter-bar' }, [
    el('label', { class: 'field' }, [
      el('span', { class: 'field-label', text: 'Actor (prefix)' }),
      el('input', { id: 'audit-actor', class: 'input', type: 'text',
        placeholder: 'e.g. admin@' }),
    ]),
    el('label', { class: 'field' }, [
      el('span', { class: 'field-label', text: 'Action (exact)' }),
      el('input', { id: 'audit-action', class: 'input', type: 'text',
        placeholder: 'e.g. account_class.create' }),
    ]),
    el('button', { class: 'btn primary', id: 'audit-apply', text: 'Apply' }),
    el('button', { class: 'btn ghost', id: 'audit-refresh', text: 'Refresh' }),
  ]);
  wrap.appendChild(filters);

  const tbl = el('div', { class: 'panel audit-table' });
  wrap.appendChild(tbl);
  root.appendChild(wrap);

  async function reload() {
    const actor = document.getElementById('audit-actor').value || '';
    const action = document.getElementById('audit-action').value || '';
    const params = new URLSearchParams();
    if (actor) params.set('actor', actor);
    if (action) params.set('action', action);
    try {
      const r = await apiGet('/api/v1/admin/audit-logs?' + params.toString());
      paint(tbl, (r && r.logs) || []);
    } catch (e) {
      toast('Failed to load audit logs: ' + (e.message || e), 'error');
    }
  }

  document.getElementById('audit-apply').addEventListener('click', reload);
  document.getElementById('audit-refresh').addEventListener('click', reload);
  reload();
  applyAutoDir(wrap);
}

function paint(panel, rows) {
  panel.innerHTML = '';
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Recent activity (' + rows.length + ' rows)' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  if (!rows.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No audit entries yet.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Time' }),
    el('th', { text: 'Actor' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Target' }),
    el('th', { text: 'Result' }),
    el('th', { text: 'IP' }),
  ])));
  const tb = el('tbody');
  rows.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: (r.timestamp || '').replace('T', ' ').replace('Z', '') }),
      el('td', { text: r.actor }),
      el('td', { text: r.action }),
      el('td', { text: r.target || '' }),
      el('td', { text: r.result || '' }),
      el('td', { text: r.ip || '' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}