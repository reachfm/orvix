/* =====================================================================
   pages/acl.js — Global Access Control UI.

   CRUD against /api/v1/admin/acl-rules. Each rule is
   allow / deny per source CIDR (or single IP) per protocol.
   Order is by priority ascending.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderACLPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Global access control' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Allow / deny rules per source CIDR or IP, per protocol.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-acl-action': 'create',
        text: 'Add rule' }),
    ]),
  ]));
  const tbl = el('div', { class: 'panel' });
  wrap.appendChild(tbl);
  root.appendChild(wrap);

  let rules = [];
  try {
    const r = await apiGet('/api/v1/admin/acl-rules');
    rules = (r && r.rules) || [];
  } catch (e) {
    toast('Failed to load ACL: ' + (e.message || e), 'error');
  }
  paint(tbl, rules);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-acl-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-acl-action');
    if (action === 'create') return openCreate(tbl, rules);
    if (action === 'delete') return doDelete(tbl, rules, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(panel, list) {
  panel.innerHTML = '';
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'ACL rules (' + list.length + ')' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No ACL rules. Default behaviour applies.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Prio' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Protocol' }),
    el('th', { text: 'Source' }),
    el('th', { text: 'Note' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  list.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: String(r.priority) }),
      el('td', { text: r.action }),
      el('td', { text: r.protocol }),
      el('td', { text: r.source }),
      el('td', { text: r.note || '' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn ghost danger', 'data-acl-action': 'delete',
          'data-id': String(r.id), text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

async function openCreate(panel, rules) {
  const root = el('div', { class: 'form-stack' });
  root.appendChild(inputField('Source (CIDR or IP)', 'source', ''));
  root.appendChild(selectField('Action', 'action', [
    { value: 'allow', label: 'allow' },
    { value: 'deny', label: 'deny' },
  ]));
  root.appendChild(selectField('Protocol', 'protocol', [
    { value: 'all', label: 'all' },
    { value: 'smtp', label: 'smtp' },
    { value: 'imap', label: 'imap' },
    { value: 'pop3', label: 'pop3' },
    { value: 'jmap', label: 'jmap' },
    { value: 'webmail', label: 'webmail' },
  ]));
  root.appendChild(inputField('Priority (lower = first)', 'priority', '100'));
  root.appendChild(inputField('Note', 'note', ''));
  modal({
    title: 'Add ACL rule',
    body: root,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Add', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(root);
      if (!payload.source) { toast('Source required', 'warn'); return false; }
      payload.priority = Number(payload.priority || 100);
      try {
        await apiPost('/api/v1/admin/acl-rules', payload);
        toast('Rule added', 'success');
        const r = await apiGet('/api/v1/admin/acl-rules');
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
  if (!confirm('Delete ACL rule #' + id + '?')) return;
  try {
    await apiDelete('/api/v1/admin/acl-rules/' + id);
    toast('Rule deleted', 'success');
    const r = await apiGet('/api/v1/admin/acl-rules');
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
function selectField(label, name, opts) {
  const sel = el('select', { class: 'input', name });
  opts.forEach((o) => sel.appendChild(el('option', { value: o.value, text: o.label })));
  return el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: label }), sel,
  ]);
}
function readForm(root) {
  const out = {};
  root.querySelectorAll('input').forEach((inp) => { out[inp.name] = inp.value; });
  root.querySelectorAll('select').forEach((s) => { out[s.name] = s.value; });
  return out;
}