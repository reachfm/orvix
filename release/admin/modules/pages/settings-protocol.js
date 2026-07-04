/* =====================================================================
   pages/settings-protocol.js — Per-protocol settings sub-page.

   Used for every one of the 10 protocol split pages
   (smtp_recv, smtp_tx, imap, pop3, webmail, webadmin,
   dns, remote_pop, jmap, mobility). The route
   /settings/protocol/:protocol provides the `protocol`
   name; this page fetches GET
   /api/v1/admin/settings/protocol/:protocol and renders
   one editable form row per returned key, then PATCHes
   back through the API.

   Restart-required fields are surfaced as a global
   banner after save. Soft-rejected fields (type mismatch)
   are reported per-field.
   ===================================================================== */

import { el, toast } from '../components.js';
import { apiGet, apiPatch } from '../api.js';
import { applyAutoDir } from '../rtl.js';

const PROTOCOL_TITLES = {
  smtp_recv: 'SMTP receiving',
  smtp_tx: 'SMTP sending / submission',
  imap: 'IMAP',
  pop3: 'POP3',
  webmail: 'WebMail',
  webadmin: 'WebAdmin',
  dns: 'DNS automation',
  remote_pop: 'Remote POP',
  jmap: 'JMAP / CJA / modern sync',
  mobility: 'Mobility & Sync',
};

export async function renderSettingsProtocolPage(root, protocol) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title',
        text: 'Settings: ' + (PROTOCOL_TITLES[protocol] || protocol) }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Per-protocol configuration. Restart-required fields are clearly flagged.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Editable settings' })));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  panel.appendChild(body);
  wrap.appendChild(panel);

  let data;
  try {
    data = await apiGet('/api/v1/admin/settings/protocol/' + protocol);
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: 'Load failed: ' + (e.message || e) }));
    applyAutoDir(wrap);
    return;
  }
  if (data && data.description) {
    wrap.appendChild(el('p', { class: 'subtle', text: data.description }));
  }
  paint(body, data, panel, protocol);
  applyAutoDir(wrap);
}

function paint(body, data, panel, protocol) {
  body.innerHTML = '';
  const keys = (data && data.keys) || [];
  if (!keys.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No editable settings on this protocol page.' }));
    return;
  }
  const form = el('form', { class: 'settings-form' });
  let hasAny = false;
  for (const k of keys) {
    hasAny = true;
    form.appendChild(buildField(k));
  }
  if (!hasAny) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No editable settings on this protocol page.' }));
    return;
  }
  const actions = el('div', { class: 'form-actions' }, [
    el('button', { type: 'button', class: 'btn ghost',
      onclick: () => location.reload(),
      text: 'Discard' }),
    el('button', { type: 'button', class: 'btn primary', id: 'sp-save',
      text: 'Save protocol settings' }),
  ]);
  form.appendChild(actions);
  body.appendChild(form);
  panel.querySelector('h3').textContent = 'Editable settings (' + keys.length + ')';
  const result = el('div', { class: 'panel-body', id: 'sp-result' });
  body.appendChild(result);

  document.getElementById('sp-save').addEventListener('click', async () => {
    const payload = {};
    for (const k of keys) {
      payload[k.key] = readField(form, k);
    }
    result.innerHTML = '';
    result.appendChild(el('p', { class: 'subtle', text: 'Saving...' }));
    try {
      const resp = await apiPatch('/api/v1/admin/settings/protocol/' + protocol, payload);
      result.innerHTML = '';
      const applied = (resp && resp.applied) || [];
      const rejected = (resp && resp.rejected) || [];
      const needRestart = !!(resp && resp.restart_required);
      if (applied.length) {
        result.appendChild(el('p', { class: 'banner banner-good',
          text: 'Saved ' + applied.length + ' field(s).' + (needRestart ? ' Restart required.' : '') }));
        const tbl = el('table', { class: 'kv-table' });
        applied.forEach((r) => {
          tbl.appendChild(el('tr', null, [
            el('th', { text: r.key }),
            el('td', { class: 'kv-v', text: String(r.value) }),
            el('td', { text: r.restart_required ? 'restart required' : 'hot' }),
          ]));
        });
        result.appendChild(tbl);
      }
      if (rejected.length) {
        result.appendChild(el('p', { class: 'banner banner-warn',
          text: 'Rejected ' + rejected.length + ' field(s):' }));
        const tbl = el('table', { class: 'kv-table' });
        rejected.forEach((r) => {
          tbl.appendChild(el('tr', null, [
            el('th', { text: r.key }),
            el('td', { class: 'kv-v', text: r.reason }),
          ]));
        });
        result.appendChild(tbl);
      }
      toast('Saved ' + applied.length + ' / ' + keys.length + ' field(s)',
        applied.length === keys.length ? 'success' : 'warn');
    } catch (err) {
      result.innerHTML = '';
      result.appendChild(el('div', { class: 'error', text: 'Save failed: ' + (err.message || err) }));
      toast('Save failed: ' + (err.message || err), 'error', 6000);
    }
  });
}

function buildField(k) {
  const id = 'sp_' + k.key.replace(/\./g, '_');
  const row = el('div', { class: 'form-row' });
  row.appendChild(el('label', { for: id }, [
    el('span', { text: k.label || k.key }),
    k.restart_required && el('span', { class: 'badge tag warn', text: 'restart required' }),
  ]));
  let input;
  if (k.type === 'bool') {
    input = el('select', { id, name: k.key, class: 'input' }, [
      el('option', { value: 'true', selected: k.value === true ? 'selected' : null }, 'true'),
      el('option', { value: 'false', selected: k.value === false ? 'selected' : null }, 'false'),
    ]);
  } else if (k.type === 'int') {
    input = el('input', { id, name: k.key, type: 'number', class: 'input',
      value: String(k.value != null ? k.value : '') });
  } else {
    input = el('input', { id, name: k.key, type: 'text', class: 'input', autocomplete: 'off',
      spellcheck: 'false', value: k.value != null ? String(k.value) : '' });
  }
  row.appendChild(input);
  if (k.description) {
    row.appendChild(el('p', { class: 'subtle small', text: k.description }));
  }
  return row;
}

function readField(form, k) {
  const node = form.querySelector('[name="' + k.key + '"]');
  if (!node) return null;
  if (k.type === 'bool') return node.value === 'true';
  if (k.type === 'int') {
    const n = Number(node.value);
    return isNaN(n) ? null : n;
  }
  return node.value;
}
