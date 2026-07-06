/* =====================================================================
   pages/admin-users.js — Administrative users management.

   Wires:
     GET    /api/v1/admin/admin-users
     POST   /api/v1/admin/admin-users                    (CSRF)
     PATCH  /api/v1/admin/admin-users/:id/password       (CSRF)
     PATCH  /api/v1/admin/admin-users/:id/status         (CSRF)
     DELETE /api/v1/admin/admin-users/:id                (CSRF)

   Backend contract (asserted by admin_users_test.go):
     - 401 if no auth
     - 409 if a user tries to disable / delete their own account
     - 403 if the deletion would leave the tenant with zero active
       superadmins
     - 500 messages are sanitised — no raw SQL/driver errors
     - 400 on password shorter than 8 chars
     - 400 on role other than admin / superadmin
     - audit log is written for every mutation

   The UI mirrors these contracts:
     - Disable / Delete buttons are disabled (and labelled) for the
       currently signed-in account.
     - Disable button is disabled for the last active superadmin row.
     - 409 / 403 errors from the backend are surfaced verbatim so
       the operator sees the real reason, not a generic toast.
   ===================================================================== */

import { el, badge, openModal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';
import { getProfile } from '../state.js';

export async function renderAdminUsersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Administrative users' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Create, disable, reset password, and remove admin / staff accounts.' }),
    ]),
  ]));

  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Add admin user',
    onclick: () => openCreate() }));
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
  let r;
  try { r = await apiGet('/api/v1/admin/admin-users'); }
  catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (e && e.message) || 'load failed' }));
    body.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Retry',
      onclick: () => refresh(body) }));
    return;
  }
  body.innerHTML = '';
  const users = (r && r.users) || [];
  if (!users.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No admin users found.' }));
    return;
  }
  const me = getProfile() || {};
  // A user is "self" when either the row id matches the profile id
  // or the email matches. The profile may carry the id under
  // .id or .user_id depending on the backend revision.
  const myId   = me.id || me.user_id || null;
  const myMail = (me.email || '').toLowerCase();
  // Count active superadmins to gate the last-superadmin case in UI.
  const activeSuperadmins = users.filter((u) => u.role === 'superadmin' && u.active).length;

  const tbl = el('table', { class: 'data-table' }, [
    el('thead', null, el('tr', null, [
      el('th', { text: 'Email' }),
      el('th', { text: 'Role' }),
      el('th', { text: 'Status' }),
      el('th', { text: 'Created' }),
      el('th', { class: 'actions', text: 'Actions' }),
    ])),
    el('tbody', null, users.map((u) => {
      const isSelf = (myId && String(u.id) === String(myId)) ||
                     (myMail && (u.email || '').toLowerCase() === myMail);
      const isLastActiveSuperadmin = u.role === 'superadmin' && u.active && activeSuperadmins <= 1;
      const disableBtn = el('button', { class: 'btn xs ghost', type: 'button',
        text: u.active ? 'Disable' : 'Enable',
        title: isSelf ? 'You cannot disable your own account'
          : (isLastActiveSuperadmin ? 'Cannot disable the last active superadmin' : ''),
        disabled: (isSelf || (isLastActiveSuperadmin && u.active)) ? 'disabled' : '',
        onclick: () => { if (!isSelf && !(isLastActiveSuperadmin && u.active)) toggleStatus(u.id, u.active, body); } });
      const deleteBtn = el('button', { class: 'btn xs danger', type: 'button',
        text: 'Delete',
        title: isSelf ? 'You cannot delete your own account'
          : (isLastActiveSuperadmin ? 'Cannot delete the last active superadmin' : ''),
        disabled: isSelf ? 'disabled' : '',
        onclick: () => { if (!isSelf) doDelete(u.id, u.email, body); } });
      return el('tr', null, [
        el('td', { text: (u.email || '-') + (isSelf ? ' (you)' : '') }),
        el('td', null, badge(u.role || 'admin', u.role === 'superadmin' ? 'info' : 'neutral')),
        el('td', null, badge(u.active ? 'active' : 'disabled', u.active ? 'good' : 'bad')),
        el('td', { text: (u.created_at || '').slice(0, 10) || '-' }),
        el('td', { class: 'actions' }, el('div', { class: 'row-actions' }, [
          el('button', { class: 'btn xs ghost', type: 'button', text: 'Reset password',
            onclick: () => doResetPw(u.id) }),
          disableBtn,
          deleteBtn,
        ])),
      ]);
    })),
  ]);
  body.appendChild(tbl);
  body.appendChild(el('p', { class: 'subtle small',
    text: 'You cannot disable or delete your own account, and the last active superadmin cannot be disabled or deleted. Both are enforced server-side; UI buttons reflect the contract.' }));
}

function openCreate() {
  openModal({
    title: 'Create admin user',
    render: (body, foot) => {
      const email = el('input', { type: 'email', placeholder: 'admin@example.com', required: 'required', autocomplete: 'off' });
      const pw = el('input', { type: 'password', placeholder: 'Password (min 8 chars)', required: 'required', autocomplete: 'new-password', minlength: '8' });
      const role = el('select', { required: 'required' }, [
        el('option', { value: 'admin' }, 'Admin'),
        el('option', { value: 'superadmin' }, 'Superadmin'),
      ]);
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Email'), email]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Password (min 8 chars)'), pw]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Role'), role]));
      body.appendChild(el('p', { class: 'subtle small',
        text: 'The new user is created active. They will need to log in before their first MFA setup.' }));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create', onclick: async () => {
        const email_v = (email.value || '').trim();
        const pw_v    = pw.value || '';
        if (!email_v || !pw_v) { toast('Email and password required', 'error'); return; }
        if (pw_v.length < 8)    { toast('Password must be at least 8 characters', 'error'); return; }
        try {
          await apiPost('/api/v1/admin/admin-users', { email: email_v, password: pw_v, role: role.value });
          toast('Admin user created', 'success', 1800);
          document.querySelector('.modal-overlay').remove();
          // Refresh in place (avoid a full page reload so the
          // session is preserved and the new row appears immediately).
          const body = document.querySelector('.page-inner .panel .panel-body');
          if (body) refresh(body);
        } catch (e) { toast((e && e.message) || 'create failed', 'error'); }
      } }));
    },
  });
}

function doResetPw(id) {
  openModal({
    title: 'Reset password',
    render: (body, foot) => {
      const pw = el('input', { type: 'password', placeholder: 'New password (min 8 chars)', required: 'required', autocomplete: 'new-password', minlength: '8' });
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'New password'), pw]));
      body.appendChild(el('p', { class: 'subtle small',
        text: 'The new password is never echoed back. The user must log in with it on next sign-in.' }));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Reset', onclick: async () => {
        const pw_v = pw.value || '';
        if (!pw_v) { toast('Password required', 'error'); return; }
        if (pw_v.length < 8) { toast('Password must be at least 8 characters', 'error'); return; }
        try {
          await apiPatch('/api/v1/admin/admin-users/' + encodeURIComponent(id) + '/password', { password: pw_v });
          toast('Password updated', 'success', 1800);
          document.querySelector('.modal-overlay').remove();
        } catch (e) { toast((e && e.message) || 'reset failed', 'error'); }
      } }));
    },
  });
}

async function toggleStatus(id, currentlyActive, body) {
  const next = !currentlyActive;
  const label = next ? 'Enable' : 'Disable';
  const ok = await confirmDanger({ title: label + ' admin user', message: label + ' this admin user?', confirmLabel: label });
  if (!ok) return;
  try {
    await apiPatch('/api/v1/admin/admin-users/' + encodeURIComponent(id) + '/status', { active: next });
    toast('Status updated', 'success', 1800);
    refresh(body);
  } catch (e) { toast((e && e.message) || 'update failed', 'error'); }
}

async function doDelete(id, email, body) {
  const ok = await confirmDanger({ title: 'Delete admin user',
    message: 'Delete ' + (email || ('user #' + id)) + '? This cannot be undone.',
    confirmLabel: 'Delete' });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/admin-users/' + encodeURIComponent(id));
    toast('Admin user deleted', 'success', 1800);
    refresh(body);
  } catch (e) { toast((e && e.message) || 'delete failed', 'error'); }
}
