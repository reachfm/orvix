/* =====================================================================
   modules/pages/internal/branding.js — Internal Ops · Per-tenant branding.

   Wires:
     GET  /api/v1/admin/tenants/current
     PATCH /api/v1/admin/tenants/:id/branding   (CSRF + admin role)

   Audit doc reference: docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md
     §2.7 row 32 (Per-tenant branding)
     §3 deliverable E (Branding)

   Honesty contract:
     - logo_url must be a public http(s) URL — the backend rejects
       localhost / 127.0.0.1 / private ranges and the UI repeats
       the rule so the operator knows before they hit Save.
     - primary_color must match #RRGGBB (3- and 8-digit CSS variants
       are deliberately not accepted so the login shell preview
       stays stable).
     - The page never claims to "publish" the new branding. Each
       PATCH returns restart_required:false because the admin SPA
       re-fetches the row on next render; the operator can
       immediately log out and back in to see the new logo.
   ===================================================================== */

import { pageHeader, panel, apiGet, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

const HEX_RE = /^#[0-9A-Fa-f]{6}$/;
const URL_RE = /^https?:\/\/\S+$/;

function isPrivateHost(host) {
  if (!host) return true;
  const h = host.toLowerCase();
  if (h === 'localhost' || h.endsWith('.localhost')) return true;
  return false;
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.internal'),
    title:   t('internalBranding.title') || 'Branding',
    subtitle: t('internalBranding.subtitle')
      || 'Set the tenant logo URL and login shell primary color. Both fields are validated; invalid values are rejected.',
  });

  const row = el('div', { class: 'ops-grid' });
  root.appendChild(row);

  const editor = panel(row, { title: t('internalBranding.editorTitle') || 'Logo + primary color' });
  editor.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  const preview = panel(row, { title: t('internalBranding.previewTitle') || 'Live preview' });
  const previewBody = el('div', { class: 'panel-body preview-shell' });
  preview.appendChild(previewBody);

  let tenant = null;
  let formEl = null;

  apiGet('/api/v1/admin/tenants/current').then((data) => {
    editor.innerHTML = '';
    tenant = data || null;
    if (!tenant || tenant.exists === false) {
      editor.appendChild(el('div', { class: 'empty',
        text: t('internalBranding.emptyTenant')
          || 'No tenant row found for this install. Branding cannot be edited until provisioning exists.' }));
      renderPreview(previewBody, null);
      return;
    }
    formEl = buildForm(tenant, (inputs) => {
      renderPreview(previewBody, inputs);
    });
    editor.appendChild(formEl);
    renderPreview(previewBody, {
      logo_url: tenant.logo_url || '',
      primary_color: tenant.primary_color || '',
    });
  }).catch((err) => {
    editor.innerHTML = '';
    editor.appendChild(el('div', { class: 'error',
      text: (t('internalBranding.error') || 'Tenant lookup failed: ') + ((err && err.message) || err) }));
  });

  applyAutoDir(root);
}

function buildForm(tenant, onChange) {
  const wrap = el('form', { class: 'branding-form', onsubmit: (e) => e.preventDefault() });
  const logoInput = el('input', { type: 'url', class: 'input',
    value: tenant.logo_url || '',
    placeholder: 'https://cdn.example.com/logo.svg',
    oninput: () => onChange({ logo_url: logoInput.value.trim(), primary_color: colorInput.value.trim() }),
  });
  const colorInput = el('input', { type: 'text', class: 'input',
    value: tenant.primary_color || '',
    placeholder: '#4F7CFF',
    pattern: '^#[0-9A-Fa-f]{6}$',
    oninput: () => onChange({ logo_url: logoInput.value.trim(), primary_color: colorInput.value.trim() }),
  });
  const errors = el('div', { class: 'branding-errors subtle' });
  const updateErrors = () => {
    errors.innerHTML = '';
    const lu = logoInput.value.trim();
    const pc = colorInput.value.trim();
    const issues = [];
    if (lu && !URL_RE.test(lu)) issues.push('Logo URL must start with http:// or https://');
    else if (lu) {
      try {
        const u = new URL(lu);
        if (isPrivateHost(u.hostname)) issues.push('Logo URL host must be a public hostname');
      } catch (_) {
        issues.push('Logo URL is not a valid URL');
      }
    }
    if (pc && !HEX_RE.test(pc)) issues.push('Primary color must be a #RRGGBB hex value');
    if (issues.length) {
      errors.appendChild(el('div', { text: issues.join('. ') + '.' }));
      save.disabled = true;
    } else {
      save.disabled = false;
    }
  };

  wrap.appendChild(el('label', { class: 'field' }, [
    el('span', { text: 'Logo URL (http(s) only)' }),
    logoInput,
  ]));
  wrap.appendChild(el('label', { class: 'field' }, [
    el('span', { text: 'Primary color (#RRGGBB)' }),
    colorInput,
  ]));
  wrap.appendChild(errors);

  const save = el('button', { type: 'submit', class: 'btn primary',
    text: 'Save branding', onclick: (ev) => {
      ev.preventDefault();
      submitBranding(tenant, logoInput.value.trim(), colorInput.value.trim());
    },
  });
  const reset = el('button', { type: 'button', class: 'btn ghost',
    text: 'Reset', onclick: () => {
      logoInput.value = tenant.logo_url || '';
      colorInput.value = tenant.primary_color || '';
      onChange({ logo_url: logoInput.value, primary_color: colorInput.value });
      updateErrors();
    },
  });
  wrap.appendChild(el('div', { class: 'form-actions' }, [save, reset]));

  setTimeout(updateErrors, 0);
  logoInput.addEventListener('input', updateErrors);
  colorInput.addEventListener('input', updateErrors);
  return wrap;
}

async function submitBranding(tenant, logoURL, primaryColor) {
  const patch = {};
  if (logoURL !== (tenant.logo_url || '')) patch.logo_url = logoURL;
  if (primaryColor !== (tenant.primary_color || '')) patch.primary_color = primaryColor;
  if (Object.keys(patch).length === 0) {
    // Nothing to do — tell the operator without a toast dependency.
    const note = el('div', { class: 'empty-hint', text: 'Nothing to save.' });
    saveNoteInto(tenant, note);
    return;
  }
  const ok = await confirmInPage({
    title: 'Save branding',
    message: 'Apply the new logo URL and primary color to the tenant row?',
    confirmLabel: 'Save',
  });
  if (!ok) return;
  try {
    const csrfFetch = (await import('../../api.js')).csrfFetch;
    const resp = await csrfFetch('/api/v1/admin/tenants/' + encodeURIComponent(tenant.id) + '/branding', {
      method: 'PATCH',
      body: JSON.stringify(patch),
    });
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}));
      throw new Error(body.error || ('HTTP ' + resp.status));
    }
    // Reflect the saved values in the form so the next save round
    // is a no-op until the operator types something new.
    tenant.logo_url = logoURL;
    tenant.primary_color = primaryColor;
  } catch (err) {
    const note = el('div', { class: 'error', text: 'Save failed: ' + ((err && err.message) || err) });
    saveNoteInto(tenant, note);
  }
}

// Internal helpers — avoid importing the toast/modal stack so this
// file stays self-contained.  Anything we render is put directly
// into the editor panel; the operator sees the result inline.
function saveNoteInto(tenant, node) {
  if (!tenant) return;
  const form = document.querySelector('.branding-form');
  if (!form) return;
  const old = form.parentNode.querySelector('.branding-save-status');
  if (old) old.remove();
  node.classList.add('branding-save-status');
  form.parentNode.appendChild(node);
}

function confirmInPage(opts) {
  return new Promise((resolve) => {
    const overlay = el('div', { class: 'modal-overlay' });
    const modal = el('div', { class: 'modal modal-md', role: 'dialog' });
    modal.appendChild(el('header', { class: 'modal-head' }, [
      el('h3', { class: 'modal-title', text: opts.title || 'Confirm' }),
      el('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close',
        onclick: () => { overlay.remove(); resolve(false); } }, '\u00d7'),
    ]));
    modal.appendChild(el('div', { class: 'modal-body' }, [
      el('div', { class: 'modal-message', text: opts.message || '' }),
    ]));
    const foot = el('footer', { class: 'modal-foot' });
    const cancel = el('button', { class: 'btn ghost', type: 'button', text: 'Cancel',
      onclick: () => { overlay.remove(); resolve(false); } });
    const confirm = el('button', { class: 'btn primary', type: 'button', text: opts.confirmLabel || 'Confirm',
      onclick: () => { overlay.remove(); resolve(true); } });
    foot.appendChild(cancel);
    foot.appendChild(confirm);
    modal.appendChild(foot);
    overlay.appendChild(modal);
    overlay.addEventListener('click', (e) => { if (e.target === overlay) { overlay.remove(); resolve(false); } });
    document.body.appendChild(overlay);
  });
}

function renderPreview(previewBody, inputs) {
  previewBody.innerHTML = '';
  let logo = '';
  let color = '';
  if (inputs) {
    logo = inputs.logo_url || '';
    color = inputs.primary_color || '';
  }
  const card = el('div', { class: 'branding-preview',
    style: color ? ('--brand-color: ' + color) : '',
  });
  const mark = el('div', { class: 'branding-preview-mark' });
  if (logo) {
    mark.appendChild(el('img', { src: logo, alt: 'logo', class: 'branding-preview-logo',
      onerror: function () { this.style.display = 'none'; } }));
  }
  mark.appendChild(el('span', { text: 'orvix' }));
  card.appendChild(mark);
  card.appendChild(el('div', { class: 'branding-preview-hint', text: 'Login shell preview' }));
  previewBody.appendChild(card);
  previewBody.appendChild(el('p', { class: 'subtle', text: 'Logo URL: ' + (logo || '(none)') }));
  previewBody.appendChild(el('p', { class: 'subtle', text: 'Primary color: ' + (color || '(default theme)') }));
}
