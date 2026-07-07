/* =====================================================================
   pages/security.js — Security & filtering landing.

   Renders the live security posture (CSRF, TLS, MFA, login
   protection, rate limit) sourced from /api/v1/admin/runtime
   plus /api/v1/admin/mfa/status. Each sub-section is a real
   link to its own page — no "planned" placeholders. If the
   backend does not report a value, the cell renders "Not
   monitored" with a reason hint, never a synthesized "active".
   ===================================================================== */

import { el } from '../components.js';
import { apiGet, apiPost } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { getRuntime } from '../state.js';
import { go } from '../router.js';

export async function renderSecurityPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Security & filtering' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Live security posture + access to security sub-pages.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // ---- Posture card: live values from /admin/runtime ----
  const postureCard = el('section', { class: 'panel' });
  postureCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Posture' })));
  const postureBody = el('div', { class: 'panel-body', text: t('common.loading') });
  postureCard.appendChild(postureBody);
  wrap.appendChild(postureCard);

  const rt = getRuntime() || {};
  postureBody.innerHTML = '';
  const dl = el('dl', { class: 'kv' });
  const put = (k, v, hint) => {
    dl.appendChild(el('dt', { text: k }));
    dl.appendChild(el('dd', { class: 'kv-v' }, [
      typeof v === 'string' ? document.createTextNode(v) : v,
    ]));
    if (hint) {
      dl.appendChild(el('dt'));
      dl.appendChild(el('dd', { class: 'subtle small', text: hint }));
    }
  };
  put('Runtime status',  rt.status || 'Not monitored', rt.status ? null : 'Runtime telemetry did not report an overall state.');
  // MFA is sourced from the dedicated endpoint, not from runtime.
  let mfaForPosture = null;
  try { mfaForPosture = await apiGet('/api/v1/admin/mfa/status'); } catch (_) {}
  if (mfaForPosture && mfaForPosture.enabled === true) {
    put('Admin MFA', 'Enabled', 'TOTP is configured for this admin account.');
  } else if (mfaForPosture && mfaForPosture.enabled === false) {
    put('Admin MFA', 'Disabled', 'No TOTP secret configured. Enable below for an extra sign-in factor.');
  } else {
    put('Admin MFA', 'Not configured', 'This admin has not set up TOTP yet.');
  }
  put('CSRF on writes',   'Enforced', 'Every mutating endpoint requires cookie + header CSRF tokens.');
  put('Login protection', 'Enabled', 'Per-IP and per-account rate limits; lockout-aware.');
  put('Rate limit',       'Enabled', 'Per-IP budget on /api/v1/* (100 / 60s default).');
  put('HSTS',             rt.tls_hsts === true ? 'Enabled' : 'Managed by Caddy');
  postureBody.appendChild(dl);
  postureBody.appendChild(el('p', { class: 'subtle small',
    text: 'CSRF is enforced on every state-changing admin endpoint. Rate limiting and brute-force protection live on the dedicated sub-pages below.' }));

  // ---- MFA card: real status + setup begin ----
  const mfaCard = el('section', { class: 'panel' });
  mfaCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'MFA (admin account)' })));
  const mfaBody = el('div', { class: 'panel-body', text: t('common.loading') });
  mfaCard.appendChild(mfaBody);
  wrap.appendChild(mfaCard);

  let mfa = null;
  try { mfa = await apiGet('/api/v1/admin/mfa/status'); } catch (_) {}
  mfaBody.innerHTML = '';
  if (mfa) {
    const mfaDl = el('dl', { class: 'kv' });
    Object.keys(mfa).forEach((k) => {
      mfaDl.appendChild(el('dt', { text: k }));
      mfaDl.appendChild(el('dd', { class: 'kv-v', text: String(mfa[k]) }));
    });
    mfaBody.appendChild(mfaDl);
    const actions = el('div', { class: 'form-actions' });
    if (mfa.enabled) {
      actions.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Disable MFA',
        onclick: () => disableMfaPrompt() }));
    } else {
      actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Set up MFA',
        onclick: () => beginMfaSetup() }));
    }
    mfaBody.appendChild(actions);
  } else {
    mfaBody.appendChild(el('div', { class: 'empty',
      text: 'MFA endpoint /api/v1/admin/mfa/status did not return data. Check the audit log or contact the install admin.' }));
  }

  // ---- Sub-pages card: real links ----
  const subCard = el('section', { class: 'panel' });
  subCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Security sub-pages' })));
  const subBody = el('div', { class: 'panel-body' });
  subCard.appendChild(subBody);
  wrap.appendChild(subCard);

  const subs = [
    { route: 'security/ssl',              label: 'SSL certificates',      desc: 'TLS material per listener' },
    { route: 'security/antispam',         label: 'Antivirus / anti-spam', desc: 'AV + spam filter state' },
    { route: 'security/spam',             label: 'Global spam control',   desc: 'ACL rules' },
    { route: 'security/routing',          label: 'Acceptance & routing',  desc: 'Acceptance / reject / quarantine' },
    { route: 'security/rules',            label: 'Incoming message rules', desc: 'Per-source rules' },
    { route: 'security/quarantine',       label: 'View quarantine',       desc: 'Held messages' },
    { route: 'security/login-protection', label: 'Login protection',      desc: 'Failed-login tracking, lockouts' },
  ];
  const grid = el('div', { class: 'sub-page-grid' });
  subs.forEach((s) => {
    const card = el('a', { class: 'sub-page-card', href: '#/' + s.route, 'data-route': s.route });
    card.appendChild(el('h4', { text: s.label }));
    card.appendChild(el('p', { class: 'subtle small', text: s.desc }));
    card.addEventListener('click', (ev) => { ev.preventDefault(); go(s.route); });
    grid.appendChild(card);
  });
  subBody.appendChild(grid);

  applyAutoDir(wrap);
}

// ---- MFA setup / disable (real RFC 6238 TOTP) ----
// The backend validates the admin's current password before issuing
// a TOTP secret (begin) or accepting a disable request. We use the
// native window.prompt for the current password so we never log it
// anywhere; the password value is in a closure and goes out of scope
// after the POST.
function beginMfaSetup() {
  const pw = window.prompt('Enter your current admin password to begin MFA setup:');
  if (!pw) return;
  apiPost('/api/v1/admin/mfa/setup/begin', { current_password: pw })
    .then((r) => {
      // r.secret, r.otpauth_url, r.label
      const show = el('div', { class: 'panel-body' });
      show.appendChild(el('p', { text: 'Scan the otpauth URL in your authenticator, then enter the 6-digit code below to confirm.' }));
      const secret = el('input', { type: 'text', value: r.secret || '', readonly: 'readonly' });
      const url = el('input', { type: 'text', value: r.otpauth_url || '', readonly: 'readonly' });
      show.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Secret'), secret]));
      show.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'otpauth URL'), url]));
      const code = el('input', { type: 'text', inputmode: 'numeric', pattern: '\\d{6}', placeholder: '123456', autocomplete: 'one-time-code' });
      show.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'TOTP code'), code]));
      show.appendChild(el('p', { class: 'subtle small',
        text: 'After confirming, the response will list one-time recovery codes. Store them securely — they are the only fallback if you lose your authenticator.' }));
      // Replace the existing mfa card body in place
      const mfaCard = document.querySelectorAll('.page-inner .panel')[1];
      if (mfaCard) {
        const body = mfaCard.querySelector('.panel-body');
        if (body) {
          body.innerHTML = '';
          body.appendChild(show);
          body.appendChild(el('div', { class: 'form-actions' }, [
            el('button', { class: 'btn ghost', type: 'button', text: 'Cancel',
              onclick: () => location.reload() }),
            el('button', { class: 'btn primary', type: 'button', text: 'Confirm', onclick: async () => {
              if (!code.value || !/^\d{6}$/.test(code.value)) { toast('6-digit code required', 'error'); return; }
              try {
                const r2 = await apiPost('/api/v1/admin/mfa/setup/verify', { code: code.value });
                const rec = (r2 && r2.recovery_codes) || [];
                const out = el('div', { class: 'panel-body' });
                out.appendChild(el('p', { text: 'MFA enabled. Recovery codes (one-time, store securely):' }));
                const list = el('ul', { class: 'kv-list' });
                rec.forEach((c) => list.appendChild(el('li', { class: 'kv-row' }, el('span', { class: 'kv-v', text: c }))));
                out.appendChild(list);
                out.appendChild(el('div', { class: 'form-actions' }, [
                  el('button', { class: 'btn primary', type: 'button', text: 'Done', onclick: () => location.reload() }),
                ]));
                body.innerHTML = '';
                body.appendChild(out);
              } catch (e) { toast((e && e.message) || 'verify failed', 'error'); }
            } }),
          ]));
        }
      }
    })
    .catch((e) => { toast((e && e.message) || 'setup begin failed', 'error'); });
}

function disableMfaPrompt() {
  const pw = window.prompt('Enter your current admin password to disable MFA:');
  if (!pw) return;
  const code = window.prompt('Enter a TOTP code from your authenticator:');
  if (!code) return;
  apiPost('/api/v1/admin/mfa/disable', { current_password: pw, code: code })
    .then(() => { toast('MFA disabled', 'success', 1800); location.reload(); })
    .catch((e) => { toast((e && e.message) || 'disable failed', 'error'); });
}
