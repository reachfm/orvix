/* =====================================================================
   pages/mailing-lists.js — Mailing-list admin UI.

   CRUD against /api/v1/admin/mailing-lists + member management
   against /api/v1/admin/mailing-lists/:id/members. Lists are
   delivery-side fan-out addresses; moderation / archive flags
   are surfaced from the backend.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderMailingListsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Mailing lists' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Address that fans out to a configurable subscriber set.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ml-action': 'create',
        text: 'Create list' }),
    ]),
  ]));
  const table = el('div', { class: 'panel' });
  wrap.appendChild(table);
  root.appendChild(wrap);

  let lists = [];
  let domains = [];
  try {
    const [lResp, dResp] = await Promise.all([
      apiGet('/api/v1/admin/mailing-lists'),
      apiGet('/api/v1/domains'),
    ]);
    lists = (lResp && lResp.lists) || [];
    domains = Array.isArray(dResp) ? dResp : [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
  }
  paint(table, lists);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-ml-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-ml-action');
    if (action === 'create') return openCreate(table, lists, domains);
    if (action === 'members') return openMembers(Number(tEl.getAttribute('data-id')));
    if (action === 'delete') return doDelete(table, lists, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(table, lists) {
  table.innerHTML = '';
  const head = el('header', { class: 'panel-head' }, el('h3', { text: 'Lists' }));
  table.appendChild(head);
  const body = el('div', { class: 'panel-body' });
  table.appendChild(body);
  if (!lists.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No mailing lists yet.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Address' }),
    el('th', { text: 'Members' }),
    el('th', { text: 'Moderation' }),
    el('th', { text: 'Archive' }),
    el('th', { text: 'Subscription' }),
    el('th', { text: 'Status' }),
    el('th', { text: '' }),
  ])));
  const tbody = el('tbody');
  lists.forEach((l) => {
    tbody.appendChild(el('tr', null, [
      el('td', { text: l.address }),
      el('td', { text: String(l.member_count || 0) }),
      el('td', { text: l.moderation_required ? 'yes' : 'no' }),
      el('td', { text: l.archive_enabled ? 'yes' : 'no' }),
      el('td', { text: l.subscription_policy || 'closed' }),
      el('td', { text: l.status || 'active' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn ghost', 'data-ml-action': 'members',
          'data-id': String(l.id), text: 'Members' }),
        el('button', { class: 'btn ghost danger', 'data-ml-action': 'delete',
          'data-id': String(l.id), text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tbody);
  body.appendChild(tbl);
}

async function openCreate(table, lists, domains) {
  const root = el('div', { class: 'form-stack' });
  root.appendChild(inputField('Address (local part)', 'address', ''));
  root.appendChild(inputField('Display name', 'display_name', ''));
  root.appendChild(inputField('Description', 'description', ''));
  const sel = el('select', { class: 'input', name: 'domain_id' });
  domains.forEach((d) => {
    sel.appendChild(el('option', { value: String(d.id || d.domain_id || 0),
      text: d.domain || d.name || '' }));
  });
  root.appendChild(el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: 'Domain' }), sel,
  ]));
  root.appendChild(checkbox('Moderation required', 'moderation_required', false));
  root.appendChild(checkbox('Archive enabled', 'archive_enabled', false));
  root.appendChild(inputField('Subscription policy', 'subscription_policy', 'closed'));
  root.appendChild(inputField('Max members (0 = unlimited)', 'max_members', '0'));

  modal({
    title: 'Create mailing list',
    body: root,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(root);
      if (!payload.address || !payload.domain_id) {
        toast('Address and domain are required', 'warn'); return false;
      }
      payload.domain_id = Number(payload.domain_id);
      payload.max_members = Number(payload.max_members || 0);
      try {
        await apiPost('/api/v1/admin/mailing-lists', payload);
        toast('Mailing list created', 'success');
        const resp = await apiGet('/api/v1/admin/mailing-lists');
        lists = (resp && resp.lists) || [];
        paint(table, lists);
        return true;
      } catch (e) {
        toast('Create failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function openMembers(id) {
  let members = [];
  try {
    const r = await apiGet('/api/v1/admin/mailing-lists/' + id + '/members');
    members = (r && r.members) || [];
  } catch (e) {
    toast('Failed to load members: ' + (e.message || e), 'error');
    return;
  }
  const root = el('div', { class: 'form-stack' });
  const ul = el('ul', { class: 'kv-list' });
  members.forEach((m) => {
    ul.appendChild(el('li', { class: 'kv-item', text: m.address + (m.display_name ? ' (' + m.display_name + ')' : '') }));
  });
  if (!members.length) ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No members yet.' }));
  root.appendChild(ul);

  root.appendChild(inputField('Add subscriber (email)', 'address', ''));
  root.appendChild(inputField('Display name (optional)', 'display_name', ''));
  root.appendChild(inputField('Role (subscriber / moderator / owner)', 'role', 'subscriber'));

  modal({
    title: 'List members',
    body: root,
    actions: [
      { label: 'Done', kind: 'primary', value: 'done' },
      { label: 'Add member', kind: 'ghost', value: 'add' },
    ],
    onAction: async (val) => {
      if (val === 'add') {
        const payload = readForm(root);
        if (!payload.address) { toast('Address required', 'warn'); return false; }
        try {
          await apiPost('/api/v1/admin/mailing-lists/' + id + '/members', payload);
          toast('Member added', 'success');
          return false; // keep modal open so user can add more
        } catch (e) {
          toast('Add failed: ' + (e.message || e), 'error');
          return false;
        }
      }
      return true;
    },
  });
}

async function doDelete(table, lists, id) {
  const l = lists.find((x) => x.id === id);
  if (!l) return;
  if (!confirm('Delete mailing list "' + l.address + '"?')) return;
  try {
    await apiDelete('/api/v1/admin/mailing-lists/' + id);
    toast('Mailing list deleted', 'success');
    const resp = await apiGet('/api/v1/admin/mailing-lists');
    lists = (resp && resp.lists) || [];
    paint(table, lists);
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