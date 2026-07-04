/* =====================================================================
   pages/admin-groups.js — RBAC admin-groups management UI.

   CRUD against /api/v1/admin/admin-groups + member management
   against /api/v1/admin/admin-groups/:id/members. The grants
   list is a comma-separated string the backend stores on the
   row; the UI shows it as checkboxes for the common tokens.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

// Common grant tokens. Operators can also pass arbitrary tokens
// via the "Custom grants" field — the backend doesn't restrict
// the grant alphabet, only the per-endpoint enforcement does.
const GRANTS = [
  'domains.read', 'domains.write', 'domains.delete',
  'mailboxes.read', 'mailboxes.write', 'mailboxes.delete',
  'mailboxes.import', 'mailboxes.export',
  'queue.read', 'queue.write', 'queue.delete',
  'backups.read', 'backups.create', 'backups.restore', 'backups.delete',
  'dns.read', 'dns.apply',
  'audit.read',
  'admin.users.read', 'admin.users.write',
  'settings.read', 'settings.write',
  'license.read', 'license.validate',
  'monitoring.read', 'monitoring.resolve',
];

export async function renderAdminGroupsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Administrative groups' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'RBAC groups with explicit grants. Built-in groups are read-only.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ag-action': 'create',
        text: 'Create group' }),
    ]),
  ]));
  const grid = el('div', { class: 'card-grid two' });
  wrap.appendChild(grid);
  root.appendChild(wrap);

  let list = [];
  try {
    const r = await apiGet('/api/v1/admin/admin-groups');
    list = (r && r.groups) || [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
  }
  paint(grid, list);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-ag-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-ag-action');
    if (action === 'create') return openCreate(grid, list);
    if (action === 'edit') return openEdit(grid, list, Number(tEl.getAttribute('data-id')));
    if (action === 'members') return openMembers(Number(tEl.getAttribute('data-id')));
    if (action === 'delete') return doDelete(grid, list, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(grid, list) {
  grid.innerHTML = '';
  if (!list.length) {
    grid.appendChild(el('div', { class: 'empty panel',
      text: 'No administrative groups defined.' }));
    return;
  }
  list.forEach((g) => {
    const isBuiltIn = (g.name || '').startsWith('builtin.');
    const card = el('section', { class: 'panel' });
    card.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: g.name }),
      el('span', { class: 'badge tag', text: isBuiltIn ? 'built-in' : 'custom' }),
    ]));
    if (g.description) {
      card.appendChild(el('div', { class: 'panel-body subtle',
        text: g.description }));
    }
    const grants = g.grants || [];
    const ul = el('ul', { class: 'kv-list grants-list' });
    if (grants.length) {
      grants.forEach((t) => ul.appendChild(el('li', { class: 'kv-item', text: t })));
    } else {
      ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No grants' }));
    }
    card.appendChild(el('div', { class: 'panel-body' }, [
      el('div', { class: 'subtle', text: 'Grants (' + grants.length + ')' }),
      ul,
    ]));
    const members = g.member_count || 0;
    card.appendChild(el('div', { class: 'panel-body subtle',
      text: 'Members: ' + members }));
    const foot = el('div', { class: 'panel-foot' });
    foot.appendChild(el('button', { class: 'btn ghost', 'data-ag-action': 'members',
      'data-id': String(g.id), text: 'Members' }));
    if (!isBuiltIn) {
      foot.appendChild(el('button', { class: 'btn ghost', 'data-ag-action': 'edit',
        'data-id': String(g.id), text: 'Edit' }));
      foot.appendChild(el('button', { class: 'btn ghost danger', 'data-ag-action': 'delete',
        'data-id': String(g.id), text: 'Delete' }));
    }
    card.appendChild(foot);
    grid.appendChild(card);
  });
}

function buildForm(g) {
  g = g || {};
  const root = el('div', { class: 'form-stack' });
  root.appendChild(inputField('Name', 'name', g.name || ''));
  root.appendChild(inputField('Description', 'description', g.description || ''));
  const granted = new Set(g.grants || []);
  const grantsBox = el('div', { class: 'grants-grid' });
  GRANTS.forEach((tok) => {
    grantsBox.appendChild(el('label', { class: 'field check' }, [
      el('input', { type: 'checkbox', name: 'grant', value: tok,
        checked: granted.has(tok) ? 'checked' : null }),
      el('span', { class: 'field-label', text: tok }),
    ]));
  });
  root.appendChild(el('div', { class: 'subtle', text: 'Grants' }));
  root.appendChild(grantsBox);
  // custom grants (free-form)
  root.appendChild(inputField('Custom grants (comma-separated)',
    'custom', (g.grants || []).filter((x) => GRANTS.indexOf(x) < 0).join(', ')));
  return root;
}

function inputField(label, name, val) {
  return el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { class: 'input', name, type: 'text', value: val }),
  ]);
}

function readForm(root) {
  const grants = [];
  root.querySelectorAll('input[type="checkbox"][name="grant"]').forEach((cb) => {
    if (cb.checked) grants.push(cb.value);
  });
  const custom = (root.querySelector('input[name="custom"]') || {}).value || '';
  custom.split(',').forEach((t) => {
    t = t.trim();
    if (t) grants.push(t);
  });
  const name = (root.querySelector('input[name="name"]') || {}).value || '';
  const description = (root.querySelector('input[name="description"]') || {}).value || '';
  return { name, description, grants };
}

async function openCreate(grid, list) {
  const form = buildForm();
  modal({
    title: 'Create admin group',
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      if (!payload.name) { toast('Name required', 'warn'); return false; }
      try {
        await apiPost('/api/v1/admin/admin-groups', payload);
        toast('Admin group created', 'success');
        const r = await apiGet('/api/v1/admin/admin-groups');
        list = (r && r.groups) || [];
        paint(grid, list);
        return true;
      } catch (e) {
        toast('Create failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function openEdit(grid, list, id) {
  const g = list.find((x) => x.id === id);
  if (!g) return;
  const form = buildForm(g);
  modal({
    title: 'Edit admin group: ' + g.name,
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Save', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      try {
        await apiPatch('/api/v1/admin/admin-groups/' + id, payload);
        toast('Admin group updated', 'success');
        const r = await apiGet('/api/v1/admin/admin-groups');
        list = (r && r.groups) || [];
        paint(grid, list);
        return true;
      } catch (e) {
        toast('Update failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function openMembers(id) {
  let members = [];
  try {
    const r = await apiGet('/api/v1/admin/admin-groups/' + id + '/members');
    members = (r && r.members) || [];
  } catch (e) {
    toast('Failed to load members: ' + (e.message || e), 'error');
    return;
  }
  const root = el('div', { class: 'form-stack' });
  const ul = el('ul', { class: 'kv-list' });
  members.forEach((m) => {
    ul.appendChild(el('li', { class: 'kv-item',
      text: m.email + (m.role ? ' (' + m.role + ')' : '') }));
  });
  if (!members.length) ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No members yet.' }));
  root.appendChild(ul);
  root.appendChild(inputField('Add user_id', 'user_id', ''));

  modal({
    title: 'Group members',
    body: root,
    actions: [
      { label: 'Done', kind: 'primary', value: 'done' },
      { label: 'Add member', kind: 'ghost', value: 'add' },
    ],
    onAction: async (val) => {
      if (val === 'add') {
        const userId = Number((root.querySelector('input[name="user_id"]') || {}).value || 0);
        if (!userId) { toast('user_id required', 'warn'); return false; }
        try {
          await apiPost('/api/v1/admin/admin-groups/' + id + '/members', { user_id: userId });
          toast('Member added', 'success');
          return false;
        } catch (e) {
          toast('Add failed: ' + (e.message || e), 'error');
          return false;
        }
      }
      return true;
    },
  });
}

async function doDelete(grid, list, id) {
  const g = list.find((x) => x.id === id);
  if (!g) return;
  if (!confirm('Delete admin group "' + g.name + '"?')) return;
  try {
    await apiDelete('/api/v1/admin/admin-groups/' + id);
    toast('Admin group deleted', 'success');
    const r = await apiGet('/api/v1/admin/admin-groups');
    list = (r && r.groups) || [];
    paint(grid, list);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error');
  }
}