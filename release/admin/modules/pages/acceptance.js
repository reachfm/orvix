/* =====================================================================
   pages/acceptance.js — Acceptance & Routing rules admin UI.

   Wires:
     GET    /api/v1/admin/acceptance-rules      → list
     POST   /api/v1/admin/acceptance-rules      → create
     PATCH  /api/v1/admin/acceptance-rules/:id  → update
     DELETE /api/v1/admin/acceptance-rules/:id  → delete
     POST   /api/v1/admin/acceptance-rules/test → dry-run match

   The page exposes CRUD on rules (priority-ordered) plus
   a "test" affordance that takes a sample
   sender/recipient/IP and walks the enabled rules in
   priority order to report which rule (if any) would
   match. The test never mutates state.
   ===================================================================== */

import { el, modal, toast } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAcceptancePage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Acceptance & routing rules' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Priority-ordered admin rules. Lower priority is applied first. Use the dry-run to verify match logic.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-acc-action': 'create',
        text: 'Create rule' }),
      el('button', { class: 'btn ghost', 'data-acc-action': 'test',
        text: 'Dry-run match' }),
    ]),
  ]));
  root.appendChild(wrap);

  const tablePanel = el('section', { class: 'panel' });
  tablePanel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Rules' })));
  const tableBody = el('div', { class: 'panel-body', text: 'Loading...' });
  tablePanel.appendChild(tableBody);
  wrap.appendChild(tablePanel);

  let list = [];
  try {
    const r = await apiGet('/api/v1/admin/acceptance-rules');
    list = (r && r.rules) || [];
  } catch (e) {
    tableBody.innerHTML = '';
    tableBody.appendChild(el('div', { class: 'empty', text: 'Failed to load: ' + (e.message || e) }));
    applyAutoDir(wrap);
    return;
  }
  paint(tableBody, list);

  wrap.addEventListener('click', async (ev) => {
    const t = ev.target.closest('[data-acc-action]');
    if (!t) return;
    const a = t.getAttribute('data-acc-action');
    if (a === 'create') return openForm(tableBody, list, null);
    if (a === 'test') return openTest();
    const m = a && a.match(/^(edit|delete):(\d+)$/);
    if (m) {
      if (m[1] === 'edit') return openForm(tableBody, list, Number(m[2]));
      if (m[1] === 'delete') return doDelete(tableBody, list, Number(m[2]));
    }
  });

  applyAutoDir(wrap);

  async function openTest() {
    const r = el('div', { class: 'form-stack' });
    r.appendChild(textField('Sender (e.g. user@example.com)', 'sender', ''));
    r.appendChild(textField('Recipient (e.g. inbox@mydomain.com)', 'recipient', ''));
    r.appendChild(textField('Source IP (e.g. 203.0.113.5)', 'source_ip', ''));
    const out = el('div', { class: 'panel-body', text: '' });
    r.appendChild(out);
    modal({
      title: 'Dry-run acceptance / routing match',
      body: r,
      actions: [
        { label: 'Cancel', kind: 'ghost', value: 'cancel' },
        { label: 'Test', kind: 'primary', value: 'ok' },
      ],
      onAction: async (val) => {
        if (val !== 'ok') return false;
        out.innerHTML = '';
        out.appendChild(el('p', { class: 'subtle', text: 'Walking rules...' }));
        try {
          const resp = await apiPost('/api/v1/admin/acceptance-rules/test', {
            sender: readField(r, 'sender'),
            recipient: readField(r, 'recipient'),
            source_ip: readField(r, 'source_ip'),
          });
          out.innerHTML = '';
          out.appendChild(el('p', null, [
            el('strong', { text: 'Result: ' }),
            el('span', { text: String((resp && resp.action_label) || '-') }),
          ]));
          if (resp && resp.matched_rule_id != null) {
            out.appendChild(el('p', null, [
              el('strong', { text: 'Matched rule id: ' }),
              el('code', { text: String(resp.matched_rule_id) }),
              el('span', { text: ' (' + (resp.matched_rule_name || '-') + ')' }),
            ]));
          }
          out.appendChild(el('p', { class: 'subtle',
            text: 'Rules walked: ' + ((resp && resp.rules_walked) || 0) }));
          return false;
        } catch (e) {
          out.innerHTML = '';
          out.appendChild(el('div', { class: 'error', text: 'Test failed: ' + (e.message || e) }));
          return false;
        }
      },
    });
  }

  async function openForm(body, list, id) {
    const isEdit = id != null;
    const r = list.find((x) => x.id === id) || {};
    const form = el('div', { class: 'form-stack' });
    form.appendChild(textField('Name', 'name', r.name || ''));
    form.appendChild(numberField('Priority (lower first)', 'priority', r.priority || 100));
    form.appendChild(select('Scope', 'scope', r.scope || 'global',
      ['global', 'domain', 'mailbox']));
    form.appendChild(textField('Scope target (domain / mailbox — empty for global)', 'scope_target', r.scope_target || ''));
    form.appendChild(textField('Sender pattern (substring or *glob*)', 'sender_pattern', r.sender_pattern || ''));
    form.appendChild(textField('Recipient pattern', 'recipient_pattern', r.recipient_pattern || ''));
    form.appendChild(textField('Source IP CIDR (optional)', 'source_ip_cidr', r.source_ip_cidr || ''));
    form.appendChild(select('Action', 'action', r.action || 'accept', ['accept', 'reject', 'quarantine']));
    form.appendChild(checkField('Enabled', 'enabled', r.enabled !== false));
    form.appendChild(textArea('Note', 'note', r.note || '', 3));
    modal({
      title: isEdit ? ('Edit acceptance rule #' + id) : 'Create acceptance rule',
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
          scope: readField(form, 'scope'),
          scope_target: readField(form, 'scope_target'),
          sender_pattern: readField(form, 'sender_pattern'),
          recipient_pattern: readField(form, 'recipient_pattern'),
          source_ip_cidr: readField(form, 'source_ip_cidr'),
          action: readField(form, 'action'),
          enabled: readCheck(form, 'enabled'),
          note: readField(form, 'note'),
        };
        try {
          if (isEdit) {
            await apiPatch('/api/v1/admin/acceptance-rules/' + id, payload);
            toast('Rule updated', 'success');
          } else {
            await apiPost('/api/v1/admin/acceptance-rules', payload);
            toast('Rule created', 'success');
          }
          const r2 = await apiGet('/api/v1/admin/acceptance-rules');
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
    if (!confirm('Delete acceptance rule #' + id + '?')) return;
    try {
      await apiDelete('/api/v1/admin/acceptance-rules/' + id);
      toast('Rule deleted', 'success');
      const r2 = await apiGet('/api/v1/admin/acceptance-rules');
      list = (r2 && r2.rules) || [];
      paint(body, list);
    } catch (e) {
      toast('Delete failed: ' + (e.message || e), 'error', 6000);
    }
  }
}

function paint(body, list) {
  body.innerHTML = '';
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No acceptance & routing rules yet. Use "Create rule" to add one.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Priority' }),
    el('th', { text: 'Name' }),
    el('th', { text: 'Scope' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Patterns' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: 'Actions' }),
  ])));
  const tb = el('tbody');
  list.forEach((r) => {
    const patterns = [];
    if (r.sender_pattern) patterns.push('from: ' + r.sender_pattern);
    if (r.recipient_pattern) patterns.push('to: ' + r.recipient_pattern);
    if (r.source_ip_cidr) patterns.push('ip: ' + r.source_ip_cidr);
    tb.appendChild(el('tr', null, [
      el('td', { text: String(r.priority) }),
      el('td', { text: r.name }),
      el('td', { text: r.scope + (r.scope_target ? ' (' + r.scope_target + ')' : '') }),
      el('td', { text: r.action }),
      el('td', { text: patterns.join('; ') || '-' }),
      el('td', { class: 'kv-v', text: r.enabled ? 'yes' : 'no' }),
      el('td', null, [
        el('button', { class: 'btn xs ghost', 'data-acc-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-acc-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function textField(label, name, value) {
  const id = 'af_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input', value: value || '' }),
  ]);
}
function numberField(label, name, value) {
  const id = 'af_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'number', class: 'input', value: String(value || 0), min: '0' }),
  ]);
}
function textArea(label, name, value, rows) {
  const id = 'af_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('textarea', { id, name, class: 'input', rows: rows || 3 }, value || ''),
  ]);
}
function checkField(label, name, value) {
  const id = 'af_' + name;
  return el('label', { class: 'field check', for: id }, [
    el('input', { id, name, type: 'checkbox', checked: value ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function select(label, name, value, options) {
  const id = 'af_' + name;
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
