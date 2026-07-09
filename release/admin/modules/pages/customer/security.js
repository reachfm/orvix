/* =====================================================================
   modules/pages/customer/security.js — Customer Admin · Security page.

   Surfaces the two real security surfaces the build exposes:

     GET /api/v1/admin/login-protection/status
     GET /api/v1/admin/mfa/status
     GET /api/v1/admin/login-protection/lockouts

   No made-up "all green" claims.  The page renders the literal state
   reported by the runtime, including the presence/absence of a
   configured Fail2Ban at the OS level (which the build never
   assumes — the operator must verify on the host).
   ===================================================================== */

import { pageHeader, panel, apiGet, fmtNumber, badge, statusKind, fmtShortDate, t, el } from '../saas-shared.js';
import { applyAutoDir } from '../../rtl.js';

function asString(v) {
  if (v == null) return '';
  if (typeof v === 'string') return v;
  if (typeof v === 'boolean') return v ? 'yes' : 'no';
  return String(v);
}

export async function render(root /* , opts */) {
  root.innerHTML = '';

  pageHeader(root, {
    eyebrow: t('sidebar.mode.customer'),
    title:   t('customerSecurity.title') || 'Security',
    subtitle: t('customerSecurity.subtitle')
      || 'Login protection state and MFA configuration. The runtime is the source of truth; this page mirrors what the API returns.',
  });

  const layout = el('div', { class: 'ops-grid' });
  root.appendChild(layout);

  const lpCard   = panel(layout, { title: t('customerSecurity.loginProtection') || 'Login protection' });
  const mfaCard  = panel(layout, { title: t('customerSecurity.mfa')            || 'Multi-factor authentication' });
  const lockCard = panel(layout, { title: t('customerSecurity.lockouts')       || 'Active lockouts', body: '' });

  // Login protection
  lpCard.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));
  apiGet('/api/v1/admin/login-protection/status').then((data) => {
    lpCard.innerHTML = '';
    if (!data) {
      lpCard.appendChild(el('div', { class: 'empty', text: t('customerSecurity.emptyLP') || 'No login-protection status returned.' }));
      return;
    }
    const dl = el('dl', { class: 'kv' });
    const enabled = data.enabled !== false;
    const persistence = asString(data.persistence || 'in_memory');
    const persistenceOk = data.persistence_ok === true;
    const kind = enabled ? 'good' : 'warn';
    const persistKind = persistenceOk ? 'good' : 'warn';

    [
      [t('customerSecurity.lpEnabled') || 'Enabled',         badge(enabled ? 'on' : 'off', kind)],
      [t('customerSecurity.lpPersistence') || 'Persistence', badge(persistenceOk ? persistence : persistence + ' (degraded)', persistKind)],
      [t('customerSecurity.lpLockoutThreshold') || 'Lockout threshold',
        data.lockout_threshold != null ? fmtNumber(data.lockout_threshold) : '—'],
      [t('customerSecurity.lpLockoutWindow') || 'Lockout window',
        data.lockout_window_seconds != null ? fmtNumber(data.lockout_window_seconds) + ' s' : '—'],
      [t('customerSecurity.lpRateLimit') || 'Rate limit',
        data.rate_limit_per_minute != null ? fmtNumber(data.rate_limit_per_minute) + ' / min' : '—'],
      [t('customerSecurity.lpUpdated') || 'Last updated',
        data.updated_at ? fmtShortDate(data.updated_at) : '—'],
    ].forEach(([k, v]) => {
      const dt = el('dt', { text: k });
      const dd = el('dd', { class: 'kv-v' });
      if (v instanceof Node) dd.appendChild(v); else dd.textContent = String(v);
      dl.appendChild(dt);
      dl.appendChild(dd);
    });
    lpCard.appendChild(dl);
    lpCard.appendChild(el('p', { class: 'subtle ops-honest-note',
      text: t('customerSecurity.lpHonestNote')
        || 'OS-level Fail2Ban is not asserted here. Verify host-level protection separately if you depend on it.' }));
  }).catch((err) => {
    lpCard.innerHTML = '';
    lpCard.appendChild(el('div', { class: 'error',
      text: (t('customerSecurity.lpError') || 'Login-protection status failed: ') + ((err && err.message) || err) }));
  });

  // MFA
  mfaCard.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));
  apiGet('/api/v1/admin/mfa/status').then((data) => {
    mfaCard.innerHTML = '';
    if (!data) {
      mfaCard.appendChild(el('div', { class: 'empty', text: t('customerSecurity.emptyMfa') || 'No MFA status returned.' }));
      return;
    }
    const supported = data.supported === true || data.enabled === true || !!data.totp;
    const configured = !!data.configured;
    const required = !!data.required;
    mfaCard.appendChild(el('div', { class: 'mfa-summary' }, [
      el('div', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: t('customerSecurity.mfaSupported') || 'TOTP supported' }),
        el('span', { class: 'kv-v' }, badge(supported ? 'yes' : 'no', supported ? 'good' : 'neutral')),
      ]),
      el('div', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: t('customerSecurity.mfaConfigured') || 'Configured for this account' }),
        el('span', { class: 'kv-v' }, badge(configured ? 'yes' : 'no', configured ? 'good' : 'neutral')),
      ]),
      el('div', { class: 'kv-row' }, [
        el('span', { class: 'kv-k', text: t('customerSecurity.mfaRequired') || 'Required by policy' }),
        el('span', { class: 'kv-v' }, badge(required ? 'yes' : 'no', required ? 'warn' : 'neutral')),
      ]),
    ]));
    mfaCard.appendChild(el('p', { class: 'subtle ops-honest-note',
      text: required
        ? (t('customerSecurity.mfaHint') || 'Sign-in will require a TOTP step. Configure it on the Settings page.')
        : (t('customerSecurity.mfaOptional') || 'TOTP is supported. Configure it on the Settings page for additional sign-in protection.') }));
  }).catch((err) => {
    mfaCard.innerHTML = '';
    mfaCard.appendChild(el('div', { class: 'error',
      text: (t('customerSecurity.mfaError') || 'MFA status failed: ') + ((err && err.message) || err) }));
  });

  // Lockouts
  lockCard.appendChild(el('div', { class: 'panel-loading', text: t('common.loading') || 'Loading…' }));
  apiGet('/api/v1/admin/login-protection/lockouts').then((data) => {
    lockCard.innerHTML = '';
    const list = (data && (data.lockouts || data)) || [];
    if (!Array.isArray(list) || list.length === 0) {
      lockCard.appendChild(el('div', { class: 'empty-state' }, [
        el('span', { class: 'empty-illustration', text: '✓' }),
        el('div', { class: 'empty-title', text: t('customerSecurity.lockoutsEmpty') || 'No active lockouts' }),
        el('div', { class: 'empty-hint',
          text: t('customerSecurity.lockoutsHint')
            || 'When accounts cross the lockout threshold their key appears here until the lockout window expires or an admin clears it.' }),
      ]));
      return;
    }
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: t('customerSecurity.lockoutsKey')    || 'Key' }),
      el('th', { text: t('customerSecurity.lockoutsCount')  || 'Failures' }),
      el('th', { text: t('customerSecurity.lockoutsUntil') || 'Until' }),
    ])));
    const tb = el('tbody');
    list.forEach((row) => {
      tb.appendChild(el('tr', null, [
        el('td', { class: 'kv-v', text: row.key || row.identifier || '-' }),
        el('td', { class: 'kv-v', text: fmtNumber(row.count || row.failures || 0) }),
        el('td', { class: 'kv-v', text: row.until ? fmtShortDate(row.until) : (row.expires_at ? fmtShortDate(row.expires_at) : '—') }),
      ]));
    });
    tbl.appendChild(tb);
    lockCard.appendChild(tbl);
  }).catch((err) => {
    lockCard.innerHTML = '';
    lockCard.appendChild(el('div', { class: 'error',
      text: (t('customerSecurity.lockoutsError') || 'Lockouts lookup failed: ') + ((err && err.message) || err) }));
  });

  applyAutoDir(root);
}

// statusKind is re-exported so other modules that rely on this
// page's helpers can be rewritten incrementally without renaming;
// unused today but harmless.
export { statusKind };
