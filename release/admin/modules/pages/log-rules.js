/* =====================================================================
   pages/log-rules.js — Log collection rules UI.

   CRUD against /api/v1/admin/log-rules. Drives the local
   log collector that ships matching lines to a destination
   (file, syslog, HTTP). Read-only listing on /api/v1/logs/
   files / server belongs to logs.js.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderLogRulesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Log collection rules' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Define which log lines get shipped, where, and at what severity.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-lr-action': 'create',
        text: 'Add rule' }),
    ]),
  ]));
  const tbl = el('div', { class: 'panel' });
  wrap.appendChild(tbl);
  root.appendChild(wrap);

  let rules = [];
  try {
    const r = await apiGet('/api/v1/admin/log-rules');
    rules = (r && r.rules) || [];
  } catch (e) {
    toast('Failed to load log rules: ' + (e.message || e), 'error');
  }
  paint(tbl, rules);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-lr-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-lr-action');
    if (action === 'create') return openCreate(tbl, rules);
    if (action === 'delete') return doDelete(tbl, rules, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(panel, list) {
  panel.innerHTML = '';
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Rules (' + list.length + ')' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No log collection rules.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Name' }),
    el('th', { text: 'Source' }),
    el('th', { text: 'Severity' }),
    el('th', { text: 'Match' }),
    el('th', { text: 'Destination' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  list.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: r.name }),
      el('td', { text: r.source }),
      el('td', { text: r.severity }),
      el('td', { text: r.match_pattern || '' }),
      el('td', { text: r.destination || '' }),
      el('td', { text: r.enabled ? 'yes' : 'no' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn ghost danger', 'data-lr-action': 'delete',
          'data-id': String(r.id), text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

async function openCreate(panel, rules) {
  const root = el('div', { class: 'form-stack' });
  root.appendChild(inputField('Name', 'name', ''));
  root.appendChild(inputField('Source (journald / syslog / file)', 'source', 'journald'));
  root.appendChild(inputField('Severity (debug/info/warn/error)', 'severity', 'info'));
  root.appendChild(inputField('Match pattern (regex)', 'match_pattern', ''));
  root.appendChild(inputField('Destination (file://, syslog://host:port, https://…)', 'destination', ''));
  root.appendChild(checkbox('Enabled', 'enabled', true));
  modal({
    title: 'Add log rule',
    body: root,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Add', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(root);
      if (!payload.name) { toast('Name required', 'warn'); return false; }
      try {
        await apiPost('/api/v1/admin/log-rules', payload);
        toast('Rule added', 'success');
        const r = await apiGet('/api/v1/admin/log-rules');
        rules = (r && r.rules) || [];
        paint(panel, rules);
        return true;
      } catch (e) {
        toast('Add failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function doDelete(panel, rules, id) {
  if (!confirm('Delete log rule #' + id + '?')) return;
  try {
    await apiDelete('/api/v1/admin/log-rules/' + id);
    toast('Rule deleted', 'success');
    const r = await apiGet('/api/v1/admin/log-rules');
    rules = (r && r.rules) || [];
    paint(panel, rules);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error');
  }
}

function inputField(label, name, val) {
  return el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { class: 'input', name, type: 'text', value: val }),
  ]);
}
function checkbox(label, name, val) {
  return el('label', { class: 'field check' }, [
    el('input', { class: 'input', name, type: 'checkbox', checked: val ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function readForm(root) {
  const out = {};
  root.querySelectorAll('input').forEach((inp) => {
    if (inp.type === 'checkbox') out[inp.name] = inp.checked;
    else out[inp.name] = inp.value;
  });
  return out;
}