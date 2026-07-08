/* =====================================================================
   pages/ftp-backup.js — FTP / SFTP backup target UI.

   Wires:
     GET    /api/v1/admin/backup-targets            → list
     POST   /api/v1/admin/backup-targets            → create
     PATCH  /api/v1/admin/backup-targets/:id        → update
     DELETE /api/v1/admin/backup-targets/:id        → delete
     POST   /api/v1/admin/backup-targets/:id/test  → connection test

   Secrets are NEVER echoed. The form accepts a password
   (encrypted via the backend before being written) and
   the listing only reports `has_secret: true/false`.
   A typed-confirm gate guards destructive deletes.
   ===================================================================== */

import { el, modal, toast, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderFtpBackupPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'FTP / SFTP backup target' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Configure an external target to mirror finished archives. Connections are probed on save; passwords are stored encrypted and never returned.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ft-action': 'create',
        text: 'Create target' }),
    ]),
  ]));
  root.appendChild(wrap);

  const honest = el('div', { class: 'banner banner-info' }, [
    el('span', { class: 'banner-text', text:
      'Configuration is persisted today. The transfer post-processor is not yet wired in this build — the runtime will pick up these targets once the upload step lands.' }),
  ]);
  wrap.appendChild(honest);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Targets' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  panel.appendChild(body);
  wrap.appendChild(panel);

  let list = [];
  try {
    const r = await apiGet('/api/v1/admin/backup-targets');
    list = (r && r.targets) || [];
    if (r && r.honest_note) {
      honest.innerHTML = '';
      honest.appendChild(el('span', { class: 'banner-text', text: r.honest_note }));
    }
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: 'Failed to load: ' + (e.message || e) }));
    applyAutoDir(wrap);
    return;
  }
  paint(body, list);

  wrap.addEventListener('click', async (ev) => {
    const t = ev.target.closest('[data-ft-action]');
    if (!t) return;
    const a = t.getAttribute('data-ft-action');
    if (a === 'create') return openForm(body, list, null);
    const m = a && a.match(/^(edit|delete|test):(\d+)$/);
    if (m) {
      if (m[1] === 'edit') return openForm(body, list, Number(m[2]));
      if (m[1] === 'delete') return doDelete(body, list, Number(m[2]));
      if (m[1] === 'test') return doTest(body, list, Number(m[2]));
    }
  });

  applyAutoDir(wrap);
}

async function openForm(body, list, id) {
  const isEdit = id != null;
  const r = list.find((x) => x.id === id) || {};
  const form = el('div', { class: 'form-stack' });
  form.appendChild(textField('Name', 'name', r.name || ''));
  form.appendChild(select('Kind', 'kind', r.kind || 'ftp', ['ftp', 'sftp']));
  form.appendChild(textField('Host', 'host', r.host || ''));
  form.appendChild(numberField('Port', 'port', r.port || (r.kind === 'sftp' ? 22 : 21)));
  form.appendChild(textField('Username', 'username', r.username || ''));
  form.appendChild(passwordField(isEdit ? 'New password (leave empty to keep)' : 'Password', 'password', ''));
  form.appendChild(textField('Remote path (default: /)', 'path', r.path || '/'));
  if (isEdit && r.kind === 'sftp') {
    form.appendChild(checkField('Clear stored password (typed-confirm at save)', 'clear_secret', false));
    form.appendChild(textField('Private key path (sftp only)', 'private_key_path', r.private_key_path || ''));
  }
  form.appendChild(checkField('Enabled', 'enabled', r.enabled === true));
  form.appendChild(checkField('Verify hostname (TLS / SSH fingerprint pinning)', 'verify_hostname', r.verify_hostname !== false));
  form.appendChild(textArea('Note', 'note', r.note || '', 3));
  modal({
    title: isEdit ? ('Edit backup target #' + id) : 'Create backup target',
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: isEdit ? 'Save' : 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return false;
      const payload = {
        name: readField(form, 'name'),
        kind: readField(form, 'kind'),
        host: readField(form, 'host'),
        port: Number(readField(form, 'port') || 0),
        username: readField(form, 'username'),
        password: readField(form, 'password'),
        private_key_path: readField(form, 'private_key_path'),
        path: readField(form, 'path'),
        enabled: readCheck(form, 'enabled'),
        verify_hostname: readCheck(form, 'verify_hostname'),
        note: readField(form, 'note'),
        clear_secret: readCheck(form, 'clear_secret'),
      };
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/backup-targets/' + id, payload);
          toast('Target updated', 'success');
        } else {
          await apiPost('/api/v1/admin/backup-targets', payload);
          toast('Target created', 'success');
        }
        const r2 = await apiGet('/api/v1/admin/backup-targets');
        list = (r2 && r2.targets) || [];
        paint(body, list);
        return true;
      } catch (e) {
        toast('Save failed: ' + (e.message || e), 'error', 6000);
        return false;
      }
    },
  });
}

async function doTest(body, list, id) {
  try {
    const r = await apiPost('/api/v1/admin/backup-targets/' + id + '/test', {});
    const msg = 'Test: ' + (r.status || '?') + ' — ' + (r.message || '');
    toast(msg, r.status === 'ok' ? 'success' : 'warn', 6000);
    const r2 = await apiGet('/api/v1/admin/backup-targets');
    list = (r2 && r2.targets) || [];
    paint(body, list);
  } catch (e) {
    toast('Test failed: ' + (e.message || e), 'error', 6000);
  }
}

async function doDelete(body, list, id) {
  const ok = await confirmDanger({
    title: 'Remove backup target',
    message: 'The encrypted password and target configuration will be permanently removed.',
    confirmLabel: 'Remove',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/backup-targets/' + id);
    toast('Target removed', 'success');
    const r2 = await apiGet('/api/v1/admin/backup-targets');
    list = (r2 && r2.targets) || [];
    paint(body, list);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error', 6000);
  }
}

function paint(body, list) {
  body.innerHTML = '';
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No backup targets yet. Use "Create target" to add one.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Name' }),
    el('th', { text: 'Kind' }),
    el('th', { text: 'Host:Port' }),
    el('th', { text: 'Username' }),
    el('th', { text: 'Path' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: 'Password' }),
    el('th', { text: 'Last test' }),
    el('th', { text: 'Actions' }),
  ])));
  const tb = el('tbody');
  list.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: r.name }),
      el('td', { text: r.kind }),
      el('td', { text: r.host + ':' + r.port }),
      el('td', { text: r.username || '-' }),
      el('td', { text: r.path || '/' }),
      el('td', { class: 'kv-v', text: r.enabled ? 'yes' : 'no' }),
      el('td', { class: 'kv-v', text: r.has_secret ? 'configured' : 'missing' }),
      el('td', { text: ((r.last_test_status || 'never') + (r.last_test_at ? ' (' + r.last_test_at + ')' : '')) }),
      el('td', null, [
        el('button', { class: 'btn xs ghost', 'data-ft-action': 'test:' + r.id, text: 'Test' }),
        el('button', { class: 'btn xs ghost', 'data-ft-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-ft-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function textField(label, name, value) {
  const id = 'ft_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input', autocomplete: 'off', value: value || '' }),
  ]);
}
function passwordField(label, name, value) {
  const id = 'ft_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'password', class: 'input', autocomplete: 'new-password', value: value || '' }),
  ]);
}
function numberField(label, name, value) {
  const id = 'ft_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'number', class: 'input', value: String(value || 0), min: '0' }),
  ]);
}
function textArea(label, name, value, rows) {
  const id = 'ft_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('textarea', { id, name, class: 'input', rows: rows || 3 }, value || ''),
  ]);
}
function checkField(label, name, value) {
  const id = 'ft_' + name;
  return el('label', { class: 'field check', for: id }, [
    el('input', { id, name, type: 'checkbox', checked: value ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function select(label, name, value, options) {
  const id = 'ft_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('select', { id, name, class: 'input' },
      options.map((o) => el('option', { value: o, selected: o === value ? 'selected' : null }, o))),
  ]);
}
function readField(scope, name) {
  const n = scope.querySelector('[name="' + name + '"]');
  return n ? (n.value || '') : '';
}
function readCheck(scope, name) {
  const n = scope.querySelector('[name="' + name + '"]');
  return n ? !!n.checked : false;
}
