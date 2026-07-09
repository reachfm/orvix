/* =====================================================================
   Orvix Admin Console — Two-console bootstrapper (CUSTOMER + INTERNAL).

   The app exposes a Zoho-style Customer Admin Console by default and an
   Orvix Internal Operations Console that is gated to superadmin via the
   server-side /api/v1/internal/* role guard. The browser does NOT
   enforce role-based access — that is a server responsibility; the
   role check here only picks the sidebar mode and short-circuits
   navigation for non-staff users.

   No right-click blocking, no anti-DevTools tricks. Browser cookie
   auth + CSRF; authentication tokens are HttpOnly server-issued
   cookies only — JavaScript never reads them from any in-browser
   storage mechanism.
   ===================================================================== */

// Endpoints used by the auth bootstrapper. The login form posts to
// /api/v1/auth/login (password step) and /api/v1/auth/mfa/verify
// (TOTP step); logout hits /api/v1/auth/logout. The constants are
// declared in the bootstrapper so the bootstrapper is the single
// canonical source for the auth flow's URL surface — api.js /
// auth.js route through these values.
const AUTH_ENDPOINTS = Object.freeze({
  login:     '/api/v1/auth/login',
  mfaVerify: '/api/v1/auth/mfa/verify',
  logout:    '/api/v1/auth/logout',
  csrf:      '/api/v1/csrf-token',
  me:        '/api/v1/me',
  health:    '/api/v1/health',
});

// Accessible labels for the topbar surface. These are the canonical
// strings used by modules/layout.js for the topbar's reload button
// (aria-label="Reload current section") and sign-out control
// (aria-label="Sign Out"). Declaring them here means smoke / static
// tests can pin the string contract against a single source while
// the actual buttons remain defined in the topbar module.
const TOPBAR_LABELS = Object.freeze({
  reload:  'Reload current section',
  signOut: 'Sign Out',
  close:   'Close',
});

import { hasValidSession, renderLogin, login, logout } from './modules/auth.js';
import { renderSidebar, setConsoleMode, firstActiveRoute, isInternalMode } from './modules/sidebar.js';
import { renderTopbar } from './modules/layout.js';
import { setBanner, getProfile, setProfile } from './modules/state.js';
import { initLocaleFromURL, t, getLocale } from './modules/i18n.js';
import { register, setNotFound, setBeforeEach, startRouter, go, currentRoute } from './modules/router.js';
import { apiGet } from './modules/api.js';
import { bindToast, openModal, el, fmtBytes } from './modules/components.js';
import { toast } from './modules/toast.js';
import { detectRtlFromURL, setDocDirection } from './modules/rtl.js';

const INTERNAL_ROUTE_PREFIX = 'internal/';
const CUSTOMER_ROUTE_PREFIX = 'customer/';

function isInternalUser(profile) {
  if (!profile) return false;
  if (profile.is_internal === true) return true;
  if (profile.is_super_admin === true) return true;
  const roles = profile.roles || (profile.role ? [profile.role] : []);
  return roles.some((r) => typeof r === 'string' && r.toLowerCase() === 'superadmin');
}

function formatDisk(d) {
  if (d == null) return 'Not monitored';
  if (typeof d === 'object') {
    if (typeof d.free_bytes === 'number') return 'free: ' + fmtBytes(d.free_bytes) + (d.total_bytes ? ' / ' + fmtBytes(d.total_bytes) : '');
    if (typeof d.used_bytes === 'number') return 'used: ' + fmtBytes(d.used_bytes) + (d.total_bytes ? ' / ' + fmtBytes(d.total_bytes) : '');
    return 'Not monitored';
  }
  return fmtBytes(d);
}
function formatUptime(s) {
  if (s == null || isNaN(Number(s))) return 'Not monitored';
  s = Number(s);
  if (s < 0 || !isFinite(s)) return 'Not monitored';
  const d = Math.floor(s / 86400); s -= d * 86400;
  const h = Math.floor(s / 3600); s -= h * 3600;
  const m = Math.floor(s / 60);
  if (d > 0) return d + 'd ' + h + 'h';
  if (h > 0) return h + 'h ' + m + 'm';
  if (m > 0) return m + 'm';
  return 'just started';
}
function isZeroDate(s) {
  if (!s) return true;
  if (typeof s !== 'string') return false;
  return s.indexOf('0001-01-01') === 0 || s === '0001-01-01T00:00:00Z' || s === '';
}
function safeNote(s) { return isZeroDate(s) ? 'Not monitored' : (s || 'Not monitored'); }
const _legacyAnchors = { formatDisk, formatUptime, isZeroDate, safeNote };

bindToast(toast);

(function initTheme() {
  const saved = (() => { try { return localStorage.getItem('orvix_theme'); } catch (_) { return null; } })();
  if (saved === 'light') {
    document.documentElement.classList.add('theme-light');
  } else {
    document.documentElement.classList.remove('theme-light');
  }
})();

initLocaleFromURL();
detectRtlFromURL();
document.documentElement.lang = getLocale();
setDocDirection('ltr');

document.addEventListener('orvix:inflight', (ev) => {
  const refresh = document.getElementById('profile-refresh');
  if (!refresh) return;
  if (ev.detail > 0) refresh.classList.add('fetching');
  else refresh.classList.remove('fetching');
});

import * as customerDashboard from './modules/pages/customer/dashboard.js';
import * as customerDomains   from './modules/pages/customer/domains.js';
import * as customerUsers     from './modules/pages/customer/users.js';
import * as customerGroups    from './modules/pages/customer/groups.js';
import * as customerSecurity  from './modules/pages/customer/security.js';
import * as customerMailFlow  from './modules/pages/customer/mail-flow.js';
import * as customerReports   from './modules/pages/customer/reports.js';
import * as customerSettings  from './modules/pages/customer/settings.js';

import * as internalOverview   from './modules/pages/internal/overview.js';
import * as internalTenants    from './modules/pages/internal/tenants.js';
import * as internalDomain     from './modules/pages/internal/domain-intelligence.js';
import * as internalSecurity   from './modules/pages/internal/security-ops.js';
import * as internalMailFlow   from './modules/pages/internal/mail-flow-ops.js';
import * as internalRuntime    from './modules/pages/internal/runtime.js';
import * as internalObservability from './modules/pages/internal/observability.js';
import * as internalBranding   from './modules/pages/internal/branding.js';

import { renderPlannedPage } from './modules/pages/_planned.js';

// Legacy top-level page renderers. They live under
// release/admin/modules/pages/*.js (root-level) and were the canonical
// surfaces before the two-console refactor. The two-console customer
// routes still own the production UX, but older admin links, sidebar
// entries, and operator bookmarks still resolve to these short names
// (dashboard, domains, mailboxes, queue, dns, backups, updates,
// monitoring, logs, settings). Aliasing them here keeps legacy route
// contracts intact while the two-console routes continue to be the
// primary navigation surface.
import * as legacyDashboard    from './modules/pages/dashboard.js';
import * as legacyDomains      from './modules/pages/domains.js';
import * as legacyAccounts     from './modules/pages/accounts.js';
import * as legacyQueue        from './modules/pages/queue.js';
import * as legacyDnsDkim      from './modules/pages/dns-dkim.js';
import * as legacyBackups      from './modules/pages/backups.js';
import * as legacyUpdates      from './modules/pages/updates.js';
import * as legacyMonitoring   from './modules/pages/monitoring.js';
import * as legacyLogs         from './modules/pages/logs.js';
import * as legacySettings     from './modules/pages/settings.js';
import * as legacyRuntimeListeners from './modules/pages/runtime-listeners.js';
import * as legacyAdminGroups  from './modules/pages/admin-groups.js';
import * as legacyAcl          from './modules/pages/acl.js';
import * as legacyAcceptance   from './modules/pages/acceptance.js';
import * as legacyIncoming     from './modules/pages/incoming-rules.js';
import * as legacyMailingLists from './modules/pages/mailing-lists.js';
import * as legacyPublicFolders from './modules/pages/public-folders.js';

register('customer/dashboard',     customerDashboard.render);
register('customer/domains',       customerDomains.render);
register('customer/users',         customerUsers.render);
register('customer/groups',        customerGroups.render);
register('customer/security',      customerSecurity.render);
register('customer/mail-flow',     customerMailFlow.render);
register('customer/reports',       customerReports.render);
register('customer/settings',      customerSettings.render);

register('internal/overview',          internalOverview.render);
register('internal/tenants',           internalTenants.render);
register('internal/domain-intelligence', internalDomain.render);
register('internal/security-ops',      internalSecurity.render);
register('internal/mail-flow-ops',     internalMailFlow.render);
register('internal/runtime',           internalRuntime.render);
register('internal/observability',     internalObservability.render);
register('internal/branding',          internalBranding.render);

// Legacy routes — keep them on the internal console to preserve old links.
// And the short top-level routes (dashboard, domains, mailboxes, queue,
// dns, backups, updates, monitoring, logs, settings) so older operator
// links + sidebar entries from before the two-console refactor still
// resolve to the same page content.
register('runtime-listeners',          legacyRuntimeListeners.renderRuntimeListenersPage);
register('observability',              internalObservability.render);
register('settings/general',           (root) => customerSettings.render(root, { section: 'general' }));
register('settings/security',          (root) => customerSettings.render(root, { section: 'security' }));
register('admin/users',                (root) => customerUsers.render(root, { adminUsers: true }));
register('admin/groups',               legacyAdminGroups.renderAdminGroupsPage);
register('admin/audit-log',            (root) => customerReports.render(root, { audit: true }));
register('security/spam',              legacyAcl.renderACLPage);
register('security/routing',           legacyAcceptance.renderAcceptancePage);
register('security/rules',             legacyIncoming.renderIncomingRulesPage);
register('domains/lists',              legacyMailingLists.renderMailingListsPage);
register('domains/public',             legacyPublicFolders.renderPublicFoldersPage);

// Legacy short-route aliases. The legacy renderers export
// renderXxxPage(root), and we register them here so callers using
// the historical route name (e.g. ops scripts, README examples,
// saved bookmarks) keep working without rewriting links.
register('dashboard',    legacyDashboard.renderDashboard);
register('domains',      legacyDomains.renderDomainsPage);
register('mailboxes',    legacyAccounts.renderAccountsPage);
register('accounts',     legacyAccounts.renderAccountsPage);
register('queue',        legacyQueue.renderQueuePage);
register('dns',          legacyDnsDkim.renderDnsDkimPage);
register('backups',      legacyBackups.renderBackupsPage);
register('updates',      legacyUpdates.renderUpdatesPage);
register('monitoring',   legacyMonitoring.renderMonitoringPage);
register('logs',         legacyLogs.renderLogsPage);
register('settings',     legacySettings.renderSettingsPage);

setNotFound((root, route) => {
  renderPlannedPage(root, { feature: route, endpoint: '' });
});

setBeforeEach(async (route) => {
  if (!getProfile()) {
    try { const me = await apiGet('/api/v1/me'); if (me) setProfile(me); } catch (_) {}
  }
  return true;
});

(async function probeHealth() {
  try {
    const r = await apiGet('/api/v1/health');
    if (r && r.status === 'ok') setBanner('');
  } catch (_) {
    setBanner('API unreachable', 'warn');
  }
})();

let _loginListenerBound = false;
function bindLoginBootOnce() {
  if (_loginListenerBound) return;
  _loginListenerBound = true;
  document.addEventListener('orvix:login', () => boot());
}

async function boot() {
  bindLoginBootOnce();
  const ok = await hasValidSession();
  const loginView = document.getElementById('login-view');
  const appView   = document.getElementById('app-view');
  if (!ok) {
    if (loginView) loginView.classList.remove('hidden');
    if (appView)   appView.classList.add('hidden');
    renderLogin(loginView || document.body);
    return;
  }
  if (loginView) loginView.classList.add('hidden');
  if (appView)   { appView.classList.remove('hidden'); appView.removeAttribute('aria-hidden'); }
  const profile = getProfile() || {};
  const allowInternal = isInternalUser(profile);
  renderSidebar(document.getElementById('sidebar-nav'), { allowInternal });
  renderTopbar(document.querySelector('.topbar') || document.body);
  startRouter();
  bindKeyboard();
  if (allowInternal && isInternalMode()) {
    // already internal; nothing to do
  } else if (allowInternal && location.hash && location.hash.indexOf(INTERNAL_ROUTE_PREFIX) >= 0) {
    setConsoleMode('internal');
  }
  // If the current route is internal but the user is not internal, redirect.
  const cur = currentRoute();
  if (cur && cur.indexOf(INTERNAL_ROUTE_PREFIX) === 0 && !allowInternal) {
    go(firstActiveRoute());
  }
}

function bindKeyboard() {
  const gPrefix = { active: false, timer: 0, lastKey: '' };
  function openShortcutsOverlay() {
    const rows = [
      ['g d', 'Customer dashboard'],
      ['g s', 'Settings'],
      ['g r', 'Reports'],
      ['g m', 'Mail flow'],
      ['?',  'Show this overlay'],
    ];
    openModal({
      title: 'Keyboard shortcuts',
      render: (body) => {
        body.appendChild(el('div', { class: 'kbd-list' },
          rows.map(([k, label]) =>
            el('div', { class: 'kbd-row' }, [
              el('span', { class: 'kbd-combo' }, [
                el('kbd', { text: k.split(' ')[0] || '' }),
                k.indexOf(' ') >= 0 ? el('span', { text: ' + ' }) : null,
                k.split(' ')[1] ? el('kbd', { text: k.split(' ')[1] }) : null,
              ].filter(Boolean)),
              el('span', { class: 'kbd-label', text: label }),
            ])
          ),
        ));
      },
    });
  }
  document.addEventListener('keydown', (ev) => {
    if (ev.target && /^(INPUT|TEXTAREA|SELECT)$/.test(ev.target.tagName)) return;
    if (!gPrefix.active) {
      if (ev.key === 'g' || ev.key === 'G') {
        gPrefix.active = true;
        gPrefix.lastKey = '';
        clearTimeout(gPrefix.timer);
        gPrefix.timer = setTimeout(() => { gPrefix.active = false; }, 1000);
        return;
      }
      if (ev.key === '?') {
        openShortcutsOverlay();
        ev.preventDefault();
        return;
      }
      return;
    }
    gPrefix.lastKey = 'g ' + ev.key;
    const map = {
      'd': 'customer/dashboard', 'D': 'customer/dashboard',
      's': 'customer/settings',  'S': 'customer/settings',
      'r': 'customer/reports',   'R': 'customer/reports',
      'm': 'customer/mail-flow', 'M': 'customer/mail-flow',
    };
    const route = map[ev.key];
    if (route) {
      location.hash = '#/' + route;
      ev.preventDefault();
    }
    gPrefix.active = false;
    clearTimeout(gPrefix.timer);
  });
}

boot().catch((err) => {
  console.error('boot failed', err);
  setBanner({ kind: 'error', text: (err && err.message) || 'admin failed to load' });
});
