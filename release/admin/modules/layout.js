/* =====================================================================
   modules/layout.js — Topbar (operator profile + global banner).

   The layout module renders and updates the topbar chrome:
     * profile email + role badge
     * inflight spinner (fetches in progress)
     * locale switcher (en/ar)
     * refresh + logout

   It also paints the global banner (set by api.js or pages when a
   non-2xx happens that the operator should know about).
   ===================================================================== */

import { el, fmtDate, $ } from './components.js';
import { logout } from './auth.js';
import { setLocale, getLocale, t, onLocaleChange } from './i18n.js';
import { getProfile } from './state.js';

export function renderTopbar(root) {
  if (!root) return;
  root.innerHTML = '';

  const title = el('div', { class: 'topbar-title' }, [
    el('strong', { text: t('dashboard.heading') }),
    el('span', { id: 'page-subtitle', text: t('topbar.subtitle') }),
  ]);
  root.appendChild(title);

  const profile = el('div', { class: 'profile' });
  const meta = el('div', { class: 'profile-meta' }, [
    el('div', { id: 'profile-email', class: 'profile-email', text: '' }),
    el('div', { id: 'profile-role', class: 'profile-role', text: '' }),
  ]);
  profile.appendChild(meta);
  profile.appendChild(el('div', { id: 'profile-avatar', class: 'profile-avatar', 'aria-hidden': 'true', text: 'A' }));

  // Theme toggle — cycles light/dark. Persisted in localStorage.
  const themeBtn = el('button', {
    id: 'theme-toggle', class: 'btn xs ghost', type: 'button',
    'aria-label': 'Toggle theme',
    title: 'Toggle theme',
  });
  function paintThemeIcon() {
    const isLight = document.documentElement.classList.contains('theme-light');
    themeBtn.textContent = isLight ? '\u2600' : '\u263D';
  }
  paintThemeIcon();
  themeBtn.addEventListener('click', () => {
    document.documentElement.classList.toggle('theme-light');
    const nowLight = document.documentElement.classList.contains('theme-light');
    try { localStorage.setItem('orvix_theme', nowLight ? 'light' : 'dark'); } catch (_) {}
    paintThemeIcon();
  });
  profile.appendChild(themeBtn);

  // Locale switcher
  const localeSel = el('select', { class: 'btn xs ghost', id: 'locale-select', 'aria-label': 'Language' }, [
    el('option', { value: 'en' }, 'English'),
    el('option', { value: 'ar' }, '\u0627\u0644\u0639\u0631\u0628\u064a\u0629'),
  ]);
  localeSel.value = getLocale();
  localeSel.addEventListener('change', () => {
    setLocale(localeSel.value);
    try { localStorage.setItem('orvix_locale', localeSel.value); } catch (_) {}
    document.documentElement.lang = localeSel.value;
    document.documentElement.setAttribute('dir', localeSel.value === 'ar' ? 'rtl' : 'ltr');
    document.body.setAttribute('dir', localeSel.value === 'ar' ? 'rtl' : 'ltr');
    location.reload();
  });
  profile.appendChild(localeSel);

  const refresh = el('button', {
    id: 'profile-refresh', class: 'btn xs ghost', type: 'button',
    'aria-label': 'Reload current section',
    title: t('topbar.refresh'), text: t('topbar.refresh'),
  });
  refresh.addEventListener('click', () => location.reload());
  profile.appendChild(refresh);

  const out = el('button', {
    id: 'logout-button', class: 'btn xs ghost', type: 'button',
    'aria-label': 'Sign Out',
    text: t('topbar.signOut'),
  });
  out.addEventListener('click', async () => {
    await logout();
    location.reload();
  });
  profile.appendChild(out);

  root.appendChild(profile);

  // Banner (initially empty)
  const banner = el('div', { id: 'topbar-banner', class: 'topbar-banner' });
  root.appendChild(banner);

  // Refresh profile display whenever state changes
  document.addEventListener('orvix:profile', () => paintProfile());
  document.addEventListener('orvix:inflight', (ev) => {
    const on = ev.detail > 0;
    if (on) refresh.classList.add('fetching'); else refresh.classList.remove('fetching');
  });
  document.addEventListener('orvix:banner', (ev) => paintBanner(ev.detail));
  paintProfile();
  paintBanner(null);
}

function paintProfile() {
  const p = getProfile() || {};
  const email = p.email || p.username || 'Signed in';
  const role  = (p.roles && p.roles[0]) || p.role || 'admin';
  const emailEl = $('profile-email');
  const roleEl  = $('profile-role');
  if (emailEl) emailEl.textContent = email;
  if (roleEl)  roleEl.textContent  = role;
  const av = $('profile-avatar');
  if (av) av.textContent = (email && email[0] || 'A').toUpperCase();
}

function paintBanner(msg) {
  const banner = $('topbar-banner');
  if (!banner) return;
  banner.innerHTML = '';
  if (!msg) return;
  const kind = msg.kind || 'info';
  const node = el('div', { class: 'banner banner-' + kind, role: 'status' }, [
    el('span', { class: 'banner-text', text: msg.text || msg.message || '' }),
    el('button', { type: 'button', class: 'banner-dismiss', 'aria-label': 'Dismiss', text: '\u00d7',
      onclick: () => banner.innerHTML = '' }),
  ]);
  banner.appendChild(node);
}
