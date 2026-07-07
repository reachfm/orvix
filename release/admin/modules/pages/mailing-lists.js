/* =====================================================================
   pages/mailing-lists.js — Mailing-list admin UI.

   CRUD against /api/v1/admin/mailing-lists + member management
   against /api/v1/admin/mailing-lists/:id/members. Lists are
   delivery-side fan-out addresses; moderation / archive flags
   are surfaced from the backend.

   Built on the shared form builder for the create/edit modal
   so the page matches every other admin modal in field grouping,
   inline validation, error banner, submit-state spinner, and
   keyboard submit.

   Backend PATCH /api/v1/admin/mailing-lists/:id covers the
   editable fields (display_name, description, moderation_required,
   archive_enabled, subscription_policy, max_members, status).
   The address/domain_id are immutable after creation.
   ===================================================================== */

import { el, modal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal } from '../form.js';

const SUBSCRIPTION_POLICIES = [
  { value: 'open',       label: 'open',       desc: 'Anyone can subscribe directly' },
  { value: 'closed',     label: 'closed',     desc: 'Owner / moderator must add subscribers' },
  { value: 'moderated',  label: 'moderated',  desc: 'New subscribers held until a moderator approves' },
  { value: 'announce',   label: 'announce',   desc: 'Read-only (announcements-only); subscribers cannot post' },
];
const STATUS_OPTIONS = [
  { value: 'active',    label: 'active' },
  { value: 'suspended', label: 'suspended' },
  { value: 'archived',  label: 'archived' },
];

let _lists = [];
let _domains = [];

export async function renderMailingListsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Mailing lists' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Address that fans out to a configurable subscriber set. Address / domain are immutable; everything else can be tuned via PATCH.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ml-action': 'create', text: 'Create list' }),
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderMailingListsPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'list-card-grid', id: 'ml-grid' });
  wrap.appendChild(grid);

  await refresh();

  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-ml-action]');
    if (!t) return;
    const action = t.getAttribute('data-ml-action');
    if (action === 'create') return openEditor();
    if (action === 'edit')   return openEditor(Number(t.getAttribute('data-id')));
    if (action === 'members') return openMembers(Number(t.getAttribute('data-id')));
    if (action === 'delete')  return doDelete(Number(t.getAttribute('data-id')));
  });
  applyAutoDir(wrap);
}

async function refresh() {
  try {
    const [lResp, dResp] = await Promise.all([
      apiGet('/api/v1/admin/mailing-lists').catch(() => ({ lists: [] })),
      apiGet('/api/v1/domains').catch(() => []),
    ]);
    _lists = (lResp && lResp.lists) || [];
    _domains = Array.isArray(dResp) ? dResp : [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
    _lists = [];
    _domains = [];
  }
  const grid = document.getElementById('ml-grid');
  if (grid) paint(grid, _lists, _domains);
}

function paint(grid, lists, domains) {
  grid.innerHTML = '';
  if (!lists.length) {
    grid.appendChild(el('div', { class: 'list-card-empty' }, [
      el('div', { class: 'list-card-empty-icon', text: '\u2709' }),
      el('h3', { text: 'No mailing lists defined' }),
      el('p', { text: 'Create a list above to fan-out inbound mail to a configured subscriber set. Lists are tenant-scoped to one of your domains.' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-ml-action': 'create', text: 'Create a list' }),
      ]),
    ]));
    return;
  }

  const domainName = (d) => (domains.find((x) => Number(x.id) === Number(d)) || {}).name || (domains.find((x) => Number(x.id) === Number(d)) || {}).domain || ('domain #' + d);

  lists.forEach((l) => {
    const card = el('article', { class: 'list-card' });
    const addr = (l.address || '') + '@' + domainName(l.domain_id);
    card.appendChild(el('header', { class: 'list-card-head' }, [
      el('h4', { class: 'list-card-title', text: addr }),
      el('div', { class: 'list-card-tag-row' }, [
        el('span', { class: 'list-card-tag', text: (l.member_count || 0) + ' members' }),
        el('span', { class: 'list-card-tag', text: l.subscription_policy || 'closed' }),
        el('span', { class: 'list-card-tag', text: l.status || 'active' }),
      ]),
    ]));
    if (l.display_name) card.appendChild(el('p', { class: 'list-card-desc', text: l.display_name }));
    if (l.description)  card.appendChild(el('p', { class: 'list-card-desc subtle small', text: l.description }));

    const meta = el('div', { class: 'list-card-meta' });
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Moderation' }),
      el('span', { class: 'v', text: l.moderation_required ? 'required' : 'off' }),
    ]));
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Archive' }),
      el('span', { class: 'v', text: l.archive_enabled ? 'enabled' : 'off' }),
    ]));
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Max members' }),
      el('span', { class: 'v', text: l.max_members > 0 ? String(l.max_members) : 'unlimited' }),
    ]));
    card.appendChild(meta);

    const actions = el('div', { class: 'list-card-actions' });
    actions.appendChild(el('button', { class: 'btn xs ghost', 'data-ml-action': 'members', 'data-id': String(l.id), text: 'Members' }));
    actions.appendChild(el('button', { class: 'btn xs ghost', 'data-ml-action': 'edit', 'data-id': String(l.id), text: 'Edit' }));
    actions.appendChild(el('button', { class: 'btn xs ghost danger', 'data-ml-action': 'delete', 'data-id': String(l.id), text: 'Delete' }));
    card.appendChild(actions);
    grid.appendChild(card);
  });
}

async function openEditor(id) {
  const list = id != null ? _lists.find((x) => Number(x.id) === Number(id)) : null;
  const isEdit = id != null;

  // Build the domain options inline because we need to surface
  // their full list in the create modal.
  const domainOpts = (_domains || []).map((d) => ({
    value: String(d.id || d.domain_id || 0),
    label: (d.domain || d.name || ('domain #' + (d.id || d.domain_id))),
  }));

  openFormModal({
    title: isEdit ? ('Edit mailing list: ' + (list.display_name || ('list #' + id))) : 'Create mailing list',
    size: 'lg',
    groups: [
      {
        title: 'Address', subtitle: 'Local part + owning domain. Address and domain are immutable after creation.', layout: 'cols-2',
        fields: [
          { name: 'address', kind: 'text', label: 'Local part', required: true,
            default: isEdit ? list.address : '',
            placeholder: 'all  |  team-frontend  |  announcements',
            help: 'The part before the @ in the delivery address.',
            validate: (v) => (/^[a-z0-9._-]+$/i.test(String(v)) ? null : { error: 'Use letters, digits, dots, dashes, underscores only.' }),
            readValue: (node) => isEdit ? list.address : String(node.value || '').trim() },
          { name: 'domain_id', kind: 'select', label: 'Domain', required: true,
            options: domainOpts, default: isEdit ? String(list.domain_id) : '',
            help: 'The owning domain (must belong to your tenant).',
            readValue: (node) => isEdit ? String(list.domain_id) : String(node.value || '') },
        ],
      },
      {
        title: 'Identity', subtitle: 'Display name + operator description.',
        fields: [
          { name: 'display_name', kind: 'text', label: 'Display name',
            default: list && list.display_name, maxLength: 256,
            help: 'Shown in webmail clients and on the welcome email.' },
          { name: 'description', kind: 'textarea', label: 'Description',
            rows: 3, maxLength: 1024, default: list && list.description,
            placeholder: 'Optional. Visible in the list card.' },
        ],
      },
      {
        title: 'Behaviour', subtitle: 'How the list behaves: who can post, how subscribers are managed, size ceiling.', layout: 'cols-2',
        fields: [
          { name: 'subscription_policy', kind: 'select', label: 'Subscription policy',
            options: SUBSCRIPTION_POLICIES,
            default: (list && list.subscription_policy) || 'closed' },
          { name: 'status', kind: 'select', label: 'Status',
            options: STATUS_OPTIONS,
            default: (list && list.status) || 'active',
            help: '"suspended" rejects new posts; "archived" is read-only.' },
          { name: 'moderation_required', kind: 'switch', label: 'Moderation required',
            default: !!(list && list.moderation_required),
            help: 'Every post must be approved by a moderator before delivery.' },
          { name: 'archive_enabled', kind: 'switch', label: 'Archive messages',
            default: !!(list && list.archive_enabled),
            help: 'Keep a copy of every delivered post in the list archive.' },
          { name: 'max_members', kind: 'number', label: 'Max members (0 = unlimited)',
            default: list ? Number(list.max_members || 0) : 0,
            min: 0, max: 999999,
            help: 'Cap the subscriber count; 0 means no cap.' },
        ],
      },
    ],
    submitLabel: isEdit ? 'Save changes' : 'Create list',
    successMessage: isEdit ? 'Mailing list updated.' : 'Mailing list created.',
    onSubmit: async (values) => {
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/mailing-lists/' + id, {
            display_name: String(values.display_name || '').trim(),
            description: String(values.description || '').trim(),
            subscription_policy: String(values.subscription_policy || 'closed'),
            status: String(values.status || 'active'),
            moderation_required: !!values.moderation_required,
            archive_enabled: !!values.archive_enabled,
            max_members: Number(values.max_members || 0),
          });
        } else {
          if (!values.address || !values.domain_id) {
            return { ok: false, error: 'Local part and domain are required.' };
          }
          await apiPost('/api/v1/admin/mailing-lists', {
            address: String(values.address || '').trim(),
            domain_id: Number(values.domain_id),
            display_name: String(values.display_name || '').trim(),
            description: String(values.description || '').trim(),
            subscription_policy: String(values.subscription_policy || 'closed'),
            status: String(values.status || 'active'),
            moderation_required: !!values.moderation_required,
            archive_enabled: !!values.archive_enabled,
            max_members: Number(values.max_members || 0),
          });
        }
        await refresh();
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
    const r = await apiGet('/api/v1/admin/mailing-lists/' + id + '/members');
    members = (r && r.members) || [];
  } catch (e) {
    toast('Failed to load members: ' + (e.message || e), 'error');
    return;
  }

  const root = el('div', { class: 'form-stack' });
  root.appendChild(el('h4', { class: 'list-card-title', text: 'Current subscribers (' + members.length + ')' }));
  const ul = el('ul', { class: 'kv-list' });
  if (!members.length) ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No subscribers yet.' }));
  members.forEach((m) => {
    const li = el('li', { class: 'kv-item',
      text: (m.address || '') + (m.display_name ? ' (' + m.display_name + ')' : '') + (m.role ? ' [' + m.role + ']' : '') });
    li.appendChild(el('button', {
      class: 'btn xs ghost danger', type: 'button', style: 'float:right',
      text: 'Remove',
      onclick: async () => {
        try {
          await apiDelete('/api/v1/admin/mailing-lists/' + id + '/members/' + (m.id || m.member_id));
          toast('Subscriber removed', 'success', 1400);
          li.remove();
        } catch (e) { toast('Remove failed: ' + (e.message || e), 'error'); }
      },
    }));
    ul.appendChild(li);
  });
  root.appendChild(ul);

  const addWrap = el('div', { style: 'margin-top:14px; padding-top:14px; border-top:1px solid var(--border-1)' });
  addWrap.appendChild(el('h4', { class: 'list-card-title', text: 'Add subscriber' }));
  const inputRow = el('div', { style: 'display:grid; grid-template-columns: 1fr 1fr 110px; gap:8px; margin-top:8px;' });
  const addr = el('input', {
    type: 'email', class: 'ff-input', placeholder: 'email@domain',
    autocomplete: 'off', spellcheck: 'false',
  });
  const name = el('input', {
    type: 'text', class: 'ff-input', placeholder: 'display name (optional)',
    autocomplete: 'off', spellcheck: 'false',
  });
  const role = el('select', { class: 'ff-input ff-select' });
  ['subscriber', 'moderator', 'owner'].forEach((r) => role.appendChild(el('option', { value: r }, r)));
  inputRow.appendChild(addr);
  inputRow.appendChild(name);
  inputRow.appendChild(role);

  const addBtn = el('button', { type: 'button', class: 'btn primary', text: 'Add subscriber' });
  addBtn.style.marginTop = '10px';
  addBtn.addEventListener('click', async () => {
    const a = String(addr.value || '').trim();
    if (!a) { toast('Subscriber email required', 'warn'); return; }
    try {
      await apiPost('/api/v1/admin/mailing-lists/' + id + '/members', {
        address: a,
        display_name: String(name.value || '').trim(),
        role: String(role.value || 'subscriber'),
      });
      toast('Subscriber added', 'success', 1400);
      addr.value = '';
      name.value = '';
    } catch (e) { toast('Add failed: ' + (e.message || e), 'error'); }
  });
  addWrap.appendChild(inputRow);
  addWrap.appendChild(addBtn);
  root.appendChild(addWrap);

  modal({
    title: 'Mailing list members',
    size: 'lg',
    body: root,
    actions: [{ label: 'Done', kind: 'primary', value: 'done' }],
    onAction: async () => true,
  });
}

async function doDelete(id) {
  const l = _lists.find((x) => Number(x.id) === Number(id));
  const desc = l ? (l.address + '@domain#' + l.domain_id) : ('list #' + id);
  const ok = await confirmDanger({
    title: 'Delete mailing list',
    message: 'Delete "' + desc + '" permanently? Subscribers lose delivery to this address; the address is freed for re-creation.',
    confirmLabel: 'Delete',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/mailing-lists/' + id);
    toast('List deleted', 'success', 1400);
    await refresh();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}
