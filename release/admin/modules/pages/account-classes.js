/* =====================================================================
   pages/account-classes.js — Service-class management UI.

   Reads/writes /api/v1/admin/account-classes. The page renders
   one card per class with its quota / send-limit / allow-flags,
   and exposes "Create class" / "Edit" / "Delete" actions. The
   built-in admin class is read-only and the UI marks it so.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAccountClassesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Account classes' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Service classes assigned to mailboxes. Built-in classes cannot be modified.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ac-action': 'create',
        text: 'Create class' }),
    ]),
  ]));

  const grid = el('div', { class: 'card-grid three' });
  wrap.appendChild(grid);
  root.appendChild(wrap);

  let list = [];
  try {
    const resp = await apiGet('/api/v1/admin/account-classes');
    list = (resp && resp.classes) || [];
  } catch (e) {
    toast('Failed to load account classes: ' + (e.message || e), 'error');
  }
  paint(grid, list);

  // Delegated handlers.
  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-ac-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-ac-action');
    if (action === 'create') return openCreate(grid, list);
    if (action === 'edit') return openEdit(grid, list, Number(tEl.getAttribute('data-id')));
    if (action === 'delete') return doDelete(grid, list, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(grid, list) {
  grid.innerHTML = '';
  if (!list.length) {
    grid.appendChild(el('div', { class: 'empty panel', text: 'No account classes defined yet.' }));
    return;
  }
  list.forEach((c) => {
    const card = el('section', { class: 'panel account-class-card' });
    card.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: c.name + (c.is_admin_class ? ' (built-in)' : '') }),
      el('span', { class: 'badge tag',
        text: c.allow_webmail ? 'webmail on' : 'webmail off' }),
    ]));
    const dl = el('dl', { class: 'kv' });
    kv(dl, 'Default quota', c.default_quota_mb + ' MB');
    kv(dl, 'Max quota', c.max_quota_mb + ' MB');
    kv(dl, 'Send limit', c.max_send_per_hour + ' / h');
    kv(dl, 'Recv limit', c.max_recv_per_hour + ' / h');
    kv(dl, 'IMAP', bool(c.allow_imap));
    kv(dl, 'POP3', bool(c.allow_pop3));
    kv(dl, 'JMAP', bool(c.allow_jmap));
    kv(dl, 'External forwarding', bool(c.allow_external_forwarding));
    card.appendChild(el('div', { class: 'panel-body' }, dl));
    if (!c.is_admin_class) {
      card.appendChild(el('div', { class: 'panel-foot' }, [
        el('button', { class: 'btn ghost', 'data-ac-action': 'edit', 'data-id': String(c.id),
          text: 'Edit' }),
        el('button', { class: 'btn ghost danger', 'data-ac-action': 'delete', 'data-id': String(c.id),
          text: 'Delete' }),
      ]));
    } else {
      card.appendChild(el('div', { class: 'panel-foot subtle', text: 'Built-in — read-only' }));
    }
    grid.appendChild(card);
  });
}

function kv(dl, k, v) {
  dl.appendChild(el('dt', { text: k }));
  dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
}
function bool(b) { return b ? 'yes' : 'no'; }

async function openCreate(grid, list) {
  const form = buildForm();
  modal({
    title: 'Create account class',
    body: form.root,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      if (!payload.name) { toast('Name is required', 'warn'); return false; }
      try {
        await apiPost('/api/v1/admin/account-classes', payload);
        toast('Account class created', 'success');
        const resp = await apiGet('/api/v1/admin/account-classes');
        list = (resp && resp.classes) || [];
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
  const c = list.find((x) => x.id === id);
  if (!c) return;
  const form = buildForm(c);
  modal({
    title: 'Edit account class: ' + c.name,
    body: form.root,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Save', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      try {
        await apiPatch('/api/v1/admin/account-classes/' + id, payload);
        toast('Account class updated', 'success');
        const resp = await apiGet('/api/v1/admin/account-classes');
        list = (resp && resp.classes) || [];
        paint(grid, list);
        return true;
      } catch (e) {
        toast('Update failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function doDelete(grid, list, id) {
  const c = list.find((x) => x.id === id);
  if (!c) return;
  if (!confirm('Delete account class "' + c.name + '"? This cannot be undone.')) return;
  try {
    await apiDelete('/api/v1/admin/account-classes/' + id);
    toast('Account class deleted', 'success');
    const resp = await apiGet('/api/v1/admin/account-classes');
    list = (resp && resp.classes) || [];
    paint(grid, list);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error');
  }
}

function buildForm(c) {
  c = c || {};
  const root = el('div', { class: 'form-stack' });
  root.appendChild(field('Name', 'name', c.name || ''));
  root.appendChild(field('Description', 'description', c.description || ''));
  root.appendChild(number('Default quota (MB)', 'default_quota_mb', c.default_quota_mb || 1024));
  root.appendChild(number('Max quota (MB)', 'max_quota_mb', c.max_quota_mb || 5120));
  root.appendChild(number('Send limit / hour', 'max_send_per_hour', c.max_send_per_hour || 500));
  root.appendChild(number('Recv limit / hour', 'max_recv_per_hour', c.max_recv_per_hour || 5000));
  root.appendChild(checkbox('Allow IMAP', 'allow_imap', c.allow_imap !== false));
  root.appendChild(checkbox('Allow POP3', 'allow_pop3', c.allow_pop3 !== false));
  root.appendChild(checkbox('Allow JMAP', 'allow_jmap', c.allow_jmap !== false));
  root.appendChild(checkbox('Allow webmail', 'allow_webmail', c.allow_webmail !== false));
  root.appendChild(checkbox('Allow external forwarding', 'allow_external_forwarding', c.allow_external_forwarding !== false));
  return { root, read: () => root };
}

function field(label, name, val) {
  const id = 'f_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input', value: val }),
  ]);
}
function number(label, name, val) {
  const id = 'f_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'number', class: 'input', value: String(val), min: '0' }),
  ]);
}
function checkbox(label, name, val) {
  const id = 'f_' + name;
  return el('label', { class: 'field check', for: id }, [
    el('input', { id, name, type: 'checkbox', checked: val ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function readForm(form) {
  const out = {};
  form.read().querySelectorAll('input').forEach((inp) => {
    if (!inp.name) return;
    if (inp.type === 'checkbox') out[inp.name] = inp.checked;
    else if (inp.type === 'number') out[inp.name] = Number(inp.value || 0);
    else out[inp.name] = inp.value;
  });
  return out;
}