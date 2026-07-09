/* =====================================================================
   modules/pages/customer/settings.js — Customer Admin · Settings.

   The customer console exposes two settings surfaces:

     Section 'general' (default)
        GET /api/v1/admin/settings (Handler AdminSettingsGet)

     Section 'security'
        GET /api/v1/admin/mfa/status
        GET /api/v1/admin/login-protection/status

   Older routing aliases (settings/general, settings/security) are
   dispatched to render(root, { section }) from app.js, so we treat
   them as the same page with different sub-views.

   Honesty contract:
     - The settings surface is read-only by design in this build.
       PATCH writes are exposed server-side but never triggered from
       the customer console so that an admin cannot accidentally
       override cross-tenant globals from a tenant-scoped session.
     - MFA "Configured" mirrors the server's boolean; never
       pre-marked as "Required" without a backend hint.
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtShortDate, badge, statusKind, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

export async function render(root, opts) {
  opts = opts || {};
  const section = (opts && opts.section) || 'general';

  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   section === 'security'
      ? (t('customerSettings.securityTitle') || 'Security settings')
      : (t('customerSettings.title')        || 'Settings'),
    subtitle: section === 'security'
      ? (t('customerSettings.securitySubtitle')
          || 'MFA posture and login-protection summary, as reported by the API.')
      : (t('customerSettings.subtitle')
          || 'Tenant-scoped global settings, returned by /api/v1/admin/settings. The console is read-only.'),
  });

  const tabs = el('div', { class: 'settings-tabs' }, [
    el('a', { href: '#/settings/general', class: 'settings-tab' + (section === 'general' ? ' active' : ''),
      text: t('customerSettings.tabGeneral') || 'General' }),
    el('a', { href: '#/settings/security', class: 'settings-tab' + (section === 'security' ? ' active' : ''),
      text: t('customerSettings.tabSecurity') || 'Security' }),
  ]);
  root.appendChild(tabs);

  if (section === 'security') {
    await renderSecuritySection(root);
  } else {
    await renderGeneralSection(root);
  }

  applyAutoDir(root);
}

async function renderGeneralSection(root) {
  const body = panel(root, {
    title: t('customerSettings.generalTitle') || 'General settings',
    meta:  '/api/v1/admin/settings',
    body:  '',
  });
  body.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));

  let data = null;
  let loadErr = null;
  try { data = await apiGet('/api/v1/admin/settings'); }
  catch (err) { loadErr = err; }

  body.innerHTML = '';
  if (loadErr) {
    body.appendChild(el('div', { class: 'error',
      text: (t('customerSettings.error') || 'Settings lookup failed: ') + ((loadErr && loadErr.message) || loadErr) }));
    return;
  }
  if (!data || typeof data !== 'object' || (Array.isArray(data) && data.length === 0)) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('span', { class: 'empty-illustration', text: 'S' }),
      el('div', { class: 'empty-title', text: t('customerSettings.emptyTitle') || 'No settings yet' }),
      el('div', { class: 'empty-hint',
        text: t('customerSettings.emptyBody')
          || 'The /api/v1/admin/settings endpoint returned no rows. Configure the install to populate settings.' }),
    ]));
    return;
  }

  // The handler returns a flat key/value map (settings rows).
  const list = Array.isArray(data) ? data : Object.keys(data).map((k) => ({ key: k, value: data[k] }));

  if (list.length === 0) {
    body.appendChild(el('div', { class: 'empty-hint',
      text: t('customerSettings.emptyList') || 'No settings keys configured.' }));
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: t('customerSettings.col.key')         || 'Key' }),
    el('th', { text: t('customerSettings.col.value')       || 'Value' }),
    el('th', { text: t('customerSettings.col.updated')     || 'Last updated' }),
  ])));
  const tb = el('tbody');
  list.forEach((row) => {
    const key = row.key || row.name || row.id || '-';
    const val = row.value != null ? row.value : (row.val != null ? row.val : '');
    const updated = row.updated_at || row.modified_at || row.timestamp || '';
    tb.appendChild(el('tr', { 'data-search': String(key).toLowerCase() + ' ' + String(val).toLowerCase() }, [
      el('td', { class: 'kv-v', text: String(key) }),
      el('td', { class: 'kv-v', text: val === '' ? '—' : String(val) }),
      el('td', { class: 'kv-v', text: updated ? fmtShortDate(updated) : '—' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  body.appendChild(el('p', { class: 'subtle ops-honest-note',
    text: t('customerSettings.generalHonestNote')
      || 'Setting writes are PATCHed through the admin-settings API and intentionally not exposed in the customer console.' }));
}

async function renderSecuritySection(root) {
  const layout = el('div', { class: 'ops-grid' });
  root.appendChild(layout);

  const mfaCard = panel(layout, { title: t('customerSettings.mfa') || 'Multi-factor authentication' });
  mfaCard.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));
  apiGet('/api/v1/admin/mfa/status').then((data) => {
    mfaCard.innerHTML = '';
    if (!data) {
      mfaCard.appendChild(el('div', { class: 'empty', text: t('customerSettings.mfaEmpty') || 'No MFA status returned.' }));
      return;
    }
    const supported = data.supported === true || data.enabled === true || !!data.totp;
    const configured = !!data.configured;
    const required = !!data.required;
    mfaCard.appendChild(el('div', { class: 'kv-row' }, [
      el('span', { class: 'kv-k', text: t('customerSettings.mfaSupported') || 'TOTP supported' }),
      el('span', { class: 'kv-v' }, badge(supported ? 'yes' : 'no', statusKind(supported ? 'enabled' : 'disabled'))),
    ]));
    mfaCard.appendChild(el('div', { class: 'kv-row' }, [
      el('span', { class: 'kv-k', text: t('customerSettings.mfaConfigured') || 'Configured for this account' }),
      el('span', { class: 'kv-v' }, badge(configured ? 'yes' : 'no', statusKind(configured ? 'active' : 'pending'))),
    ]));
    mfaCard.appendChild(el('div', { class: 'kv-row' }, [
      el('span', { class: 'kv-k', text: t('customerSettings.mfaRequired') || 'Policy requires MFA' }),
      el('span', { class: 'kv-v' }, badge(required ? 'yes' : 'no', statusKind(required ? 'warn' : 'ok'))),
    ]));
  }).catch((err) => {
    mfaCard.innerHTML = '';
    mfaCard.appendChild(el('div', { class: 'error',
      text: (t('customerSettings.mfaError') || 'MFA status failed: ') + ((err && err.message) || err) }));
  });

  const lpCard = panel(layout, { title: t('customerSettings.lp') || 'Login protection' });
  lpCard.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));
  apiGet('/api/v1/admin/login-protection/status').then((data) => {
    lpCard.innerHTML = '';
    if (!data) {
      lpCard.appendChild(el('div', { class: 'empty', text: t('customerSettings.lpEmpty') || 'No login-protection status returned.' }));
      return;
    }
    const dl = el('dl', { class: 'kv' });
    [
      ['Enabled',           data.enabled !== false ? 'on' : 'off'],
      ['Persistence',       data.persistence || 'in_memory'],
      ['Lockout threshold', data.lockout_threshold != null ? data.lockout_threshold : '—'],
      ['Rate limit',        data.rate_limit_per_minute != null ? data.rate_limit_per_minute + ' / min' : '—'],
      ['Updated',           data.updated_at ? fmtShortDate(data.updated_at) : '—'],
    ].forEach(([k, v]) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
    });
    lpCard.appendChild(dl);
  }).catch((err) => {
    lpCard.innerHTML = '';
    lpCard.appendChild(el('div', { class: 'error',
      text: (t('customerSettings.lpError') || 'Login-protection status failed: ') + ((err && err.message) || err) }));
  });
}
