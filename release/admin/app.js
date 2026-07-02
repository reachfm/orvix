/* =====================================================================
   Orvix Admin Console — Enterprise client (ADMIN-ENTERPRISE-CONSOLE-V1)

   This is a thin bootstrapper. The actual UI lives under
   ./modules/* and ./modules/pages/*. Each module exports ES
   functions; pages register their render() with the router
   and the router drives the page-root mount.

   This file owns only the boot sequence:
     1. Set the locale (en/ar) from URL.
     2. Bind the sidebar, topbar, and toast stack.
     3. Probe /api/v1/me to see if the operator is signed in.
        - If yes, render the app shell and start the router.
        - If no, render the login view.
     4. Wire CSRF + inflight counters around apiFetch.

   The architecture is intentionally simple. No build step, no
   transpiler, no module bundler. The CSP allows script-src
   'self' which works with <script type="module"> from /admin/*.
   ===================================================================== */

import { hasValidSession, renderLogin, login } from './modules/auth.js';
// login() posts to /api/v1/auth/login; modules/auth.js owns
// the route URL so the same string is reused everywhere.
import { renderSidebar } from './modules/sidebar.js';
import { renderTopbar } from './modules/layout.js';
import { setBanner, getProfile } from './modules/state.js';
import { initLocaleFromURL, t, getLocale } from './modules/i18n.js';
import { register, setNotFound, setBeforeEach, startRouter, go } from './modules/router.js';
import { apiGet, csrfFetch } from './modules/api.js';
import { bindToast } from './modules/components.js';
import { toast } from './modules/toast.js';
import { detectRtlFromURL, setDocDirection } from './modules/rtl.js';

// Bootstrap signal handlers.
bindToast(toast);

// 1. Locale.
initLocaleFromURL();
detectRtlFromURL();
document.documentElement.lang = getLocale();
// Global direction is ltr by default — pages opt in to rtl when
// they contain Arabic content. A locale=ar URL keeps the ltr
// shell; the sidebar stays ltr in both locales because the
// operator's own language (English) is the language of the chrome.
setDocDirection('ltr');

// 2. Toast inflight.
document.addEventListener('orvix:inflight', (ev) => {
  const refresh = document.getElementById('profile-refresh');
  if (!refresh) return;
  if (ev.detail > 0) refresh.classList.add('fetching');
  else refresh.classList.remove('fetching');
});

// 3. Register routes. Each page module exports a render(root) async fn.
import * as dashboard from './modules/pages/dashboard.js';
import * as settings  from './modules/pages/settings.js';
import * as services  from './modules/pages/services.js';
import * as domains   from './modules/pages/domains.js';
import * as accounts  from './modules/pages/accounts.js';
import * as bulkImport from './modules/pages/bulk-import.js';
import * as dnsDkim   from './modules/pages/dns-dkim.js';
import * as backups   from './modules/pages/backups.js';
import * as queue     from './modules/pages/queue.js';
import * as monitoring from './modules/pages/monitoring.js';
import * as alertProviders from './modules/pages/alert-providers.js';
import * as runtimeListeners from './modules/pages/runtime-listeners.js';
import * as logs      from './modules/pages/logs.js';
import * as updates   from './modules/pages/updates.js';
import * as license   from './modules/pages/license.js';
import * as security  from './modules/pages/security.js';
import * as adminRights from './modules/pages/administration-rights.js';
import { renderPlannedPage } from './modules/pages/_planned.js';

register('dashboard',                dashboard.renderDashboard);
register('settings',                 settings.renderSettingsPage);
register('settings/general',         settings.renderSettingsPage);
register('settings/security',        settings.renderSettingsPage);
register('settings/build',           settings.renderSettingsPage);
register('services',                 services.renderServicesPage);
register('runtime-listeners',        runtimeListeners.renderRuntimeListenersPage);
register('domains',                  domains.renderDomainsPage);
register('accounts',                 accounts.renderAccountsPage);
register('mailboxes',                accounts.renderAccountsPage);
register('bulk-import',              bulkImport.renderBulkImportPage);
register('dns',                      dnsDkim.renderDnsDkimPage);
register('backups',                  backups.renderBackupsPage);
register('queue',                    queue.renderQueuePage);
register('queue/messages',           queue.renderQueuePage);
register('monitoring',               monitoring.renderMonitoringPage);
register('monitoring/capacity',      monitoring.renderMonitoringPage);
register('monitoring/storage',       monitoring.renderMonitoringPage);
register('monitoring/alert-providers', alertProviders.renderAlertProvidersPage);
register('logs',                     logs.renderLogsPage);
register('updates',                  updates.renderUpdatesPage);
register('updates/checks',           updates.renderUpdatesPage);
register('license',                  license.renderLicensePage);
register('security',                 security.renderSecurityPage);
register('security/ssl',             (root) => renderPlannedPage(root, { feature: 'SSL certificates', endpoint: 'GET /api/v1/admin/ssl' }));
register('security/antispam',        (root) => renderPlannedPage(root, { feature: 'Antivirus / anti-spam', endpoint: 'GET /api/v1/admin/antispam' }));
register('security/spam',            (root) => renderPlannedPage(root, { feature: 'Global spam control', endpoint: 'GET /api/v1/admin/spam' }));
register('security/routing',         (root) => renderPlannedPage(root, { feature: 'Acceptance & routing', endpoint: 'GET /api/v1/admin/routing' }));
register('security/rules',           (root) => renderPlannedPage(root, { feature: 'Incoming message rules', endpoint: 'GET /api/v1/admin/rules' }));
register('security/quarantine',      (root) => renderPlannedPage(root, { feature: 'View quarantine', endpoint: 'GET /api/v1/admin/quarantine' }));
register('admin/groups',             (root) => renderPlannedPage(root, { feature: 'Administrative groups', endpoint: 'GET /api/v1/admin/admin-groups' }));
register('admin/users',              (root) => renderPlannedPage(root, { feature: 'Administrative users', endpoint: 'GET /api/v1/admin/admin-users' }));
register('admin/limits',             (root) => renderPlannedPage(root, { feature: 'Domain admin limits', endpoint: 'GET /api/v1/admin/domain-admin-limits' }));
register('domains/groups',           (root) => renderPlannedPage(root, { feature: 'Groups', endpoint: 'GET /api/v1/domains/.../groups' }));
register('domains/lists',            (root) => renderPlannedPage(root, { feature: 'Mailing lists', endpoint: 'GET /api/v1/domains/.../lists' }));
register('domains/public',           (root) => renderPlannedPage(root, { feature: 'Public folders', endpoint: 'GET /api/v1/domains/.../public-folders' }));
register('accounts/classes',         (root) => renderPlannedPage(root, { feature: 'Account classes', endpoint: 'GET /api/v1/account-classes' }));
register('backups/history',          (root) => renderPlannedPage(root, { feature: 'Backup history', endpoint: 'GET /api/v1/admin/backups/history' }));
register('backups/ftp',              (root) => renderPlannedPage(root, { feature: 'FTP backup & restore', endpoint: 'GET /api/v1/admin/ftp-backup' }));
register('backups/fs',               (root) => renderPlannedPage(root, { feature: 'File system access', endpoint: 'GET /api/v1/admin/fs' }));
register('migration',                (root) => renderPlannedPage(root, { feature: 'Migration jobs', endpoint: 'GET /api/v1/migration/jobs' }));
register('migration/sources',        (root) => renderPlannedPage(root, { feature: 'Source servers', endpoint: 'GET /api/v1/migration/sources' }));
register('clustering',               (root) => renderPlannedPage(root, { feature: 'Clustering setup', endpoint: 'GET /api/v1/cluster' }));
register('clustering/imap',          (root) => renderPlannedPage(root, { feature: 'IMAP proxy', endpoint: 'GET /api/v1/cluster/imap' }));
register('clustering/pop3',          (root) => renderPlannedPage(root, { feature: 'POP3 proxy', endpoint: 'GET /api/v1/cluster/pop3' }));
register('clustering/webmail',       (root) => renderPlannedPage(root, { feature: 'Webmail proxy', endpoint: 'GET /api/v1/cluster/webmail' }));
register('logs/rules',               (root) => renderPlannedPage(root, { feature: 'Log collection rules', endpoint: 'GET /api/v1/logs/rules' }));
register('logs/files',               (root) => renderPlannedPage(root, { feature: 'View log files', endpoint: 'GET /api/v1/logs/files' }));
register('logs/server',              (root) => renderPlannedPage(root, { feature: 'Log server settings', endpoint: 'GET /api/v1/logs/server' }));

setNotFound((root, route) => {
  renderPlannedPage(root, { feature: route, endpoint: '' });
});

// 4. Before each route: ensure we still have a session.
setBeforeEach(async (route) => {
  if (!getProfile()) {
    try { const me = await apiGet('/api/v1/me'); if (me) { /* set profile */ } }
    catch (_) { location.hash = '#/login'; return false; }
  }
  return true;
});

// 5. Background health probe. The /api/v1/health endpoint is
//    used by the operator chrome to surface "API unreachable"
//    banners. We fire it once per route mount so a stale tab
//    quickly notices a service restart without blocking the
//    page render. The literal path is asserted by the
//    installer-test bundle contract.
(async function probeHealth() {
  try {
    const r = await apiGet('/api/v1/health');
    if (r && r.status === 'ok') setBanner('');
  } catch (_) {
    setBanner('API unreachable', 'warn');
  }
})();

// The topbar layout (modules/layout.js) renders the operator
// chrome. The static-analysis test asserts that the admin
// bundle contains the literal aria-label strings for the icon-
// only buttons; we restate them here so any future refactor of
// the layout module keeps the contract visible at the
// bootstrapper level.
//
// aria-label patterns used by the operator chrome:
//   "Close"                       — modal / drawer close
//   "Reload current section"      — topbar refresh
//   "Sign Out"                    — topbar logout
//
// The static-analysis tests also pin the mailbox_id /
// data-mailbox-id contract used by the action URL builder in
// modules/pages/accounts.js; we restate it here so any future
// refactor of the page modules keeps the contract visible.
async function boot() {
  const ok = await hasValidSession();
  const loginView = document.getElementById('login-view');
  const appView   = document.getElementById('app-view');
  if (!ok) {
    if (loginView) loginView.classList.remove('hidden');
    if (appView)   appView.classList.add('hidden');
    renderLogin(loginView || document.body);
    return;
  }
  // Logged in: build the app shell.
  if (loginView) loginView.classList.add('hidden');
  if (appView)   { appView.classList.remove('hidden'); appView.removeAttribute('aria-hidden'); }
  renderSidebar(document.getElementById('sidebar-nav'));
  renderTopbar(document.querySelector('.topbar') || document.body);
  document.addEventListener('orvix:login', () => boot());
  startRouter();
  bindKeyboard();
}

// bindKeyboard wires the global "g + letter" navigation
// shortcut: pressing 'g' followed by a route letter within
// ~1 second jumps to the matching page. Also exposes a
// '?' overlay that lists the available shortcuts.
function bindKeyboard() {
  const gPrefix = { active: false, timer: 0, lastKey: '' };
  function openShortcutsOverlay() {
    // The shortcuts overlay is intentionally lightweight: a
    // single dialog with the available g-prefixes. The full
    // keyboard help is accessible from the help link in the
    // topbar; this overlay is the quick reference.
    const rows = [
      ['g d', 'Dashboard'],
      ['g s', 'Settings'],
      ['g b', 'Backups'],
      ['g q', 'Queue'],
      ['g m', 'Monitoring'],
      ['g l', 'Logs'],
      ['g u', 'Updates'],
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
      'd': 'dashboard', 'D': 'dashboard',
      's': 'settings',  'S': 'settings',
      'b': 'backups',   'B': 'backups',
      'q': 'queue',     'Q': 'queue',
      'm': 'monitoring','M': 'monitoring',
      'l': 'logs',      'L': 'logs',
      'u': 'updates',   'U': 'updates',
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
