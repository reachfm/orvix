/* =====================================================================
   pages/incoming-rules.js — Admin-scoped Incoming Message Rules UI.

   Distinct from per-mailbox webmail rules at
   /api/v1/webmail/rules: these are tenant-wide rules
   applied to every incoming message before any
   per-mailbox filter.

   Wires:
     GET    /api/v1/admin/incoming-msg-rules       → list
     POST   /api/v1/admin/incoming-msg-rules       → create
     PATCH  /api/v1/admin/incoming-msg-rules/:id   → update
     DELETE /api/v1/admin/incoming-msg-rules/:id   → delete
   ===================================================================== */

import { el, modal, toast } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderIncomingRulesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Incoming message rules' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Tenant-wide filter rules applied before per-mailbox webmail rules. Priority-ordered.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-irr-action': 'create',
        text: 'Create rule' }),
    ]),
  ]));
  root.appendChild(wrap);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Rules' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  panel.appendChild(body);
  wrap.appendChild(panel);

  let list = [];
  try {
    const r = await apiGet('/api/v1/admin/incoming-msg-rules');
    list = (r && r.rules) || [];
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: 'Failed to load: ' + (e.message || e) }));
    applyAutoDir(wrap);
    return;
  }
  paint(body, list);

  wrap.addEventListener('click', async (ev) => {
    const t = ev.target.closest('[data-irr-action]');
    if (!t) return;
    const a = t.getAttribute('data-irr-action');
    if (a === 'create') return openForm(body, list, null);
    const m = a && a.match(/^(edit|delete):(\d+)$/);
    if (m) {
      if (m[1] === 'edit') return openForm(body, list, Number(m[2]));
      if (m[1] === 'delete') return doDelete(body, list, Number(m[2]));
    }
  });

  applyAutoDir(wrap);
}

async function openForm(body, list, id) {
  const isEdit = id != null;
  const r = list.find((x) => x.id === id) || {};
  const form = el('div', { class: 'form-stack' });
  form.appendChild(textField('Name', 'name', r.name || ''));
  form.appendChild(numberField('Priority (lower first)', 'priority', r.priority || 100));
  form.appendChild(select('Field', 'field', r.field || 'subject',
    ['subject', 'from', 'to', 'any_header', 'size', 'spf', 'dkim', 'dmarc']));
  form.appendChild(select('Operator', 'operator', r.operator || 'contains',
    ['contains', 'equals', 'starts_with', 'ends_with', 'matches', 'gt', 'lt']));
  form.appendChild(textField('Value', 'value', r.value || ''));
  form.appendChild(select('Action', 'action', r.action || 'reject',
    ['reject', 'quarantine', 'tag']));
  form.appendChild(textField('Action target (folder / label / forward address)', 'action_target', r.action_target || ''));
  form.appendChild(select('Apply to', 'apply_to', r.apply_to || 'all',
    ['all', 'incoming_only', 'outgoing_only']));
  form.appendChild(checkField('Stop processing further rules', 'stop_processing', r.stop_processing === true));
  form.appendChild(checkField('Enabled', 'enabled', r.enabled !== false));
  form.appendChild(textArea('Note', 'note', r.note || '', 3));
  modal({
    title: isEdit ? ('Edit incoming message rule #' + id) : 'Create incoming message rule',
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: isEdit ? 'Save' : 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return false;
      const payload = {
        name: readField(form, 'name'),
        priority: Number(readField(form, 'priority') || 0),
        field: readField(form, 'field'),
        operator: readField(form, 'operator'),
        value: readField(form, 'value'),
        action: readField(form, 'action'),
        action_target: readField(form, 'action_target'),
        apply_to: readField(form, 'apply_to'),
        stop_processing: readCheck(form, 'stop_processing'),
        enabled: readCheck(form, 'enabled'),
        note: readField(form, 'note'),
      };
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/incoming-msg-rules/' + id, payload);
          toast('Rule updated', 'success');
        } else {
          await apiPost('/api/v1/admin/incoming-msg-rules', payload);
          toast('Rule created', 'success');
        }
        const r2 = await apiGet('/api/v1/admin/incoming-msg-rules');
        list = (r2 && r2.rules) || [];
        paint(body, list);
        return true;
      } catch (e) {
        toast('Save failed: ' + (e.message || e), 'error', 6000);
        return false;
      }
    },
  });
}

async function doDelete(body, list, id) {
  if (!confirm('Delete incoming message rule #' + id + '?')) return;
  try {
    await apiDelete('/api/v1/admin/incoming-msg-rules/' + id);
    toast('Rule deleted', 'success');
    const r2 = await apiGet('/api/v1/admin/incoming-msg-rules');
    list = (r2 && r2.rules) || [];
    paint(body, list);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error', 6000);
  }
}

function paint(body, list) {
  body.innerHTML = '';
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No admin-scoped incoming message rules yet. Use "Create rule" to add one.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Priority' }),
    el('th', { text: 'Name' }),
    el('th', { text: 'Condition' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: 'Actions' }),
  ])));
  const tb = el('tbody');
  list.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: String(r.priority) }),
      el('td', { text: r.name }),
      el('td', { class: 'kv-v mono small', text: r.field + ' ' + r.operator + ' "' + r.value + '"' }),
      el('td', { text: r.action + (r.action_target ? ' → ' + r.action_target : '') + (r.stop_processing ? ' [stop]' : '') }),
      el('td', { class: 'kv-v', text: r.enabled ? 'yes' : 'no' }),
      el('td', null, [
        el('button', { class: 'btn xs ghost', 'data-irr-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-irr-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function textField(label, name, value) {
  const id = 'ir_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input', value: value || '' }),
  ]);
}
function numberField(label, name, value) {
  const id = 'ir_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'number', class: 'input', value: String(value || 0), min: '0' }),
  ]);
}
function textArea(label, name, value, rows) {
  const id = 'ir_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('textarea', { id, name, class: 'input', rows: rows || 3 }, value || ''),
  ]);
}
function checkField(label, name, value) {
  const id = 'ir_' + name;
  return el('label', { class: 'field check', for: id }, [
    el('input', { id, name, type: 'checkbox', checked: value ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function select(label, name, value, options) {
  const id = 'ir_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('select', { id, name, class: 'input' },
      options.map((o) => el('option', { value: o, selected: o === value ? 'selected' : null }, o))),
  ]);
}
function readField(scope, name) {
  const n = scope.querySelector('[name="' + name + '"]');
  return n ? (n.value || '') : '';
}
function readCheck(scope, name) {
  const n = scope.querySelector('[name="' + name + '"]');
  return n ? !!n.checked : false;
}
