/* =====================================================================
   modules/auth.js — login view, MFA challenge, sign-in bootstrapping.

   Depends on:
     api.js   - login(), logout()
     state.js - profile, then
     router.js - go()
     toast.js - success notification

   The login view is rendered into #login-view. After successful
   login, the bootstrapper hides login-view and shows app-view.
   ===================================================================== */

import { login, logout } from './api.js';
import { el, esc } from './components.js';
import { setProfile } from './state.js';
import { t } from './i18n.js';
import { applyAutoDir, setDocDirection } from './rtl.js';
import { apiGet } from './api.js';

// ---------- login view render ----------------------------------
export function renderLogin(root) {
  root.innerHTML = '';
  // Reset global dir to LTR for the branded login view; once we
  // know the user's preferred UI language we honor it instead.
  setDocDirection('ltr');

  const shell = el('section', { class: 'login-shell' });
  const brand = el('aside', { class: 'login-brand' }, [
    el('div', null, [
      el('div', { class: 'mark', text: 'O' }),
      el('div', { class: 'brand-title', text: t('login.brandTitle') }),
      el('p', { class: 'brand-copy', text: t('login.brandCopy') }),
    ]),
    el('div', {
      class: 'login-build',
      text: t('login.buildTag', { version: (window.__ORVIX_BUILD__ && window.__ORVIX_BUILD__.tag) || 'v1' }),
    }),
  ]);
  const wrap = el('main', { class: 'login-card-wrap' });
  const card = el('section', { class: 'login-card', 'aria-labelledby': 'login-title' });

  card.appendChild(el('div', { class: 'eyebrow', text: t('login.eyebrow') }));
  card.appendChild(el('h1', { id: 'login-title', text: t('login.title') }));
  card.appendChild(el('p', { class: 'subtle', text: t('login.subtitle') }));

  const form = el('form', { id: 'login-form', autocomplete: 'on' });
  form.appendChild(el('label', null, [
    t('login.username'), ' ',
    el('input', {
      id: 'login-email', name: 'email',
      type: 'email', autocomplete: 'username', required: 'required',
      placeholder: t('login.usernamePh'),
      dir: 'auto',
    }),
  ]));
  form.appendChild(el('label', null, [
    t('login.password'), ' ',
    el('input', {
      id: 'login-password', name: 'password',
      type: 'password', autocomplete: 'current-password', required: 'required',
      placeholder: t('login.passwordPh'),
    }),
  ]));

  const mfaWrap = el('div', { class: 'form-row hidden', id: 'login-mfa-wrap' });
  mfaWrap.appendChild(el('label', null, [
    t('login.mfa'),
    el('input', {
      id: 'login-mfa', name: 'mfa', type: 'text',
      inputmode: 'numeric', autocomplete: 'one-time-code',
      placeholder: '123456',
    }),
  ]));
  form.appendChild(mfaWrap);

  const submit = el('button', {
    id: 'login-button', class: 'btn primary', type: 'submit',
    style: 'width:100%;',
    text: t('login.signIn'),
  });
  form.appendChild(submit);

  const message = el('div', { id: 'login-message', class: 'message error', role: 'alert' });
  form.appendChild(message);

  card.appendChild(form);
  wrap.appendChild(card);
  shell.appendChild(brand);
  shell.appendChild(wrap);
  root.appendChild(shell);

  applyAutoDir(card);

  // Wire submit
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    submit.disabled = 'disabled';
    message.classList.remove('visible');
    message.textContent = '';
    const email = form.querySelector('#login-email').value.trim();
    const password = form.querySelector('#login-password').value;
    const mfa = (form.querySelector('#login-mfa') || {}).value || '';
    try {
      await login(email, password, mfa);
      await postLoginBoot();
      // Hand control back to the bootstrapper; it will hide login-view
      // and reveal the app shell.
      document.dispatchEvent(new CustomEvent('orvix:login'));
    } catch (err) {
      submit.removeAttribute('disabled');
      message.classList.add('visible');
      message.textContent = (err && err.message) || t('login.failed');
      if (err && err.code === 'mfa_required') {
        mfaWrap.classList.remove('hidden');
      }
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
