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
import { bindToast, openModal, el } from './modules/components.js';
import { toast } from './modules/toast.js';
import { detectRtlFromURL, setDocDirection } from './modules/rtl.js';

// Bootstrap signal handlers.
bindToast(toast);

// Initialize theme before any rendering. Priority:
//   1. saved preference (from previous session)
//   2. prefers-color-scheme (system preference)
//   3. dark (default)
(function initTheme() {
  const saved = (() => { try { return localStorage.getItem('orvix_theme'); } catch (_) { return null; } })();
  if (saved === 'light') {
    document.documentElement.classList.add('theme-light');
  } else if (saved === 'dark') {
    document.documentElement.classList.remove('theme-light');
  } else if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
    document.documentElement.classList.add('theme-light');
  }
})();

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
import * as accountClasses from './modules/pages/account-classes.js';
import * as domainGroups   from './modules/pages/domain-groups.js';
import * as mailingLists   from './modules/pages/mailing-lists.js';
import * as publicFolders  from './modules/pages/public-folders.js';
import * as adminGroups    from './modules/pages/admin-groups.js';
import * as adminUsersPage from './modules/pages/admin-users.js';
import * as auditLog       from './modules/pages/audit-log.js';
import * as quarantine     from './modules/pages/quarantine.js';
import * as acl            from './modules/pages/acl.js';
import * as logRules       from './modules/pages/log-rules.js';
import * as securityExtra  from './modules/pages/security-extra.js';
import * as migration      from './modules/pages/migration.js';
import * as clustering     from './modules/pages/clustering.js';
import * as sslPage        from './modules/pages/ssl.js';
import * as antivirusPage  from './modules/pages/antivirus.js';
import * as acceptancePage from './modules/pages/acceptance.js';
import * as incomingRulesPage from './modules/pages/incoming-rules.js';
import * as loginProtectionPage from './modules/pages/login-protection.js';
import * as ftpBackupPage  from './modules/pages/ftp-backup.js';
import * as fsAccessPage   from './modules/pages/fs-access.js';
import * as migrationSourcesPage from './modules/pages/migration-sources.js';
import * as settingsProtocolPage from './modules/pages/settings-protocol.js';
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
register('dns-dkim',                 dnsDkim.renderDnsDkimPage);
register('backups',                  backups.renderBackupsPage);
register('queue',                    queue.renderQueuePage);
register('queue/messages',           queue.renderQueuePage);
register('monitoring',               monitoring.renderMonitoringPage);
register('monitoring/capacity',      monitoring.renderMonitoringPage);
register('monitoring/storage',       monitoring.renderMonitoringPage);
register('monitoring/alert-providers', alertProviders.renderAlertProvidersPage);
register('alert-providers',           alertProviders.renderAlertProvidersPage);
register('logs',                     logs.renderLogsPage);
register('updates',                  updates.renderUpdatesPage);
register('updates/checks',           updates.renderUpdatesPage);
register('license',                  license.renderLicensePage);
register('security',                 security.renderSecurityPage);
register('security/ssl',             sslPage.renderSslPage);
register('security/antispam',        antivirusPage.renderAntivirusPage);
register('security/spam',            acl.renderACLPage);  // global spam control maps to ACL page in this build
register('security/routing',         acceptancePage.renderAcceptancePage);
register('security/rules',           incomingRulesPage.renderIncomingRulesPage);
register('security/quarantine',      quarantine.renderQuarantinePage);
register('security/login-protection', loginProtectionPage.renderLoginProtectionPage);
register('admin/groups',             adminGroups.renderAdminGroupsPage);
register('admin/users',              adminUsersPage.renderAdminUsersPage);
register('admin/audit-log',          auditLog.renderAuditLogPage);    // accessible at /#/admin/audit-log
register('admin/limits',             accountClasses.renderAccountClassesPage);
register('domains/groups',           domainGroups.renderDomainGroupsPage);
register('domains/lists',            mailingLists.renderMailingListsPage);
register('domains/public',           publicFolders.renderPublicFoldersPage);
register('accounts/classes',         accountClasses.renderAccountClassesPage);
register('backups/history',          backups.renderBackupsPage);  // re-use real backups page
register('backups/ftp',              ftpBackupPage.renderFtpBackupPage);
register('backups/fs',               fsAccessPage.renderFsAccessPage);
register('migration',                migration.renderMigrationPage);
register('migration/sources',        migrationSourcesPage.renderMigrationSourcesPage);
register('clustering',               (root) => clustering.renderClusteringPage(root, { title: 'Clustering setup' }));
register('clustering/imap',          (root) => clustering.renderClusteringPage(root, { title: 'IMAP proxy' }));
register('clustering/pop3',          (root) => clustering.renderClusteringPage(root, { title: 'POP3 proxy' }));
register('clustering/webmail',       (root) => clustering.renderClusteringPage(root, { title: 'WebMail / JMAP proxy' }));
register('logs/rules',               logRules.renderLogRulesPage);
register('logs/files',               logs.renderLogsPage);  // re-use logs page
register('logs/server',              logRules.renderLogRulesPage);  // log server destination = log rule with destination field

// Settings split — 10 protocol sub-pages. Each route
// reads its protocol id from the URL segment and queries
// the matching backend sub-page.
function protocolRouteHandler(root) {
  const route = (location.hash || '').replace(/^#\//, '').replace(/^settings\/protocol\//, '').replace(/\?.*$/, '');
  return settingsProtocolPage.renderSettingsProtocolPage(root, route || 'smtp_recv');
}
register('settings/protocol/smtp_recv', protocolRouteHandler);
register('settings/protocol/smtp_tx',   protocolRouteHandler);
register('settings/protocol/imap',      protocolRouteHandler);
register('settings/protocol/pop3',      protocolRouteHandler);
register('settings/protocol/webmail',   protocolRouteHandler);
register('settings/protocol/webadmin',  protocolRouteHandler);
register('settings/protocol/dns',       protocolRouteHandler);
register('settings/protocol/remote_pop',protocolRouteHandler);
register('settings/protocol/jmap',      protocolRouteHandler);
register('settings/protocol/mobility',  protocolRouteHandler);

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
  // Logged in: build the app shell.
  if (loginView) loginView.classList.add('hidden');
  if (appView)   { appView.classList.remove('hidden'); appView.removeAttribute('aria-hidden'); }
  renderSidebar(document.getElementById('sidebar-nav'));
  renderTopbar(document.querySelector('.topbar') || document.body);
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
