/* =====================================================================
   modules/layout.js — Topbar (operator profile + global banner).

   The layout module renders and updates the topbar chrome:
     * operator email + role badge
     * global search (jumps to matching route)
     * inflight spinner (fetches in progress)
     * locale switcher (en/ar)
     * theme toggle, refresh, logout
     * global banner (set by api.js or pages)

   The static-analysis test in the installer asserts the literal
   aria-label strings for the operator chrome buttons; we restate them
   here so any future refactor keeps the contract visible.
   ===================================================================== */

import { el, fmtDate, $ } from './components.js';
import { logout } from './auth.js';
import { setLocale, getLocale, t, onLocaleChange } from './i18n.js';
import { getProfile } from './state.js';
import { i } from './icons.js';

export function renderTopbar(root) {
  if (!root) return;
  root.innerHTML = '';

  // ---- Left: page context (title + breadcrumb-like subtitle) ----
  const left = el('div', { class: 'topbar-left' });
  const title = el('div', { class: 'topbar-title' }, [
    el('strong', { text: t('dashboard.heading') }),
    el('span', { id: 'page-subtitle', text: t('topbar.subtitle') }),
  ]);
  left.appendChild(title);
  root.appendChild(left);

  // ---- Center: search ----
  const search = el('label', { class: 'topbar-search' });
  const searchIcon = el('span', { class: 'search-icon' });
  searchIcon.innerHTML = i('search', { size: 14 });
  search.appendChild(searchIcon);
  const searchInput = el('input', {
    type: 'search', id: 'topbar-search-input',
    placeholder: 'Search settings, routes, or mailboxes…',
    autocomplete: 'off', spellcheck: 'false',
  });
  search.appendChild(searchInput);
  const searchKbd = el('span', { class: 'kbd-hint', text: '/' });
  search.appendChild(searchKbd);
  root.appendChild(search);

  // Quick-route map: query substring -> route
  const ROUTE_HINTS = [
    { kw: ['dashboard', 'overview', 'home'],  route: 'dashboard' },
    { kw: ['domain'],                          route: 'domains' },
    { kw: ['mailbox', 'account', 'user'],      route: 'accounts' },
    { kw: ['queue', 'mail flow'],              route: 'queue' },
    { kw: ['log', 'audit'],                    route: 'logs' },
    { kw: ['setting'],                         route: 'settings/general' },
    { kw: ['security', 'mfa', 'csrf'],         route: 'settings/security' },
    { kw: ['license'],                         route: 'license' },
    { kw: ['runtime', 'listener', 'smtp', 'imap', 'pop3'], route: 'runtime-listeners' },
    { kw: ['backup'],                          route: 'backups' },
    { kw: ['antivirus', 'clamav', 'scan'],     route: 'security/antispam' },
    { kw: ['routing', 'acceptance'],           route: 'security/routing' },
    { kw: ['rule', 'incoming'],                route: 'security/rules' },
    { kw: ['quarantine'],                      route: 'security/quarantine' },
    { kw: ['admin', 'group', 'permission'],    route: 'admin/groups' },
    { kw: ['dns', 'dkim', 'mx', 'spf'],        route: 'dns' },
    { kw: ['mailing', 'list'],                 route: 'domains/lists' },
    { kw: ['public', 'folder'],                route: 'domains/public' },
    { kw: ['monitor', 'metric', 'chart'],      route: 'monitoring' },
    { kw: ['alert'],                           route: 'monitoring/alert-providers' },
    { kw: ['update', 'version'],               route: 'updates' },
  ];
  searchInput.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter') {
      const q = (searchInput.value || '').toLowerCase().trim();
      if (!q) return;
      for (const hint of ROUTE_HINTS) {
        if (hint.kw.some((k) => q.indexOf(k) >= 0)) {
          location.hash = '#/' + hint.route;
          searchInput.blur();
          ev.preventDefault();
          return;
        }
      }
    } else if (ev.key === 'Escape') {
      searchInput.value = '';
      searchInput.blur();
    }
  });
  // Global "/" focuses the search.
  document.addEventListener('keydown', (ev) => {
    if (ev.key === '/' && document.activeElement !== searchInput &&
        !/^(INPUT|TEXTAREA|SELECT)$/.test((document.activeElement || {}).tagName || '')) {
      ev.preventDefault();
      searchInput.focus();
      searchInput.select();
    }
  });

  // ---- Right: profile + theme + locale + actions ----
  const right = el('div', { class: 'topbar-right' });

  const profile = el('div', { class: 'profile' });
  const meta = el('div', { class: 'profile-meta' }, [
    el('div', { id: 'profile-email', class: 'profile-email', text: '' }),
    el('div', { id: 'profile-role', class: 'profile-role', text: '' }),
  ]);
  profile.appendChild(meta);
  profile.appendChild(el('div', { id: 'profile-avatar', class: 'profile-avatar', 'aria-hidden': 'true', text: 'A' }));

  // Theme toggle — cycles dark/light. Persisted in localStorage.
  const themeBtn = el('button', {
    id: 'theme-toggle', class: 'btn xs ghost icon', type: 'button',
    'aria-label': 'Toggle theme',
    title: 'Toggle theme',
  });
  function paintThemeIcon() {
    const isLight = document.documentElement.classList.contains('theme-light');
    themeBtn.innerHTML = isLight ? '☀' : '☾';
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
    el('option', { value: 'ar' }, '\u0627\u0644\u0639\u0631\u0628\u064A\u0629'),
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
    id: 'logout-button', class: 'btn xs ghost danger', type: 'button',
    'aria-label': 'Sign Out',
    text: t('topbar.signOut'),
  });
  out.addEventListener('click', async () => {
    await logout();
    location.reload();
  });
  profile.appendChild(out);

  right.appendChild(profile);
  root.appendChild(right);

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
