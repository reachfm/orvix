/* =====================================================================
   pages/logs.js — Local service logs / audit logs.

   Wires:
     GET /api/v1/audit/logs?severity=&source=&since=
   ===================================================================== */

import { el, table, fmtShortDate } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderLogsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('logs.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'Audit log entries from the admin subsystem.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Filters
  const filters = el('div', { class: 'table-bar' });
  const sevSel = el('select', null, [
    el('option', { value: '' }, t('logs.severity')),
    el('option', { value: 'info' }, 'info'),
    el('option', { value: 'warn' }, 'warn'),
    el('option', { value: 'error' }, 'error'),
  ]);
  const sourceSel = el('select', null, [
    el('option', { value: '' }, t('logs.source')),
    el('option', { value: 'admin' }, 'admin'),
    el('option', { value: 'api' }, 'api'),
    el('option', { value: 'system' }, 'system'),
  ]);
  const sinceInput = el('input', { type: 'datetime-local' });
  filters.appendChild(sevSel);
  filters.appendChild(sourceSel);
  filters.appendChild(sinceInput);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Audit log' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(filters);
  wrap.appendChild(card);

  async function refresh() {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    const params = new URLSearchParams();
    if (sevSel.value)   params.set('severity', sevSel.value);
    if (sourceSel.value) params.set('source', sourceSel.value);
    if (sinceInput.value) params.set('since', sinceInput.value);
    let data;
    try { data = await apiGet('/api/v1/audit/logs' + (params.toString() ? '?' + params : '')); }
    catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message || 'load failed' })); return; }
    body.innerHTML = '';
    const list = (data && (data.logs || data.entries || data)) || [];
    if (!list.length) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
    body.appendChild(table({
      columns: [
        { name: 'when',  label: 'Time',     render: (r) => fmtShortDate(r.timestamp || r.created_at || r.time || '') },
        { name: 'sev',   label: 'Severity', render: (r) => {
          const s = (r.severity || r.level || 'info').toLowerCase();
          const k = s === 'error' ? 'bad' : s === 'warn' ? 'warn' : 'neutral';
          return el('span', { class: 'badge ' + k, text: s });
        } },
        { name: 'src',   label: 'Source',   render: (r) => r.source || r.component || '-' },
        { name: 'msg',   label: 'Message',  render: (r) => r.message || r.event || r.action || '-' },
        { name: 'actor', label: 'Actor',    render: (r) => r.actor || r.user || r.email || '-' },
      ],
      rows: list,
    }));
    applyAutoDir(body);
  }

  [sevSel, sourceSel].forEach((s) => s.addEventListener('change', refresh));
  sinceInput.addEventListener('change', refresh);
  refresh();
}
