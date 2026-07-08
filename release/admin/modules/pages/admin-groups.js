/* =====================================================================
   pages/admin-groups.js — RBAC groups management UI.

   Built on the shared form builder (modules/form.js) so the
   edit modal enjoys field grouping, inline validation,
   submit-state spinner, error banner, keyboard submit, and
   the same visual treatment as every other admin modal.
   ===================================================================== */

import { el, modal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal } from '../form.js';

// Catalogue of grant tokens. The backend doesn't restrict
// the grant alphabet, but the UI surfaces the common ones so
// the operator gets the bulk of the surface area with two
// clicks. A free-form "Custom grants" field accepts any
// additional tokens.
const GRANT_GROUPS = [
  {
    title: 'Domains',
    items: [
      { value: 'domains.read',   label: 'domains.read',   desc: 'List / inspect domains' },
      { value: 'domains.write',  label: 'domains.write',  desc: 'Create / edit domains (incl. PATCH)' },
      { value: 'domains.delete', label: 'domains.delete', desc: 'Soft-delete domains' },
    ],
  },
  {
    title: 'Mailboxes',
    items: [
      { value: 'mailboxes.read',     label: 'mailboxes.read',     desc: 'List / inspect mailboxes' },
      { value: 'mailboxes.write',    label: 'mailboxes.write',    desc: 'Create / update mailboxes' },
      { value: 'mailboxes.delete',   label: 'mailboxes.delete',   desc: 'Delete mailboxes' },
      { value: 'mailboxes.import',   label: 'mailboxes.import',   desc: 'Bulk CSV import' },
      { value: 'mailboxes.export',   label: 'mailboxes.export',   desc: 'Bulk CSV export' },
    ],
  },
  {
    title: 'Queue',
    items: [
      { value: 'queue.read',   label: 'queue.read',   desc: 'Inspect deferred / bounced / failed' },
      { value: 'queue.write',  label: 'queue.write',  desc: 'Retry / cancel / bounce' },
      { value: 'queue.delete', label: 'queue.delete', desc: 'Drop queue entries' },
    ],
  },
  {
    title: 'Backups',
    items: [
      { value: 'backups.read',    label: 'backups.read',    desc: 'List / download backups' },
      { value: 'backups.create',  label: 'backups.create',  desc: 'Trigger backup jobs' },
      { value: 'backups.restore', label: 'backups.restore', desc: 'Restore from backup' },
      { value: 'backups.delete',  label: 'backups.delete',  desc: 'Delete backups' },
    ],
  },
  {
    title: 'DNS, audit, users, settings, license, monitoring',
    items: [
      { value: 'dns.read',           label: 'dns.read',           desc: 'Inspect DNS plan / providers' },
      { value: 'dns.apply',          label: 'dns.apply',          desc: 'Push DNS records to provider' },
      { value: 'audit.read',         label: 'audit.read',         desc: 'Read admin audit log' },
      { value: 'admin.users.read',   label: 'admin.users.read',   desc: 'List admin users / groups' },
      { value: 'admin.users.write',  label: 'admin.users.write',  desc: 'Create / disable / reset admin users' },
      { value: 'settings.read',      label: 'settings.read',      desc: 'Read runtime settings' },
      { value: 'settings.write',     label: 'settings.write',     desc: 'Patch runtime settings' },
      { value: 'license.read',       label: 'license.read',       desc: 'Inspect license posture' },
      { value: 'license.validate',   label: 'license.validate',   desc: 'Re-validate license' },
      { value: 'monitoring.read',    label: 'monitoring.read',    desc: 'Read alerts / capacity / snapshot' },
      { value: 'monitoring.resolve', label: 'monitoring.resolve', desc: 'Resolve alerts' },
    ],
  },
];
const KNOWN_GRANTS = GRANT_GROUPS.flatMap((g) => g.items.map((i) => i.value));
const GRANT_OPTIONS = GRANT_GROUPS.flatMap((g) => g.items);

// module-scoped cache so nested helpers can read the live list.
let _groups = [];

export async function renderAdminGroupsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Administrative groups' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'RBAC groups with explicit grants. Built-in groups are read-only; custom groups can be created, edited, and reassigned.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ag-action': 'create', text: 'Create group' }),
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderAdminGroupsPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'list-card-grid', id: 'ag-grid' });
  wrap.appendChild(grid);

  await refreshGrid();

  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-ag-action]');
    if (!t) return;
    const action = t.getAttribute('data-ag-action');
    if (action === 'create') return openEditor(null);
    if (action === 'edit') return openEditor(Number(t.getAttribute('data-id')));
    if (action === 'members') return openMembers(Number(t.getAttribute('data-id')));
    if (action === 'delete') return doDelete(Number(t.getAttribute('data-id')));
  });
  applyAutoDir(wrap);
}

async function refreshGrid() {
  try {
    const r = await apiGet('/api/v1/admin/admin-groups');
    _groups = (r && r.groups) || [];
  } catch (e) {
    toast('Failed to load groups: ' + (e.message || e), 'error');
    _groups = [];
  }
  const grid = document.getElementById('ag-grid');
  if (grid) paintGrid(grid, _groups);
}

function paintGrid(grid, list) {
  grid.innerHTML = '';
  if (!list || !list.length) {
    grid.appendChild(el('div', { class: 'list-card-empty' }, [
      el('div', { class: 'list-card-empty-icon', text: '\u2731' }),
      el('h3', { text: 'No administrative groups yet' }),
      el('p', { text: 'Custom groups let you scope multi-admin RBAC grants — e.g. "dns-admins", "billing-admins". Built-in groups (admin, super-admin) cover the default role surface.' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-ag-action': 'create', text: 'Create a group' }),
      ]),
    ]));
    return;
  }
  list.forEach((g) => {
    const isBuiltIn = String(g.name || '').startsWith('builtin.');
    const grants = g.grants || [];
    const card = el('article', { class: 'list-card' });
    card.appendChild(el('header', { class: 'list-card-head' }, [
      el('h4', { class: 'list-card-title', text: g.name || ('#' + g.id) }),
      el('div', { class: 'list-card-tag-row' }, [
        el('span', { class: 'list-card-tag', text: isBuiltIn ? 'built-in' : 'custom' }),
        el('span', { class: 'list-card-tag', text: grants.length + ' grants' }),
        el('span', { class: 'list-card-tag', text: (g.member_count || 0) + ' members' }),
      ]),
    ]));
    if (g.description) card.appendChild(el('p', { class: 'list-card-desc', text: g.description }));
    const meta = el('div', { class: 'list-card-meta' });
    const preview = grants.length
      ? (grants.slice(0, 6).join(', ') + (grants.length > 6 ? ' +\u2026' : ''))
      : '\u2014';
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Grants' }),
      el('span', { class: 'v', text: preview }),
    ]));
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Updated' }),
      el('span', { class: 'v', text: formatDateTime(g.updated_at) }),
    ]));
    card.appendChild(meta);
    const actions = el('div', { class: 'list-card-actions' });
    actions.appendChild(el('button', { class: 'btn xs ghost', 'data-ag-action': 'members',
      'data-id': String(g.id), text: 'Members' }));
    if (!isBuiltIn) {
      actions.appendChild(el('button', { class: 'btn xs ghost', 'data-ag-action': 'edit',
        'data-id': String(g.id), text: 'Edit' }));
      actions.appendChild(el('button', { class: 'btn xs ghost danger', 'data-ag-action': 'delete',
        'data-id': String(g.id), text: 'Delete' }));
    }
    card.appendChild(actions);
    grid.appendChild(card);
  });
}

function formatDateTime(s) {
  if (!s) return '\u2014';
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return String(s);
    return d.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  } catch (_) { return String(s); }
}

async function openEditor(id) {
  let g = { name: '', description: '', grants: [] };
  if (id != null) g = (_groups.find((x) => Number(x.id) === Number(id))) || g;
  const isEdit = id != null;
  const customGrants = (g.grants || []).filter((x) => KNOWN_GRANTS.indexOf(x) < 0);

  openFormModal({
    title: isEdit ? ('Edit group: ' + g.name) : 'Create administrative group',
    size: 'lg',
    groups: [
      {
        title: 'Identity',
        subtitle: 'Built-in groups are read-only and cannot be renamed. Custom groups accept dot-separated lowercase identifiers.',
        layout: 'cols-2',
        fields: [
          { name: 'name', kind: 'text', label: 'Name', required: true,
            placeholder: 'group.billing-admins',
            help: 'Lowercase, dot-separated identifier.',
            validate: (v) => (/^[a-z0-9._-]+$/.test(String(v)) ? null : { error: 'Use lowercase letters, digits, dots, dashes.' }),
            default: g.name },
          { name: 'description', kind: 'textarea', label: 'Description',
            placeholder: 'What this group is for', rows: 3, maxLength: 512,
            help: 'Free-form, shown in the group card.', default: g.description },
        ],
      },
      {
        title: 'Grants',
        subtitle: 'Toggle the operations this group can perform. Unknown tokens can be added under Custom grants.',
        fields: [
          { name: 'grants_known', kind: 'checkbox-grid', span: 'full',
            options: GRANT_OPTIONS, default: g.grants },
        ],
      },
      {
        title: 'Custom grants',
        subtitle: 'Comma-separated additional grant tokens. Server stores any string and enforces it per endpoint.',
        fields: [
          { name: 'grants_custom', kind: 'text', label: 'Custom grants',
            placeholder: 'plugin.beta-feature, custom.permission',
            help: 'Whitespace trimmed, duplicates removed.', default: customGrants.join(', ') },
        ],
      },
    ],
    submitLabel: isEdit ? 'Save changes' : 'Create group',
    successMessage: isEdit ? 'Administrative group updated.' : 'Administrative group created.',
    onSubmit: async (values) => {
      const known = (values.grants_known || []);
      const custom = String(values.grants_custom || '').split(',').map((s) => s.trim()).filter(Boolean);
      const seen = new Set();
      const grants = [];
      [...known, ...custom].forEach((tok) => { if (!seen.has(tok)) { seen.add(tok); grants.push(tok); } });
      const payload = {
        name: String(values.name || '').trim(),
        description: String(values.description || '').trim(),
        grants,
      };
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/admin-groups/' + id, payload);
        } else {
          await apiPost('/api/v1/admin/admin-groups', payload);
        }
        await refreshGrid();
        return { ok: true };
      } catch (e) {
        return { ok: false, error: (e && (e.detail || e.message)) || 'Save failed.' };
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
  root.appendChild(el('h4', { class: 'list-card-title', text: 'Current members (' + members.length + ')' }));
  const ul = el('ul', { class: 'kv-list' });
  if (!members.length) ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No members yet. Add one below.' }));
  members.forEach((m) => {
    const li = el('li', { class: 'kv-item',
      text: (m.email || ('user #' + m.user_id)) + (m.role ? ' (' + m.role + ')' : '') });
    li.appendChild(el('button', {
      class: 'btn xs ghost danger', type: 'button', style: 'float:right',
      text: 'Remove',
      onclick: async () => {
        try {
          await apiDelete('/api/v1/admin/admin-groups/' + id + '/members/' + (m.user_id || m.id));
          toast('Member removed', 'success', 1400);
          li.remove();
        } catch (e) { toast('Remove failed: ' + (e.message || e), 'error'); }
      },
    }));
    ul.appendChild(li);
  });
  root.appendChild(ul);

  const addWrap = el('div', { style: 'margin-top:14px; padding-top:14px; border-top:1px solid var(--border-1)' });
  addWrap.appendChild(el('h4', { class: 'list-card-title', text: 'Add member' }));
  const inputRow = el('div', { style: 'display:grid; grid-template-columns: 1fr auto; gap:8px; margin-top:8px;' });
  const userInput = el('input', {
    type: 'text', class: 'ff-input', placeholder: 'user ID',
    autocomplete: 'off', spellcheck: 'false',
  });
  const addBtn = el('button', { type: 'button', class: 'btn primary', text: 'Add' });
  addBtn.addEventListener('click', async () => {
    const raw = String(userInput.value || '').trim();
    if (!raw) { toast('Enter a user ID', 'warn'); return; }
    const userId = Number(raw);
    if (!userId || isNaN(userId)) { toast('User ID must be a numeric identifier', 'warn'); return; }
    try {
      await apiPost('/api/v1/admin/admin-groups/' + id + '/members', { user_id: userId });
      toast('Member added', 'success', 1400);
      userInput.value = '';
    } catch (e) {
      toast('Add failed: ' + (e.message || e), 'error');
    }
  });
  inputRow.appendChild(userInput);
  inputRow.appendChild(addBtn);
  addWrap.appendChild(inputRow);
  root.appendChild(addWrap);

  modal({
    title: 'Group members',
    body: root,
    actions: [{ label: 'Done', kind: 'primary', value: 'done' }],
    onAction: async () => true,
  });
}

async function doDelete(id) {
  let name = '#' + id;
  const g = _groups.find((x) => Number(x.id) === Number(id));
  if (g) name = g.name;
  const ok = await confirmDanger({
    title: 'Delete administrative group',
    message: 'Delete "' + name + '"? Members keep their existing rights via other groups; this group\'s grants stop being attached when it is removed.',
    confirmLabel: 'Delete',
    requireText: name,
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/admin-groups/' + id);
    toast('Group deleted', 'success', 1400);
    await refreshGrid();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}
