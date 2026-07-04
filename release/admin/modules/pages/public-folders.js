/* =====================================================================
   pages/public-folders.js — Public folder management UI.

   CRUD against /api/v1/admin/public-folders. Public folders are
   shared folders on a mailbox that other mailboxes can subscribe
   to. The page renders one card per folder with its owner and
   read-only flag.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderPublicFoldersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Public folders' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Shared folders that multiple mailboxes can subscribe to.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-pf-action': 'create',
        text: 'Create folder' }),
    ]),
  ]));
  const grid = el('div', { class: 'card-grid two' });
  wrap.appendChild(grid);
  root.appendChild(wrap);

  let folders = [];
  let mailboxes = [];
  try {
    const [fResp, mResp] = await Promise.all([
      apiGet('/api/v1/admin/public-folders'),
      apiGet('/api/v1/mailboxes'),
    ]);
    folders = (fResp && fResp.folders) || [];
    mailboxes = Array.isArray(mResp) ? mResp : [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
  }
  paint(grid, folders);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-pf-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-pf-action');
    if (action === 'create') return openCreate(grid, folders, mailboxes);
    if (action === 'delete') return doDelete(grid, folders, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(grid, list) {
  grid.innerHTML = '';
  if (!list.length) {
    grid.appendChild(el('div', { class: 'empty panel',
      text: 'No public folders defined.' }));
    return;
  }
  list.forEach((f) => {
    const card = el('section', { class: 'panel' });
    card.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: f.folder_path }),
      el('span', { class: 'badge tag', text: f.read_only ? 'read-only' : 'read-write' }),
    ]));
    const dl = el('dl', { class: 'kv' });
    kv(dl, 'Owner', f.owner_email || ('#' + f.owner_mailbox_id));
    kv(dl, 'Display name', f.display_name || '—');
    kv(dl, 'Members', String(f.member_count || 0));
    if (f.description) kv(dl, 'Description', f.description);
    card.appendChild(el('div', { class: 'panel-body' }, dl));
    card.appendChild(el('div', { class: 'panel-foot' }, [
      el('button', { class: 'btn ghost danger', 'data-pf-action': 'delete',
        'data-id': String(f.id), text: 'Delete' }),
    ]));
    grid.appendChild(card);
  });
}

function kv(dl, k, v) {
  dl.appendChild(el('dt', { text: k }));
  dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
}

function buildForm(mailboxes) {
  const root = el('div', { class: 'form-stack' });
  const sel = el('select', { class: 'input', name: 'owner_mailbox_id' });
  mailboxes.forEach((m) => {
    sel.appendChild(el('option', { value: String(m.id || m.mailbox_id || 0),
      text: m.email || m.address || '' }));
  });
  root.appendChild(el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: 'Owner mailbox' }), sel,
  ]));
  root.appendChild(inputField('Folder path (e.g. Public/Announcements)', 'folder_path', ''));
  root.appendChild(inputField('Display name', 'display_name', ''));
  root.appendChild(inputField('Description', 'description', ''));
  root.appendChild(checkbox('Read-only', 'read_only', false));
  return root;
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
  const sel = root.querySelector('select[name="owner_mailbox_id"]');
  if (sel) out.owner_mailbox_id = Number(sel.value);
  return out;
}

async function openCreate(grid, folders, mailboxes) {
  const form = buildForm(mailboxes);
  modal({
    title: 'Create public folder',
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      if (!payload.folder_path || !payload.owner_mailbox_id) {
        toast('Owner and folder path required', 'warn'); return false;
      }
      try {
        await apiPost('/api/v1/admin/public-folders', payload);
        toast('Public folder created', 'success');
        const resp = await apiGet('/api/v1/admin/public-folders');
        folders = (resp && resp.folders) || [];
        paint(grid, folders);
        return true;
      } catch (e) {
        toast('Create failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function doDelete(grid, folders, id) {
  const f = folders.find((x) => x.id === id);
  if (!f) return;
  if (!confirm('Delete public folder "' + f.folder_path + '"?')) return;
  try {
    await apiDelete('/api/v1/admin/public-folders/' + id);
    toast('Public folder deleted', 'success');
    const resp = await apiGet('/api/v1/admin/public-folders');
    folders = (resp && resp.folders) || [];
    paint(grid, folders);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error');
  }
}