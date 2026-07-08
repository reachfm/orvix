/* =====================================================================
   pages/migration-sources.js — Migration source CRUD UI.

   Wires:
     GET    /api/v1/admin/migration-sources           → list
     POST   /api/v1/admin/migration-sources           → create
     PATCH  /api/v1/admin/migration-sources/:id       → update
     DELETE /api/v1/admin/migration-sources/:id       → delete
     POST   /api/v1/admin/migration-sources/:id/test  → connection test

   Passwords are NEVER echoed. The form accepts a
   password (encrypted by the backend) and the listing
   only reports `has_secret: true/false`. The connection
   test does a TCP (+ optional TLS handshake) probe and
   records the result on the row.
   ===================================================================== */

import { el, modal, toast } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderMigrationSourcesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Migration sources' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'External IMAP / JMAP / POP3 servers to migrate from. The job picker reads from this list. Passwords are stored encrypted and never returned.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ms-action': 'create',
        text: 'Create source' }),
    ]),
  ]));
  root.appendChild(wrap);

  const note = el('div', { class: 'banner banner-info' });
  note.appendChild(el('span', { class: 'banner-text', text:
    'The migration engine is not initialised in dev. Creating / editing / testing sources works regardless — they are persisted and a future migration job will pick them up.' }));
  wrap.appendChild(note);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Sources' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  panel.appendChild(body);
  wrap.appendChild(panel);

  let list = [];
  try {
    const r = await apiGet('/api/v1/admin/migration-sources');
    list = (r && r.sources) || [];
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: 'Failed to load: ' + (e.message || e) }));
    applyAutoDir(wrap);
    return;
  }
  paint(body, list);

  wrap.addEventListener('click', async (ev) => {
    const t = ev.target.closest('[data-ms-action]');
    if (!t) return;
    const a = t.getAttribute('data-ms-action');
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
  form.appendChild(select('Kind', 'kind', r.kind || 'imap', ['imap', 'jmap', 'pop3', 'ews', 'smtp']));
  form.appendChild(textField('Host', 'host', r.host || ''));
  form.appendChild(numberField('Port', 'port', r.port || 993));
  form.appendChild(textField('Username', 'username', r.username || ''));
  form.appendChild(passwordField(isEdit ? 'New password (leave empty to keep)' : 'Password', 'password', ''));
  form.appendChild(checkField('Use TLS', 'use_tls', r.use_tls !== false));
  form.appendChild(checkField('Allow insecure TLS', 'allow_insecure', r.allow_insecure === true));
  form.appendChild(textField('Default base folder (IMAP / EWS)', 'default_base_folder', r.default_base_folder || 'INBOX'));
  form.appendChild(textField('Verify hostname override', 'verify_hostname', r.verify_hostname || ''));
  if (isEdit) {
    form.appendChild(checkField('Clear stored password (typed-confirm at save)', 'clear_secret', false));
  }
  form.appendChild(textArea('Note', 'note', r.note || '', 3));
  modal({
    title: isEdit ? ('Edit migration source #' + id) : 'Create migration source',
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
        use_tls: readCheck(form, 'use_tls'),
        allow_insecure: readCheck(form, 'allow_insecure'),
        default_base_folder: readField(form, 'default_base_folder'),
        verify_hostname: readField(form, 'verify_hostname'),
        note: readField(form, 'note'),
        clear_secret: readCheck(form, 'clear_secret'),
      };
      try {
        if (isEdit) {
          await apiPatch('/api/v1/admin/migration-sources/' + id, payload);
          toast('Source updated', 'success');
        } else {
          await apiPost('/api/v1/admin/migration-sources', payload);
          toast('Source created', 'success');
        }
        const r2 = await apiGet('/api/v1/admin/migration-sources');
        list = (r2 && r2.sources) || [];
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
    const r = await apiPost('/api/v1/admin/migration-sources/' + id + '/test', {});
    const msg = 'Test: ' + (r.status || '?') + ' — ' + (r.message || '');
    toast(msg, r.status === 'ok' ? 'success' : 'warn', 6000);
    const r2 = await apiGet('/api/v1/admin/migration-sources');
    list = (r2 && r2.sources) || [];
    paint(body, list);
  } catch (e) {
    toast('Test failed: ' + (e.message || e), 'error', 6000);
  }
}

async function doDelete(body, list, id) {
  if (!confirm('Delete migration source #' + id + '? This cannot be undone.')) return;
  try {
    await apiDelete('/api/v1/admin/migration-sources/' + id);
    toast('Source deleted', 'success');
    const r2 = await apiGet('/api/v1/admin/migration-sources');
    list = (r2 && r2.sources) || [];
    paint(body, list);
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error', 6000);
  }
}

function paint(body, list) {
  body.innerHTML = '';
  if (!list.length) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No migration sources yet. Use "Create source" to add one.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Name' }),
    el('th', { text: 'Kind' }),
    el('th', { text: 'Host:Port' }),
    el('th', { text: 'Username' }),
    el('th', { text: 'TLS' }),
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
      el('td', { class: 'kv-v', text: (r.use_tls ? 'yes' : 'no') + (r.allow_insecure ? ' (allow_insecure)' : '') }),
      el('td', { class: 'kv-v', text: r.has_secret ? 'configured' : 'missing' }),
      el('td', { text: ((r.last_test_status || 'never') + (r.last_test_at ? ' (' + r.last_test_at + ')' : '')) }),
      el('td', null, [
        el('button', { class: 'btn xs ghost', 'data-ms-action': 'test:' + r.id, text: 'Test' }),
        el('button', { class: 'btn xs ghost', 'data-ms-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-ms-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function textField(label, name, value) {
  const id = 'ms_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input', autocomplete: 'off', value: value || '' }),
  ]);
}
function passwordField(label, name, value) {
  const id = 'ms_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'password', class: 'input', autocomplete: 'new-password', value: value || '' }),
  ]);
}
function numberField(label, name, value) {
  const id = 'ms_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'number', class: 'input', value: String(value || 0), min: '0' }),
  ]);
}
function textArea(label, name, value, rows) {
  const id = 'ms_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('textarea', { id, name, class: 'input', rows: rows || 3 }, value || ''),
  ]);
}
function checkField(label, name, value) {
  const id = 'ms_' + name;
  return el('label', { class: 'field check', for: id }, [
    el('input', { id, name, type: 'checkbox', checked: value ? 'checked' : null }),
    el('span', { class: 'field-label', text: label }),
  ]);
}
function select(label, name, value, options) {
  const id = 'ms_' + name;
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
