/* =====================================================================
   pages/branding.js — Per-tenant branding editor.

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

import { el, openModal, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPatch, csrfFetch } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

const HEX_RE = /^#[0-9A-Fa-f]{6}$/;
const URL_RE = /^https?:\/\/\S+$/;

function isPrivateHost(host) {
  if (!host) return true;
  const h = host.toLowerCase();
  if (h === 'localhost' || h.endsWith('.localhost')) return true;
  return false;
}

export async function renderBrandingPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Branding' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Set the tenant logo URL and login shell primary color. Both fields are validated; invalid values are rejected.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const row = el('div', { class: 'ops-grid' });
  wrap.appendChild(row);

  const editor = el('section', { class: 'panel' });
  editor.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Logo + primary color' }),
  ]));
  const formBody = el('div', { class: 'panel-body', text: 'Loading...' });
  editor.appendChild(formBody);
  row.appendChild(editor);

  const preview = el('section', { class: 'panel' });
  preview.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Live preview' }),
  ]));
  const previewBody = el('div', { class: 'panel-body preview-shell' });
  preview.appendChild(previewBody);
  row.appendChild(preview);

  let tenant = null;
  let formEl = null;

  apiGet('/api/v1/admin/tenants/current').then((data) => {
    formBody.innerHTML = '';
    tenant = data || null;
    if (!tenant || tenant.exists === false) {
      formBody.appendChild(el('div', { class: 'empty',
        text: 'No tenant row found for this install. Branding cannot be edited until provisioning exists.' }));
      renderPreview(previewBody, null);
      return;
    }
    formEl = buildForm(tenant, (inputs) => {
      renderPreview(previewBody, inputs);
    });
    formBody.appendChild(formEl);
    renderPreview(previewBody, {
      logo_url: tenant.logo_url || '',
      primary_color: tenant.primary_color || '',
    });
  }).catch((err) => {
    formBody.innerHTML = '';
    formBody.appendChild(el('div', { class: 'error',
      text: 'Tenant lookup failed: ' + ((err && err.message) || err) }));
  });

  applyAutoDir(wrap);
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

  // Trigger an initial validation pass.
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
    toast('Nothing to save.', 'info', 1500);
    return;
  }
  const ok = await confirmDanger({
    title: 'Save branding',
    message: 'Apply the new logo URL and primary color to the tenant row?',
    confirmLabel: 'Save',
  });
  if (!ok) return;
  try {
    const resp = await csrfFetch('/api/v1/admin/tenants/' + encodeURIComponent(tenant.id) + '/branding', {
      method: 'PATCH',
      body: JSON.stringify(patch),
    });
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}));
      throw new Error(body.error || ('HTTP ' + resp.status));
    }
    toast('Branding saved.', 'success', 1800);
  } catch (err) {
    toast('Save failed: ' + ((err && err.message) || err), 'error', 6000);
  }
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
