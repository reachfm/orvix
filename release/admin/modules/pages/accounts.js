/* =====================================================================
   pages/accounts.js — Mailbox / account management.

   Mirrors the domains page but operates on mailboxes. Wires:
     GET    /api/v1/mailboxes
     GET    /api/v1/mailboxes/:id
     POST   /api/v1/mailboxes         (CSRF)
     PATCH  /api/v1/mailboxes/:id/status  (CSRF)
     PATCH  /api/v1/mailboxes/:id/password (CSRF)
     DELETE /api/v1/mailboxes/:id     (CSRF)
   ===================================================================== */

import { el, table, badge, fmtShortDate, openModal, openDrawer, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAccountsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Accounts' }),
      el('p', { class: 'page-subtitle subtle', text: 'Manage mailboxes. Bulk import lives in Domains → Bulk Import.' }),
    ]),
  ]));

  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'New mailbox',
    onclick: () => openCreate() }));
  wrap.appendChild(actions);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Mailboxes' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  await refresh();
  applyAutoDir(wrap);

  async function refresh() {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    let data;
    try { data = await apiGet('/api/v1/mailboxes'); }
    catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message || 'load failed' })); return; }
    body.innerHTML = '';
    const list = (data && (data.users || data.mailboxes || data)) || [];
    if (!list.length) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
    body.appendChild(table({
      columns: [
        { name: 'e',    label: 'Email', render: (r) => r.email || r.address || '-' },
        { name: 'n',    label: 'Name',  render: (r) => r.name || r.display_name || '-' },
        { name: 'st',   label: 'Status', render: (r) => {
          const s = (r.status || 'active').toLowerCase();
          return badge(s, s === 'active' ? 'good' : (s === 'suspended' || s === 'locked' ? 'bad' : 'neutral'));
        } },
        { name: 'q',    label: 'Quota (MB)', render: (r) => r.quota_mb != null ? String(r.quota_mb) : '-' },
        { name: 'a',    label: '', cellClass: 'actions', render: (r) => {
          const w = el('div', { class: 'row-actions' });
          if (r.id) {
            // mv-action / mb-action: legacy row-action classes
            // asserted by the static-analysis test. We keep them
            // as literal class names so the contracts remain
            // discoverable.
            w.appendChild(el('button', { class: 'btn xs ghost mv-action', type: 'button', text: 'Detail', onclick: () => openDetail(r.id) }));
            w.appendChild(el('button', { class: 'btn xs ghost mb-action', type: 'button', text: r.status === 'suspended' ? 'Resume' : 'Suspend',
              onclick: () => toggleStatus(r.id, r.status) }));
            w.appendChild(el('button', { class: 'btn xs ghost mb-action', type: 'button', text: 'Reset password', onclick: () => doResetPw(r.id) }));
            w.appendChild(el('button', { class: 'btn xs danger mb-action', type: 'button', text: 'Delete', onclick: () => doDelete(r.id) }));
          }
          return w;
        } },
      ],
      rows: list,
    }));
  }
}

function openCreate() {
  openModal({
    title: 'Create mailbox',
    render: (body, foot) => {
      const email = el('input', { type: 'email', placeholder: 'user@example.com', required: 'required', autocomplete: 'off', spellcheck: 'false' });
      const name  = el('input', { type: 'text', placeholder: 'Display name' });
      const pw    = el('input', { type: 'password', placeholder: 'Initial password', required: 'required', autocomplete: 'new-password' });
      const quota = el('input', { type: 'number', placeholder: 'Quota (MB) (optional — class default applied otherwise)' });
      // Account class selector. When selected, the class's
      // defaults (quota, send limits, feature gates) are
      // applied to the mailbox unless overridden by the
      // operator. The class must belong to the same tenant.
      const classSelect = el('select', { id: 'mb_class_id' });
      classSelect.appendChild(el('option', { value: '0' }, 'No class (defaults to 1024MB / no extra gates)'));
      // Lazy-load classes; if the call fails the dropdown
      // still has the "no class" option so the form is usable.
      apiGet('/api/v1/admin/account-classes').then((r) => {
        const classes = (r && r.classes) || [];
        classes.forEach((c) => {
          classSelect.appendChild(el('option', {
            value: String(c.id),
            title: 'max ' + c.max_quota_mb + 'MB; send ' + c.max_send_per_hour + '/h; recv ' + c.max_recv_per_hour + '/h',
          }, c.name + (c.is_admin_class ? ' (built-in)' : '')));
        });
      }).catch(() => { /* leave defaults */ });
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Email'), email]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Name'), name]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Password'), pw]));
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Quota (MB)'), quota]));
      body.appendChild(el('div', { class: 'form-row' }, [
        el('label', { for: 'mb_class_id' }, 'Account class (optional)'),
        classSelect,
      ]));
      body.appendChild(el('p', { class: 'subtle small',
        text: 'Pick an account class to apply its quota / send-limit defaults. See /accounts/classes for the full list.' }));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create', onclick: async () => {
        if (!email.value || !pw.value) { toast('Email and password required', 'error'); return; }
        try {
          await apiPost('/api/v1/mailboxes', {
            email: email.value,
            name: name.value,
            password: pw.value,
            quota_mb: Number(quota.value) || 0,
            class_id: Number(classSelect.value || 0) || 0,
          });
          // Important: never echo the password back to the operator.
          pw.value = '';
          toast('Mailbox created', 'success', 1800);
          location.reload();
        } catch (e) { toast((e && e.message) || 'create failed', 'error'); }
      } }));
    },
  });
}

async function toggleStatus(id, current) {
  const next = current === 'suspended' ? 'active' : 'suspended';
  const ok = await confirmDanger({ title: next === 'suspended' ? 'Suspend mailbox' : 'Resume mailbox', message: 'Switch mailbox to ' + next + '?', confirmLabel: next });
  if (!ok) return;
  try { await apiPatch('/api/v1/mailboxes/' + encodeURIComponent(id) + '/status', { status: next }); toast('Status updated', 'success', 1800); setTimeout(() => location.reload(), 400); }
  catch (e) { toast((e && e.message) || 'update failed', 'error'); }
}

async function doResetPw(id) {
  const pw = prompt('New password (will not be displayed again):');
  if (!pw) return;
  try { await apiPatch('/api/v1/mailboxes/' + encodeURIComponent(id) + '/password', { password: pw }); toast('Password updated', 'success', 1800); }
  catch (e) { toast((e && e.message) || 'reset failed', 'error'); }
}

async function doDelete(id) {
  const ok = await confirmDanger({ title: 'Delete mailbox', message: 'Delete this mailbox? Mail may be retained by policy.', confirmLabel: 'Delete' });
  if (!ok) return;
  try { await apiDelete('/api/v1/mailboxes/' + encodeURIComponent(id)); toast('Mailbox deleted', 'success', 1800); setTimeout(() => location.reload(), 400); }
  catch (e) { toast((e && e.message) || 'delete failed', 'error'); }
}

async function openDetail(id) {
  openDrawer({
    title: 'Mailbox ' + id, eyebrow: 'Account',
    render: async (body) => {
      body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
      let data;
      try { data = await apiGet('/api/v1/mailboxes/' + encodeURIComponent(id)); }
      catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message })); return; }
      body.innerHTML = '';
      const dl = el('dl', { class: 'kv' });
      ['id','email','name','status','quota_mb','created_at'].forEach((k) => {
        if (data && data[k] != null) { dl.appendChild(el('dt', { text: k })); dl.appendChild(el('dd', { class: 'kv-v', text: String(data[k]) })); }
      });
      body.appendChild(dl);
    },
  });
}
