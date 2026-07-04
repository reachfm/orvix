/* =====================================================================
   pages/fs-access.js — Safe read-only File System browser.

   Wires:
     GET /api/v1/admin/fs/browse?root=/var/log/orvix/    → list
     GET /api/v1/admin/fs/read?path=<file>               → content

   The backend restricts browsing to an allowlist of
   approved roots (/var/log/orvix/, /var/backups/orvix/,
   /var/lib/orvix/, /etc/orvix/tls/, /var/log/).
   Path traversal attempts return 403 from the backend.
   Secret-shaped files return redacted — never their
   content — and the UI surfaces that clearly.
   ===================================================================== */

import { el, modal, toast } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

const ROOTS = [
  '/var/log/orvix/',
  '/var/backups/orvix/',
  '/var/lib/orvix/',
  '/etc/orvix/tls/',
  '/var/log/',
];

export async function renderFsAccessPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'File system access' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Read-only browsing of approved server directories (logs, backups, runtime data, TLS, generic /var/log). Files matching secret-shape patterns are redacted.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const note = el('div', { class: 'banner banner-info' }, [
    el('span', { class: 'banner-text', text:
      'Browsing is restricted to the following approved roots. Files outside these directories return 403 from the backend.' }),
  ]);
  wrap.appendChild(note);

  const rootList = el('ul', { class: 'kv' });
  ROOTS.forEach((r) => rootList.appendChild(el('li', null, el('code', { class: 'code', text: r }))));
  wrap.appendChild(rootList);

  const picker = el('div', { class: 'form-row' });
  picker.appendChild(el('label', { class: 'field', for: 'fs-root' }, [
    el('span', { class: 'field-label', text: 'Browse root' }),
    el('select', { id: 'fs-root', name: 'root', class: 'input' },
      ROOTS.map((r) => el('option', { value: r, selected: r === ROOTS[0] ? 'selected' : null }, r))),
  ]));
  picker.appendChild(el('button', { class: 'btn primary', type: 'button',
    onclick: () => browse(picker, picker.querySelector('#fs-root').value), text: 'Browse' }));
  wrap.appendChild(picker);

  const view = el('section', { class: 'panel' });
  view.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Contents' })));
  const viewBody = el('div', { class: 'panel-body', text: 'Pick a root and click Browse.' });
  view.appendChild(viewBody);
  wrap.appendChild(view);

  applyAutoDir(wrap);
}

async function browse(scope, root) {
  const view = scope.parentElement.querySelector('.panel');
  const viewBody = view.querySelector('.panel-body');
  viewBody.innerHTML = '';
  viewBody.appendChild(el('p', { class: 'subtle', text: 'Listing ' + root + ' ...' }));
  try {
    const r = await apiGet('/api/v1/admin/fs/browse?root=' + encodeURIComponent(root));
    viewBody.innerHTML = '';
    if (r && r.error) {
      viewBody.appendChild(el('div', { class: 'error', text: r.error }));
      return;
    }
    const entries = (r && r.entries) || [];
    if (!entries.length) {
      viewBody.appendChild(el('div', { class: 'empty', text: 'Empty directory.' }));
      return;
    }
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'Name' }),
      el('th', { text: 'Type' }),
      el('th', { text: 'Size' }),
      el('th', { text: 'Modified' }),
      el('th', { text: 'Actions' }),
    ])));
    const tb = el('tbody');
    entries.forEach((e) => {
      const tr = el('tr', null, [
        el('td', { text: e.name }),
        el('td', { text: e.is_dir ? 'dir' : 'file' }),
        el('td', { text: e.is_dir ? '-' : String(e.size) }),
        el('td', { text: e.modified_at || '-' }),
      ]);
      if (e.is_dir) {
        tr.appendChild(el('td', null,
          el('button', { class: 'btn xs ghost', type: 'button',
            text: 'Open', onclick: () => browse(scope, e.path) })));
      } else if (e.secret_flag) {
        tr.appendChild(el('td', null,
          el('span', { class: 'badge tag warn', text: 'redacted' })));
      } else {
        tr.appendChild(el('td', null,
          el('button', { class: 'btn xs ghost', type: 'button',
            text: 'Read', onclick: () => readFile(scope, e.path) })));
      }
      tb.appendChild(tr);
    });
    tbl.appendChild(tb);
    viewBody.appendChild(tbl);
  } catch (e) {
    viewBody.innerHTML = '';
    viewBody.appendChild(el('div', { class: 'error', text: 'Browse failed: ' + (e.message || e) }));
  }
}

async function readFile(scope, path) {
  const view = scope.parentElement.querySelector('.panel');
  const body = el('div', { class: 'panel-body' });
  modal({
    title: path,
    body,
    actions: [{ label: 'Close', kind: 'ghost', value: 'cancel' }],
  });
  try {
    const r = await apiGet('/api/v1/admin/fs/read?path=' + encodeURIComponent(path));
    if (r && r.secret_redacted) {
      body.appendChild(el('div', { class: 'banner banner-warn',
        text: r.reason || 'file is secret-shaped; contents redacted' }));
      return;
    }
    body.appendChild(el('p', { class: 'subtle',
      text: 'size: ' + r.size + ', truncated: ' + (r.truncated ? 'yes' : 'no') + ', max bytes: ' + r.max_bytes }));
    if (!r.is_text) {
      body.appendChild(el('div', { class: 'empty', text: '(binary file — content not shown)' }));
      return;
    }
    const pre = el('pre', { class: 'code-block' });
    pre.appendChild(document.createTextNode(r.content || ''));
    body.appendChild(pre);
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: 'Read failed: ' + (e.message || e) }));
  }
}
