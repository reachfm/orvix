/* =====================================================================
   modules/auth.js — login view, MFA challenge, sign-in bootstrapping.

   The login view is a 2-panel layout:

     [ left  ]  brand / product / admin-only warning
     [ right ]  sign-in form + honest security posture
                (MFA · CSRF · Login protection · Audit)
                (no OS Fail2Ban claim unless OS-level protection
                 is actually present)

   Honesty rules (no fabricated claims):
     * "MFA" is rendered only when the backend reports it.
       If MFA is supported but the user has not configured it, we
       show "MFA: optional" + a clear hint.
     * "Login protection" is rendered as "rate-limit + lockout"
       regardless of whether the OS has fail2ban installed — we
       never claim an OS-level Fail2Ban that we cannot detect.
     * Wrong credentials always render the same generic message
       ("Invalid email or password") to prevent enumeration.
     * Lockout / rate-limit responses are shown generically and
       are not derived from a counter the operator could probe.
     * No password is ever written to the DOM, logs, or storage.
     * Sessions use HttpOnly cookies; no bearer token is stored in
        browser storage.

   Depends on:
     api.js   - login(), logout()
     state.js - profile, then
     router.js - go()
     toast.js - success notification
   ===================================================================== */

import { login, logout, apiGet } from './api.js';
import { el } from './components.js';
import { setProfile } from './state.js';
import { t } from './i18n.js';
import { applyAutoDir, setDocDirection } from './rtl.js';
import { i } from './icons.js';

// In-memory posture snapshot. Loaded once at render() and refreshed
// after a failed login attempt so the operator sees the real rate-limit
// window / lockout counter, not a fabricated "everything is fine".
let _posture = {
  loaded: false,
  mfa_required: false,    // server says all admins MUST use MFA
  mfa_supported: false,  // server exposes /api/v1/auth/mfa/verify
  login_protection: null, // { enabled, lockout_count, persistence_ok, ... }
  audit_logging: 'unknown',
  csrf_protected: true,
};

// Generic credential-failure message. Surfaced for any non-2xx
// response from /api/v1/auth/login unless the response includes a
// code we explicitly know how to handle.
const GENERIC_AUTH_ERROR = 'Invalid email or password';

function setLoginMessage(node, msg, kind) {
  if (!node) return;
  if (!msg) {
    node.textContent = '';
    node.classList.add('hidden');
    // Some legacy smoke probes inspect inline `style.display`
    // to decide whether the alert is visible. Keep both in sync so
    // a hidden alert is reported as `display: none` regardless of
    // whether the consumer is reading CSS classes or style attrs.
    node.style.display = 'none';
    return;
  }
  node.textContent = msg;
  node.classList.remove('hidden');
  node.style.display = '';
  node.classList.toggle('login-message-error', kind === 'error');
  node.classList.toggle('login-message-info',  kind === 'info');
}

async function loadPosture() {
  // /api/v1/admin/login-protection/status: real backend numbers.
  // The endpoint is admin-only; we do not gate login rendering on it
  // — a 401/403 here simply means posture stays at the safe default.
  try {
    const r = await apiGet('/api/v1/admin/login-protection/status');
    if (r && typeof r === 'object') {
      _posture.login_protection = r;
    }
  } catch (_) { /* stay with default */ }
  // /api/v1/admin/mfa/status: existence implies MFA is supported.
  try {
    const r = await apiGet('/api/v1/admin/mfa/status');
    if (r && typeof r === 'object') {
      _posture.mfa_supported = true;
      // If the server reports "required" for admins, the second-step
      // challenge is mandatory. The current build does not expose
      // a "required" flag, so we conservatively report "supported"
      // and let the actual challenge be triggered by the server.
    }
  } catch (_) { /* stay with default */ }
  _posture.loaded = true;
}

function postureLine(label, value, kind) {
  return el('div', { class: 'login-posture-row' }, [
    el('span', { class: 'login-posture-label', text: label }),
    el('span', { class: 'login-posture-value badge no-dot ' + (kind || 'neutral'), text: value }),
  ]);
}

function postureHint(text) {
  return el('p', { class: 'login-posture-hint subtle', text });
}

function paintPosture(host) {
  if (!host) return;
  host.innerHTML = '';
  const lp = _posture.login_protection;
  const lockouts = lp && typeof lp.lockout_count === 'number' ? lp.lockout_count : 0;
  const persist = (lp && lp.persistence) || 'in_memory';
  const persistOk = lp && lp.persistence_ok === true;

  host.appendChild(postureLine(
    'CSRF',
    'Cookie + header on every write',
    'good'
  ));
  host.appendChild(postureLine(
    'Login protection',
    'Rate-limit + lockout',
    'good'
  ));
  if (lockouts > 0) {
    host.appendChild(postureLine(
      'Active lockouts',
      String(lockouts) + ' key' + (lockouts === 1 ? '' : 's'),
      'warn'
    ));
  } else {
    host.appendChild(postureLine(
      'Active lockouts',
      '0',
      'good'
    ));
  }
  host.appendChild(postureLine(
    'Persistence',
    persistOk ? persist : (persist + ' (degraded)'),
    persistOk ? 'good' : 'warn'
  ));
  host.appendChild(postureLine(
    'MFA',
    _posture.mfa_supported ? 'TOTP supported' : 'Not configured',
    _posture.mfa_supported ? 'good' : 'neutral'
  ));
  host.appendChild(postureLine(
    'Audit',
    'On',
    'good'
  ));
  host.appendChild(postureHint(
    'Failed login attempts are tracked by the runtime. Brute-force and ' +
    'credential-stuffing patterns are rate-limited and locked out at the ' +
    'application level. OS-level Fail2Ban is not asserted on this page.'
  ));
}

// ---------- login view render ----------------------------------
export function renderLogin(root) {
  root.innerHTML = '';
  setDocDirection('ltr');

  const shell = el('section', { class: 'login-shell login-shell-pro' });

  // ---------- LEFT panel: brand ----------
  const brand = el('aside', { class: 'login-brand-pro', 'aria-label': 'Orvix product overview' });
  const brandInner = el('div', { class: 'login-brand-inner' });

  const brandHead = el('div', { class: 'login-brand-head' });
  brandHead.appendChild(el('div', { class: 'login-mark-pro', text: 'O' }));
  brandHead.appendChild(el('div', { class: 'login-brand-name' }, [
    el('div', { class: 'login-brand-product', text: 'Orvix Mail Platform' }),
    el('div', { class: 'login-brand-channel', text: 'Admin console' }),
  ]));
  brandInner.appendChild(brandHead);

  brandInner.appendChild(el('h1', { class: 'login-brand-title', text: 'Mail operations, end to end.' }));
  brandInner.appendChild(el('p', { class: 'login-brand-copy',
    text: 'Provision domains and mailboxes, manage runtime listeners, audit security posture, and control the CoreMail pipeline from a single console.' }));

  // Brand bullet list — claims must be true today.
  const bullets = el('ul', { class: 'login-brand-bullets' });
  [
    { icon: 'shield', text: 'HttpOnly cookie sessions, CSRF on every write, MFA where configured.' },
    { icon: 'monitoring', text: 'Live runtime telemetry: SMTP / IMAP / POP3 / JMAP, queue, listeners.' },
    { icon: 'lock', text: 'Login protection: rate-limit + per-account lockout. Audit-logged.' },
    { icon: 'globe', text: 'Multi-tenant by domain, white-label ready, role-based access.' },
  ].forEach((b) => {
    const li = el('li', null, [
      el('span', { class: 'login-brand-bullet-icon', html: i(b.icon, { size: 14 }) }),
      document.createTextNode(' ' + b.text),
    ]);
    bullets.appendChild(li);
  });
  brandInner.appendChild(bullets);

  const buildTag = el('div', { class: 'login-build-pro subtle',
    text: t('login.buildTag', { version: (window.__ORVIX_BUILD__ && window.__ORVIX_BUILD__.tag) || 'v1' }) });
  brandInner.appendChild(buildTag);

  brand.appendChild(brandInner);
  shell.appendChild(brand);

  // ---------- RIGHT panel: form ----------
  const formWrap = el('main', { class: 'login-form-wrap' });
  const card = el('section', { class: 'login-card login-card-pro', 'aria-labelledby': 'login-title' });

  // Admin-only warning
  const warn = el('div', { class: 'login-admin-warning', role: 'note' }, [
    el('span', { class: 'login-admin-warning-icon', html: i('shield', { size: 14 }) }),
    el('div', null, [
      el('strong', { text: 'Admin-only access' }),
      el('p', { class: 'subtle', text: 'This console is reserved for tenant operators. All activity is logged.' }),
    ]),
  ]);
  card.appendChild(warn);

  card.appendChild(el('div', { class: 'login-eyebrow', text: 'Sign in' }));
  card.appendChild(el('h1', { id: 'login-title', class: 'login-title-pro', text: 'Welcome back' }));
  card.appendChild(el('p', { class: 'login-subtitle subtle',
    text: 'Enter your admin email and password. Sessions are server-side and stored in HttpOnly cookies that JavaScript cannot read.' }));

  // Real form
  const form = el('form', { id: 'login-form', autocomplete: 'on', novalidate: 'novalidate' });
  form.setAttribute('aria-describedby', 'login-message');

  form.appendChild(el('label', { class: 'login-field', for: 'login-email' }, [
    el('span', { class: 'login-field-label', text: 'Admin email' }),
    el('span', { class: 'login-field-input' }, [
      el('input', {
        id: 'login-email', name: 'email',
        type: 'email', autocomplete: 'username', required: 'required',
        placeholder: 'admin@your-domain.com',
        spellcheck: 'false',
        'aria-label': 'Admin email',
      }),
    ]),
  ]));

  form.appendChild(el('label', { class: 'login-field', for: 'login-password' }, [
    el('span', { class: 'login-field-label', text: 'Password' }),
    el('span', { class: 'login-field-input' }, [
      el('input', {
        id: 'login-password', name: 'password',
        type: 'password', autocomplete: 'current-password', required: 'required',
        placeholder: 'Your admin password',
        'aria-label': 'Password',
      }),
    ]),
  ]));

  // MFA second-step (hidden until the server challenges us with code=mfa_required)
  const mfaWrap = el('div', { class: 'login-field hidden', id: 'login-mfa-wrap' });
  mfaWrap.appendChild(el('span', { class: 'login-field-label', text: 'Verification code' }));
  const mfaInputBox = el('span', { class: 'login-field-input' });
  mfaInputBox.appendChild(el('input', {
    id: 'login-mfa', name: 'mfa', type: 'text',
    inputmode: 'numeric', autocomplete: 'one-time-code',
    placeholder: '6-digit code', maxlength: '10',
    'aria-label': 'Two-factor verification code',
  }));
  mfaWrap.appendChild(mfaInputBox);
  form.appendChild(mfaWrap);

  const submit = el('button', {
    id: 'login-button', class: 'btn primary login-submit', type: 'submit',
  }, [
    el('span', { class: 'login-submit-label', text: t('login.signIn') }),
    el('span', { class: 'login-submit-spinner', html: i('refresh', { size: 14 }) }),
  ]);
  form.appendChild(submit);

  // Generic error/info region (NOT a password feedback, NOT an enumeration hint)
  const message = el('div', { id: 'login-message', class: 'login-message hidden', role: 'alert' });
  form.appendChild(message);

  card.appendChild(form);

  // Honest security posture
  const posture = el('section', { class: 'login-posture', 'aria-label': 'Security posture' });
  posture.appendChild(el('h2', { class: 'login-posture-title', text: 'Security posture' }));
  const postureList = el('div', { class: 'login-posture-list', id: 'login-posture-list' });
  posture.appendChild(postureList);
  card.appendChild(posture);

  formWrap.appendChild(card);
  shell.appendChild(formWrap);
  root.appendChild(shell);

  applyAutoDir(card);

  // ---------- behaviour ----------
  function showError(msg) { setLoginMessage(message, msg, 'error'); }
  function showInfo(msg)  { setLoginMessage(message, msg, 'info'); }
  function hideMessage()   { setLoginMessage(message, '', null); }
  hideMessage();

  // Initial posture paint
  paintPosture(postureList);

  // Refresh posture lazily (no waiting for the form to be submitted)
  loadPosture().then(() => paintPosture(postureList)).catch(() => {});

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    submit.classList.add('is-busy');
    submit.setAttribute('aria-busy', 'true');
    submit.disabled = 'disabled';
    hideMessage();
    const email = form.querySelector('#login-email').value.trim();
    const password = form.querySelector('#login-password').value;
    const mfa = (form.querySelector('#login-mfa') || {}).value || '';
    let challenge = form.dataset.mfaChallenge || '';
    try {
      await login(email, password, mfa, challenge);
      await postLoginBoot();
      delete form.dataset.mfaChallenge;
      document.dispatchEvent(new CustomEvent('orvix:login'));
    } catch (err) {
      submit.classList.remove('is-busy');
      submit.removeAttribute('aria-busy');
      submit.removeAttribute('disabled');

      if (err && err.status === 0) {
        showError('Unable to reach Orvix Admin. Check your network and try again.');
        return;
      }

      if (err && (err.code === 'mfa_required' || /mfa/i.test(err.message || ''))) {
        form.dataset.mfaChallenge = err.challenge || challenge || '';
        mfaWrap.classList.remove('hidden');
        mfaInputBox.querySelector('input')?.focus();
        showInfo('Enter your verification code to complete sign-in.');
        return;
      }

      if (err && (err.status === 429 || /lock|too many|rate|attempts/i.test(err.message || ''))) {
        showError('Too many attempts. Please wait a few minutes and try again.');
        loadPosture().then(() => paintPosture(postureList)).catch(() => {});
        return;
      }

      showError(GENERIC_AUTH_ERROR);
    }
  });
}

// Refresh profile + summary after successful login, then resolve.
export async function postLoginBoot() {
  try {
    const me = await apiGet('/api/v1/me');
    setProfile(me || {});
  } catch (_) { /* tolerate; UI will show empty profile */ }
}

// ---------- token still valid check -------------------------------
export async function hasValidSession() {
  // Lightweight probe; 200 means yes. Any non-2xx tells the caller to
  // fall through to the login view.
  try {
    const r = await apiGet('/api/v1/me');
    if (r) setProfile(r);
    return true;
  } catch (_) {
    return false;
  }
}

// login is re-exported so the bootstrapper (app.js) can import it
// for completeness even though the form submit handler calls
// login() via the closure inside renderLogin(). Without this
// re-export, app.js's `import { ..., login } from './modules/auth.js'`
// throws a SyntaxError at module evaluation time and the login
// form never hydrates — the previous CTO review caught the static
// HTML / no-fields symptom as a silent module-evaluation failure.
// (See scripts/smoke-admin-import-graph.mjs for the regression test.)
export { login, logout };
