/* =====================================================================
   pages/public-folders.js — Public folder management UI.

   CRUD against /api/v1/admin/public-folders. Public folders are
   shared folders on a mailbox that other mailboxes can subscribe
   to via IMAP. Built on the shared form builder so the create/edit
   modal matches every other admin modal.

   Backend PATCH /api/v1/admin/public-folders/:id covers the
   editable fields (display_name, description, read_only). The
   owner mailbox and folder path are immutable after creation.
   ===================================================================== */

import { el, modal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal } from '../form.js';

let _folders = [];
let _mailboxes = [];

export async function renderPublicFoldersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Public folders' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Shared folders that multiple mailboxes can subscribe to via IMAP. Owner and path are immutable; everything else is editable via PATCH.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-pf-action': 'create', text: 'Create folder' }),
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderPublicFoldersPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'list-card-grid', id: 'pf-grid' });
  wrap.appendChild(grid);

  await refresh();

  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-pf-action]');
    if (!t) return;
    const action = t.getAttribute('data-pf-action');
    if (action === 'create') return openEditor(null);
    if (action === 'edit')   return openEditor(Number(t.getAttribute('data-id')));
    if (action === 'delete') return doDelete(Number(t.getAttribute('data-id')));
  });
  applyAutoDir(wrap);
}

async function refresh() {
  try {
    const [fResp, mResp] = await Promise.all([
      apiGet('/api/v1/admin/public-folders').catch(() => ({ folders: [] })),
      apiGet('/api/v1/mailboxes').catch(() => []),
    ]);
    _folders = (fResp && fResp.folders) || [];
    _mailboxes = Array.isArray(mResp) ? mResp : [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
    _folders = [];
    _mailboxes = [];
  }
  const grid = document.getElementById('pf-grid');
  if (grid) paint(grid, _folders);
}

function paint(grid, list) {
  grid.innerHTML = '';
  if (!list.length) {
    grid.appendChild(el('div', { class: 'list-card-empty' }, [
      el('div', { class: 'list-card-empty-icon', text: '\u2605' }),
      el('h3', { text: 'No public folders defined' }),
      el('p', { text: 'Create a public folder above to share an IMAP folder between mailboxes. Subscribers see the folder under their IMAP namespace via "Public/" prefix.' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-pf-action': 'create', text: 'Create a folder' }),
      ]),
    ]));
    return;
  }
  list.forEach((f) => {
    const card = el('article', { class: 'list-card' });
    card.appendChild(el('header', { class: 'list-card-head' }, [
      el('h4', { class: 'list-card-title', text: f.folder_path || ('folder #' + f.id) }),
      el('div', { class: 'list-card-tag-row' }, [
        el('span', { class: 'list-card-tag', text: f.read_only ? 'read-only' : 'read-write' }),
        el('span', { class: 'list-card-tag', text: (f.member_count || 0) + ' subscribers' }),
      ]),
    ]));
    if (f.display_name || f.description) {
      const desc = f.display_name ? f.display_name : '';
      card.appendChild(el('p', { class: 'list-card-desc', text: desc }));
      if (f.description) card.appendChild(el('p', { class: 'list-card-desc subtle small', text: f.description }));
    }
    const meta = el('div', { class: 'list-card-meta' });
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Owner' }),
      el('span', { class: 'v', text: f.owner_email || ('mailbox #' + f.owner_mailbox_id) }),
    ]));
    meta.appendChild(el('div', { class: 'kv-mini' }, [
      el('span', { class: 'k', text: 'Updated' }),
      el('span', { class: 'v', text: formatDateTime(f.updated_at) }),
    ]));
    card.appendChild(meta);

    const actions = el('div', { class: 'list-card-actions' });
    actions.appendChild(el('button', { class: 'btn xs ghost', 'data-pf-action': 'edit', 'data-id': String(f.id), text: 'Edit' }));
    actions.appendChild(el('button', { class: 'btn xs ghost danger', 'data-pf-action': 'delete', 'data-id': String(f.id), text: 'Delete' }));
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
  const folder = id != null ? _folders.find((x) => Number(x.id) === Number(id)) : null;
  const isEdit = id != null;

  const mailboxOpts = (_mailboxes || []).map((m) => ({
    value: String(m.id || m.mailbox_id || 0),
    label: (m.email || m.address || ('mailbox #' + (m.id || m.mailbox_id))),
  }));

  openFormModal({
    title: isEdit ? ('Edit public folder: ' + (folder.folder_path || ('folder #' + id))) : 'Create public folder',
    size: 'lg',
    groups: [
      {
        title: 'Folder',
        subtitle: 'Owner mailbox + the IMAP folder name. Both are immutable after creation.', layout: 'cols-2',
        fields: [
          { name: 'owner_mailbox_id', kind: 'select', label: 'Owner mailbox', required: true,
            options: mailboxOpts,
            default: isEdit ? String(folder.owner_mailbox_id) : '',
            readValue: (node) => isEdit ? String(folder.owner_mailbox_id) : String(node.value || '') },
          { name: 'folder_path', kind: 'text', label: 'Folder path', required: true,
            default: isEdit ? folder.folder_path : '',
            placeholder: 'Public/Announcements',
            help: 'The IMAP folder name. Conventionally under a top-level "Public/" prefix.',
            validate: (v) => (/^[A-Za-z0-9._/-]+$/.test(String(v)) ? null : { error: 'Use letters, digits, dots, dashes, underscores, forward slashes only.' }),
            readValue: (node) => isEdit ? folder.folder_path : String(node.value || '').trim() },
        ],
      },
      {
        title: 'Identity',
        subtitle: 'How the folder is presented in IMAP clients and admin cards.',
        fields: [
          { name: 'display_name', kind: 'text', label: 'Display name',
            default: folder && folder.display_name, maxLength: 256,
            placeholder: 'Announcements  |  Team-wide' },
          { name: 'description', kind: 'textarea', label: 'Description',
            rows: 3, maxLength: 1024, default: folder && folder.description,
            placeholder: 'Optional. Visible in the folder card and IMAP annotation.' },
        ],
      },
      {
        title: 'Behaviour',
        fields: [
          { name: 'read_only', kind: 'switch', label: 'Read-only folder',
            default: !!(folder && folder.read_only),
            help: 'When enabled, subscribers cannot append / flag messages \u2014 useful for announcements lists.' },
        ],
      },
    ],
    submitLabel: isEdit ? 'Save changes' : 'Create folder',
    successMessage: isEdit ? 'Public folder updated.' : 'Public folder created.',
    onSubmit: async (values) => {
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/public-folders/' + id, {
            display_name: String(values.display_name || '').trim(),
            description: String(values.description || '').trim(),
            read_only: !!values.read_only,
          });
        } else {
          if (!values.owner_mailbox_id || !values.folder_path) {
            return { ok: false, error: 'Owner mailbox and folder path are required.' };
          }
          await apiPost('/api/v1/admin/public-folders', {
            owner_mailbox_id: Number(values.owner_mailbox_id),
            folder_path: String(values.folder_path || '').trim(),
            display_name: String(values.display_name || '').trim(),
            description: String(values.description || '').trim(),
            read_only: !!values.read_only,
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

async function doDelete(id) {
  const f = _folders.find((x) => Number(x.id) === Number(id));
  const desc = f ? (f.folder_path || ('folder #' + id)) : ('folder #' + id);
  const ok = await confirmDanger({
    title: 'Delete public folder',
    message: 'Delete folder "' + desc + '" permanently? Subscribers lose visibility.',
    confirmLabel: 'Delete',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/public-folders/' + id);
    toast('Folder deleted', 'success', 1400);
    await refresh();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}
