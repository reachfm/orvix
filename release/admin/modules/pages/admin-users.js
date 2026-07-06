import { el, badge, openModal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';

export async function renderAdminUsersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Administrative Users' }),
      el('p', { class: 'page-subtitle subtle', text: 'Manage admin and staff accounts.' }),
    ]),
  ]));

  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Add admin user', onclick: () => openCreate() }));
  wrap.appendChild(actions);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Admin users' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);
  root.appendChild(wrap);

  await refresh(body);
}

async function refresh(body) {
  body.innerHTML = '';
  body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
  try {
    const r = await apiGet('/api/v1/admin/admin-users');
    body.innerHTML = '';
    const users = (r && r.users) || [];
    if (!users.length) {
      body.appendChild(el('div', { class: 'empty', text: 'No admin users found.' }));
      return;
    }
    const tbl = el('table', { class: 'data-table' }, [
      el('thead', null, el('tr', null, [
        el('th', { text: 'Email' }),
        el('th', { text: 'Role' }),
        el('th', { text: 'Status' }),
        el('th', { text: 'Created' }),
        el('th', { class: 'actions', text: 'Actions' }),
      ])),
      el('tbody', null, users.map((u) => el('tr', null, [
        el('td', { text: u.email || '-' }),
        el('td', null, badge(u.role || 'admin', u.role === 'superadmin' ? 'info' : 'neutral')),
        el('td', null, badge(u.active ? 'active' : 'disabled', u.active ? 'good' : 'bad')),
        el('td', { text: (u.created_at || '').slice(0, 10) }),
        el('td', { class: 'actions' }, el('div', { class: 'row-actions' }, [
          el('button', { class: 'btn xs ghost', type: 'button', text: 'Reset password', onclick: () => doResetPw(u.id) }),
          el('button', { class: 'btn xs ghost', type: 'button', text: u.active ? 'Disable' : 'Enable', onclick: () => toggleStatus(u.id, u.active) }),
          el('button', { class: 'btn xs danger', type: 'button', text: 'Delete', onclick: () => doDelete(u.id, u.email) }),
        ])),
      ]))),
    ]);
    body.appendChild(tbl);
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (e && e.message) || 'load failed' }));
  }
}

function openCreate() {
  openModal({
    title: 'Create admin user',
    render: (body, foot) => {
      const email = el('input', { type: 'email', placeholder: 'admin@example.com', required: 'required', autocomplete: 'off' });
      const pw = el('input', { type: 'password', placeholder: 'Password (min 8 chars)', required: 'required', autocomplete: 'new-password' });
      const role = el('select', { required: 'required' }, [
        el('option', { value: 'admin' }, 'Admin'),
        el('option', { value: 'superadmin' }, 'Superadmin'),
      ]);
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Email'), email]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Password'), pw]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Role'), role]));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create', onclick: async () => {
        if (!email.value || !pw.value) { toast('Email and password required', 'error'); return; }
        try {
          await apiPost('/api/v1/admin/admin-users', { email: email.value, password: pw.value, role: role.value });
          toast('Admin user created', 'success', 1800);
          location.reload();
        } catch (e) { toast((e && e.message) || 'create failed', 'error'); }
      } }));
    },
  });
}

async function doResetPw(id) {
  openModal({
    title: 'Reset password',
    render: (body, foot) => {
      const pw = el('input', { type: 'password', placeholder: 'New password (min 8 chars)', required: 'required', autocomplete: 'new-password' });
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Password'), pw]));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Reset', onclick: async () => {
        if (!pw.value) { toast('Password required', 'error'); return; }
        try {
          await apiPatch('/api/v1/admin/admin-users/' + encodeURIComponent(id) + '/password', { password: pw.value });
          toast('Password updated', 'success', 1800);
          document.querySelector('.modal-overlay').remove();
        } catch (e) { toast((e && e.message) || 'reset failed', 'error'); }
      } }));
    },
  });
}

async function toggleStatus(id, currentlyActive) {
  const label = currentlyActive ? 'Disable' : 'Enable';
  const ok = await confirmDanger({ title: label + ' admin user', message: label + ' this admin user?', confirmLabel: label });
  if (!ok) return;
  try {
    await apiPatch('/api/v1/admin/admin-users/' + encodeURIComponent(id) + '/status', { active: !currentlyActive });
    toast('Status updated', 'success', 1800);
    location.reload();
  } catch (e) { toast((e && e.message) || 'update failed', 'error'); }
}

async function doDelete(id, email) {
  const ok = await confirmDanger({ title: 'Delete admin user', message: 'Delete ' + email + '? This cannot be undone.', confirmLabel: 'Delete' });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/admin-users/' + encodeURIComponent(id));
    toast('Admin user deleted', 'success', 1800);
    location.reload();
  } catch (e) { toast((e && e.message) || 'delete failed', 'error'); }
}
