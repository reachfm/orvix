/* =====================================================================
   pages/ssl.js — SSL / TLS certificates admin page.

   Wires:
     GET    /api/v1/admin/ssl/certificates          → list + warnings
     POST   /api/v1/admin/ssl/certificates          → upload PEM + key
     DELETE /api/v1/admin/ssl/certificates/:id      → remove
     POST   /api/v1/admin/ssl/certificates/reload   → reload runtime
     GET    /api/v1/admin/ssl/acme/status           → honest ACME view
     GET    /api/v1/admin/ssl/expiry-warnings       → fast badge poll

   The page is honest: ACME issuance is not implemented
   in this build; the UI tells the operator which script
   and which endpoint to use, never fabricates automation.

   Secrets: the upload form never echoes the private key,
   and the listing never returns the key bytes. The
   /admin/ssl/certificates payload contains paths and
   fingerprints only.
   ===================================================================== */

import { el, modal, confirmDanger, toast } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderSslPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'SSL certificates' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Inventory + upload / remove / reload. Runtime certs loaded from coremail.tls_cert_file / tls_key_file.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-ssl-action': 'upload',
        text: 'Upload certificate' }),
      el('button', { class: 'btn ghost', 'data-ssl-action': 'reload',
        text: 'Reload runtime' }),
    ]),
  ]));
  root.appendChild(wrap);

  const acmePanel = el('section', { class: 'panel' });
  acmePanel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'ACME / automated issuance' })));
  acmePanel.appendChild(el('div', { class: 'panel-body',
    text: 'Checking ACME provider...' }));
  wrap.appendChild(acmePanel);

  const warningPanel = el('section', { class: 'panel panel-warn' });
  warningPanel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Expiry warnings' })));
  warningPanel.appendChild(el('div', { class: 'panel-body',
    text: 'Loading...' }));
  wrap.appendChild(warningPanel);

  const runtimePanel = el('section', { class: 'panel' });
  runtimePanel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Runtime certificates (active)' })));
  const runtimeBody = el('div', { class: 'panel-body', text: 'Loading...' });
  runtimePanel.appendChild(runtimeBody);
  wrap.appendChild(runtimePanel);

  const uploadedPanel = el('section', { class: 'panel' });
  uploadedPanel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Uploaded certificates' })));
  const uploadedBody = el('div', { class: 'panel-body', text: 'Loading...' });
  uploadedPanel.appendChild(uploadedBody);
  wrap.appendChild(uploadedPanel);

  // Load
  let acmeData, listData;
  try {
    [acmeData, listData] = await Promise.all([
      apiGet('/api/v1/admin/ssl/acme/status'),
      apiGet('/api/v1/admin/ssl/certificates'),
    ]);
  } catch (err) {
    wrap.appendChild(el('div', { class: 'panel error-panel' }, [
      el('div', { class: 'panel-head' }, el('h3', { text: 'Could not load SSL data' })),
      el('div', { class: 'panel-body', text: (err && err.message) || String(err) }),
    ]));
    applyAutoDir(wrap);
    return;
  }

  // ACME panel
  acmePanel.querySelector('.panel-body').innerHTML = '';
  const acmeBody = acmePanel.querySelector('.panel-body');
  if (acmeData && acmeData.acme_enabled) {
    acmeBody.appendChild(el('div', { class: 'banner banner-good',
      text: 'ACME enabled. Provider: ' + (acmeData.acme_provider || '-') }));
  } else {
    acmeBody.appendChild(el('div', { class: 'banner banner-warn',
      text: 'ACME disabled. ' + (acmeData && acmeData.acme_provider) }));
  }
  if (acmeData && acmeData.on_disk_candidates && acmeData.on_disk_candidates.length) {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'On-disk candidate cert directory' }),
    ])));
    const tb = el('tbody');
    acmeData.on_disk_candidates.forEach((p) => {
      tb.appendChild(el('tr', null, [el('td', { class: 'kv-v', text: p })]));
    });
    tbl.appendChild(tb);
    acmeBody.appendChild(el('p', { class: 'subtle',
      text: 'Detected upstream ACME directories. Use these as source files for setup-smtp-tls.sh.' }));
    acmeBody.appendChild(tbl);
  }
  if (acmeData && acmeData.script_helper) {
    acmeBody.appendChild(el('p', { class: 'subtle' }, [
      el('strong', { text: 'Helper script: ' }),
      el('code', { text: acmeData.script_helper }),
    ]));
  }
  if (acmeData && acmeData.manual_paths) {
    const wrap2 = el('div', { class: 'kv' });
    const ul = el('ul');
    acmeData.manual_paths.forEach((p) => {
      ul.appendChild(el('li', null, el('code', { class: 'code', text: p })));
    });
    wrap2.appendChild(ul);
    acmeBody.appendChild(el('p', { class: 'subtle', text: 'Manual endpoints:' }));
    acmeBody.appendChild(wrap2);
  }
  if (acmeData && acmeData.honest_notes) {
    const wrap3 = el('div', { class: 'subtle' });
    acmeData.honest_notes.forEach((n) => {
      wrap3.appendChild(el('p', { text: '• ' + n }));
    });
    acmeBody.appendChild(wrap3);
  }

  // Warnings
  const wb = warningPanel.querySelector('.panel-body');
  wb.innerHTML = '';
  const warns = (listData && listData.expiry_warnings) || [];
  if (!warns.length) {
    wb.appendChild(el('div', { class: 'empty',
      text: 'No certificates expiring within '
        + ((listData && listData.expiry_cutoff_days) || 30)
        + ' days.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'Name' }),
      el('th', { text: 'Expires' }),
      el('th', { text: 'Days left' }),
      el('th', { text: 'Status' }),
    ])));
    const tb = el('tbody');
    warns.forEach((c) => {
      tb.appendChild(el('tr', null, [
        el('td', { text: c.name }),
        el('td', { text: c.not_after || '-' }),
        el('td', { text: String(c.days_remaining == null ? '-' : c.days_remaining) }),
        el('td', { class: 'kv-v', text: c.status || '-' }),
      ]));
    });
    tbl.appendChild(tb);
    wb.appendChild(tbl);
  }

  // Runtime
  paintCertTable(runtimeBody, (listData && listData.runtime) || [], 'runtime');

  // Uploaded
  paintCertTable(uploadedBody, (listData && listData.uploaded) || [], 'uploaded');

  // Action delegation
  wrap.addEventListener('click', async (ev) => {
    const t = ev.target.closest('[data-ssl-action]');
    if (!t) return;
    const action = t.getAttribute('data-ssl-action');
    if (action === 'upload') return openUpload();
    if (action === 'reload') return doReload();
    const m = action && action.match(/^delete:(\d+)$/);
    if (m) return doDelete(Number(m[1]));
  });

  applyAutoDir(wrap);

  async function openUpload() {
    const root2 = el('div', { class: 'form-stack' });
    root2.appendChild(textField('Name', 'name', '', 'e.g. mail-production-2024'));
    root2.appendChild(textArea('Certificate PEM (fullchain)', 'cert_pem', '', 14));
    root2.appendChild(textArea('Private key PEM', 'key_pem', '', 8));
    root2.appendChild(el('p', { class: 'subtle',
      text: 'The private key is written server-side to /etc/orvix/tls/admin/<name>.key.pem with mode 0600 and never echoed back.' }));
    modal({
      title: 'Upload SSL certificate',
      body: root2,
      actions: [
        { label: 'Cancel', kind: 'ghost', value: 'cancel' },
        { label: 'Upload', kind: 'primary', value: 'ok' },
      ],
      onAction: async (val) => {
        if (val !== 'ok') return;
        const name = readField(root2, 'name').trim();
        const cert_pem = readField(root2, 'cert_pem').trim();
        const key_pem = readField(root2, 'key_pem').trim();
        if (!name || !cert_pem || !key_pem) {
          toast('name, cert_pem, and key_pem are all required', 'warn');
          return false;
        }
        try {
          await apiPost('/api/v1/admin/ssl/certificates', { name, cert_pem, key_pem });
          toast('Certificate imported', 'success');
          setTimeout(() => location.reload(), 350);
          return true;
        } catch (e) {
          toast('Upload failed: ' + (e.message || e), 'error', 6000);
          return false;
        }
      },
    });
  }

  async function doReload() {
    try {
      const r = await apiPost('/api/v1/admin/ssl/certificates/reload', {});
      toast(r && r.success ? 'Reload OK' : 'Reload failed', r && r.success ? 'success' : 'warn');
    } catch (e) {
      toast('Reload failed: ' + (e.message || e), 'error', 6000);
    }
  }

  async function doDelete(id) {
    const ok = await confirmDanger({
      title: 'Remove uploaded certificate',
      message: 'The PEM + key will be removed from disk. The runtime listener is unaffected unless this cert is the active one.',
      confirmLabel: 'Remove',
      dangerous: true,
    });
    if (!ok) return;
    try {
      await apiDelete('/api/v1/admin/ssl/certificates/' + id);
      toast('Certificate removed', 'success');
      setTimeout(() => location.reload(), 350);
    } catch (e) {
      toast('Delete failed: ' + (e.message || e), 'error', 6000);
    }
  }
}

function paintCertTable(body, rows, source) {
  body.innerHTML = '';
  if (!rows.length) {
    body.appendChild(el('div', { class: 'empty',
      text: source === 'runtime'
        ? 'No runtime certificate is loaded yet. Upload one or copy via setup-smtp-tls.sh.'
        : 'No uploaded certificates yet. Use the "Upload certificate" button.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Name' }),
    el('th', { text: 'Common name' }),
    el('th', { text: 'Issuer' }),
    el('th', { text: 'Expires' }),
    el('th', { text: 'Days left' }),
    el('th', { text: 'Status' }),
    el('th', { text: 'Fingerprint (SHA-256)' }),
    el('th', { text: 'Actions' }),
  ])));
  const tb = el('tbody');
  rows.forEach((c) => {
    const tr = el('tr', null, [
      el('td', { text: c.name || '-' }),
      el('td', { text: c.common_name || '-' }),
      el('td', { text: c.issuer || '-' }),
      el('td', { text: c.not_after || '-' }),
      el('td', { text: String(c.days_remaining == null ? '-' : c.days_remaining) }),
      el('td', { class: 'kv-v', text: c.status || '-' }),
      el('td', { class: 'kv-v mono small', text: c.fingerprint_sha256 || '-' }),
    ]);
    if (source === 'uploaded' && c.id) {
      tr.appendChild(el('td', null,
        el('button', { class: 'btn xs ghost danger',
          'data-ssl-action': 'delete:' + c.id, text: 'Remove' })));
    } else {
      tr.appendChild(el('td', { text: '-' }));
    }
    tb.appendChild(tr);
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function textField(label, name, value, placeholder) {
  const id = 'sf_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { id, name, type: 'text', class: 'input',
      value: value || '', placeholder: placeholder || '' }),
  ]);
}
function textArea(label, name, value, rows) {
  const id = 'sf_' + name;
  return el('label', { class: 'field', for: id }, [
    el('span', { class: 'field-label', text: label }),
    el('textarea', { id, name, class: 'input', rows: rows || 8 }, value || ''),
  ]);
}
function readField(scope, name) {
  const node = scope.querySelector('[name="' + name + '"]');
  return node ? (node.value || '') : '';
}
