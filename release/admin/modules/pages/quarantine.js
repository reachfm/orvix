/* =====================================================================
   pages/quarantine.js — Quarantine viewer UI.

   Read-only listing of /api/v1/admin/quarantine (held messages)
   plus release / delete actions on each row. Resolution is
   written to coremail_audit and the row's status updates
   immediately.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderQuarantinePage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Quarantine' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Held messages awaiting admin review. Release or delete each row.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('select', { id: 'q-status', class: 'input' }, [
        el('option', { value: 'held', text: 'Held' }),
        el('option', { value: 'released', text: 'Released' }),
        el('option', { value: 'deleted', text: 'Deleted' }),
      ]),
      el('button', { class: 'btn ghost', id: 'q-refresh', text: 'Refresh' }),
    ]),
  ]));

  const tbl = el('div', { class: 'panel' });
  wrap.appendChild(tbl);
  root.appendChild(wrap);

  async function reload() {
    const status = document.getElementById('q-status').value || 'held';
    try {
      const r = await apiGet('/api/v1/admin/quarantine?status=' + encodeURIComponent(status));
      paint(tbl, (r && r.quarantine) || [], status);
    } catch (e) {
      toast('Failed to load quarantine: ' + (e.message || e), 'error');
    }
  }
  document.getElementById('q-refresh').addEventListener('click', reload);
  document.getElementById('q-status').addEventListener('change', reload);
  reload();
  applyAutoDir(wrap);
}

function paint(panel, rows, status) {
  panel.innerHTML = '';
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: status + ' (' + rows.length + ')' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  if (!rows.length) {
    body.appendChild(el('div', { class: 'empty', text: 'Nothing here.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Held at' }),
    el('th', { text: 'Recipient' }),
    el('th', { text: 'Sender' }),
    el('th', { text: 'Subject' }),
    el('th', { text: 'Reason' }),
    el('th', { text: 'Severity' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  rows.forEach((r) => {
    const actions = el('td', { class: 'actions' });
    if (status === 'held') {
      actions.appendChild(el('button', { class: 'btn ghost success',
        'data-q-action': 'release', 'data-id': String(r.id), text: 'Release' }));
      actions.appendChild(el('button', { class: 'btn ghost danger',
        'data-q-action': 'delete', 'data-id': String(r.id), text: 'Delete' }));
    } else if (r.resolved_by) {
      actions.appendChild(el('span', { class: 'subtle', text: 'By ' + r.resolved_by }));
    }
    tb.appendChild(el('tr', null, [
      el('td', { text: (r.created_at || '').replace('T', ' ').replace('Z', '') }),
      el('td', { text: r.recipient }),
      el('td', { text: r.sender }),
      el('td', { text: r.subject || '' }),
      el('td', { text: r.reason || '' }),
      el('td', { text: r.severity || '' }),
      actions,
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  panel.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-q-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-q-action');
    const id = Number(tEl.getAttribute('data-id'));
    if (!confirm('Confirm ' + action + ' for quarantine #' + id + '?')) return;
    try {
      await apiPost('/api/v1/admin/quarantine/' + id + '/resolve', { action });
      toast('Quarantine ' + action + 'd', 'success');
      reload();
    } catch (e) {
      toast('Action failed: ' + (e.message || e), 'error');
    }
  }, { once: true });
}