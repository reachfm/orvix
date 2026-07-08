/* =====================================================================
   modules/sidebar.js — Grouped navigation with collapsible sections.

   The sidebar is rendered into #sidebar-nav in index.html. Sections
   can be collapsed (state is persisted to localStorage). The active
   state is set by the router via orvix:route events.

   Each group has a header (collapsible), a list of items, and an
   optional planned badge. Items route through hash navigation
   (#/route). A planned item renders a dimmed badge and does not
   navigate.
   ===================================================================== */

import { el } from './components.js';
import { t } from './i18n.js';
import { i } from './icons.js';

const STORAGE_KEY = 'orvix_sidebar_v1';

const GROUPS = [
  // group = { id, labelKey, icon, items = [{ route, labelKey, icon, planned?, badge? }] }
  { id: 'overview',  labelKey: null, items: [
    { route: 'dashboard', labelKey: 'sidebar.dashboard', icon: 'dashboard' },
  ]},

  { id: 'globalSettings', labelKey: 'sidebar.group.globalSettings', icon: 'settings', items: [
    { route: 'settings/general',  labelKey: 'sidebar.item.generalSettings',  icon: 'settings' },
    { route: 'settings/security', labelKey: 'sidebar.item.securityDefaults', icon: 'shield'   },
    { route: 'license',           labelKey: 'sidebar.item.license',          icon: 'license'  },
    { route: 'settings/build',    labelKey: 'sidebar.item.buildInfo',        icon: 'cpu'      },
  ]},

  // Settings split — 10 protocol sub-pages. The
  // backend exposes one endpoint per protocol under
  // /api/v1/admin/settings/protocol/:protocol.
  { id: 'protocolSettings', labelKey: 'sidebar.group.protocolSettings', icon: 'globe', items: [
    { route: 'settings/protocol/smtp_recv', labelKey: 'sidebar.item.smtpRecv',   icon: 'smtp' },
    { route: 'settings/protocol/smtp_tx',   labelKey: 'sidebar.item.smtpTx',     icon: 'smtp' },
    { route: 'settings/protocol/imap',      labelKey: 'sidebar.item.imap',       icon: 'imap' },
    { route: 'settings/protocol/pop3',      labelKey: 'sidebar.item.pop3',       icon: 'pop3' },
    { route: 'settings/protocol/webmail',   labelKey: 'sidebar.item.webmailS',   icon: 'mail' },
    { route: 'settings/protocol/webadmin',  labelKey: 'sidebar.item.webadminS',  icon: 'admin' },
    { route: 'settings/protocol/dns',       labelKey: 'sidebar.item.dnsProto',   icon: 'dns' },
    { route: 'settings/protocol/remote_pop',labelKey: 'sidebar.item.remotePop',  icon: 'pop3' },
    { route: 'settings/protocol/jmap',      labelKey: 'sidebar.item.jmap',       icon: 'mail' },
    { route: 'settings/protocol/mobility',  labelKey: 'sidebar.item.mobility',   icon: 'globe' },
  ]},

  { id: 'services', labelKey: 'sidebar.group.services', icon: 'cpu', items: [
    { route: 'services',          labelKey: 'sidebar.item.services',         icon: 'services' },
    { route: 'runtime-listeners', labelKey: 'sidebar.item.runtimeListeners', icon: 'monitoring' },
  ]},

  { id: 'domainsAccounts', labelKey: 'sidebar.group.domainsAccounts', icon: 'domain', items: [
    { route: 'domains',       labelKey: 'sidebar.item.domains',        icon: 'domain'  },
    { route: 'accounts',      labelKey: 'sidebar.item.accounts',       icon: 'mailbox' },
    { route: 'domains/groups', labelKey: 'sidebar.item.groups',        icon: 'group'   },
    { route: 'domains/lists',  labelKey: 'sidebar.item.mailingLists',  icon: 'list'    },
    { route: 'domains/public', labelKey: 'sidebar.item.publicFolders', icon: 'folder'  },
    { route: 'accounts/classes', labelKey: 'sidebar.item.accountClasses', icon: 'user' },
    { route: 'bulk-import',   labelKey: 'sidebar.item.bulkImport',     icon: 'upload'  },
    { route: 'dns',           labelKey: 'sidebar.item.dnsDkim',        icon: 'dns'     },
  ]},

  { id: 'security', labelKey: 'sidebar.group.security', icon: 'shield', items: [
    { route: 'security/ssl',      labelKey: 'sidebar.item.sslCerts',       icon: 'certificate' },
    { route: 'security/antispam', labelKey: 'sidebar.item.antivirus',      icon: 'shield' },
    { route: 'security/spam',     labelKey: 'sidebar.item.spamControl',    icon: 'spam'   },
    { route: 'security/routing',  labelKey: 'sidebar.item.routing',        icon: 'routing'},
    { route: 'security/rules',    labelKey: 'sidebar.item.incomingRules',  icon: 'rules'  },
    { route: 'security/quarantine', labelKey: 'sidebar.item.quarantine',   icon: 'lock'   },
    { route: 'security/login-protection', labelKey: 'sidebar.item.loginProtection', icon: 'key' },
  ]},

  { id: 'updates', labelKey: 'sidebar.group.updates', icon: 'update', items: [
    { route: 'updates', labelKey: 'sidebar.item.updateStatus',  icon: 'update' },
    { route: 'updates/checks', labelKey: 'sidebar.item.upgradeChecks', icon: 'refresh' },
  ]},

  { id: 'queue', labelKey: 'sidebar.group.queue', icon: 'queue', items: [
    { route: 'queue',          labelKey: 'sidebar.item.queueProcessing', icon: 'queue' },
    { route: 'queue/messages', labelKey: 'sidebar.item.queueView',       icon: 'mail'  },
  ]},

  { id: 'status', labelKey: 'sidebar.group.status', icon: 'monitoring', items: [
    { route: 'monitoring',          labelKey: 'sidebar.item.reporting',      icon: 'monitoring' },
    { route: 'monitoring/capacity', labelKey: 'sidebar.item.charts',        icon: 'charts'     },
    { route: 'monitoring/storage',  labelKey: 'sidebar.item.storageCharts', icon: 'storage'    },
    { route: 'monitoring/alert-providers', labelKey: 'sidebar.item.alertProviders', icon: 'alert' },
    { route: 'observability',       labelKey: 'sidebar.item.observability', icon: 'monitoring' },
    { route: 'alerts',              labelKey: 'sidebar.item.alerts',        icon: 'alert'      },
    { route: 'storage-topology',    labelKey: 'sidebar.item.storageTopology', icon: 'storage' },
  ]},

  { id: 'tenancy', labelKey: 'sidebar.group.tenancy', icon: 'domain', items: [
    { route: 'tenants',  labelKey: 'sidebar.item.tenants',  icon: 'domain' },
    { route: 'branding', labelKey: 'sidebar.item.branding', icon: 'paint'  },
  ]},

  { id: 'logging', labelKey: 'sidebar.group.logging', icon: 'log', items: [
    { route: 'logs',           labelKey: 'sidebar.item.localLogs',   icon: 'log'   },
    { route: 'logs/rules',     labelKey: 'sidebar.item.logRules',   icon: 'rules' },
    { route: 'logs/files',     labelKey: 'sidebar.item.viewLogFiles', icon: 'folder' },
    { route: 'logs/server',    labelKey: 'sidebar.item.logServer',  icon: 'server' },
  ]},

  { id: 'backup', labelKey: 'sidebar.group.backup', icon: 'backup', items: [
    { route: 'backups',           labelKey: 'sidebar.item.backupStatus',  icon: 'backup' },
    { route: 'backups/history',   labelKey: 'sidebar.item.backupHistory', icon: 'log'    },
    { route: 'backups/ftp',       labelKey: 'sidebar.item.ftpBackup',     icon: 'globe'  },
    { route: 'backups/fs',        labelKey: 'sidebar.item.fsAccess',      icon: 'folder' },
  ]},

  { id: 'migration', labelKey: 'sidebar.group.migration', icon: 'migration', items: [
    { route: 'migration',         labelKey: 'sidebar.item.migrationJobs',  hide: true, icon: 'migration' },
    { route: 'migration/sources', labelKey: 'sidebar.item.sourceServers', hide: true, icon: 'server'    },
  ]},

  { id: 'clustering', labelKey: 'sidebar.group.clustering', icon: 'cluster', items: [
    { route: 'clustering',         labelKey: 'sidebar.item.clusterSetup',   hide: true, icon: 'cluster' },
    { route: 'clustering/imap',    labelKey: 'sidebar.item.imapProxy',      hide: true, icon: 'imap'    },
    { route: 'clustering/pop3',    labelKey: 'sidebar.item.pop3Proxy',      hide: true, icon: 'pop3'    },
    { route: 'clustering/webmail', labelKey: 'sidebar.item.webmailProxy',   hide: true, icon: 'mail'    },
  ]},

  { id: 'admin', labelKey: 'sidebar.group.adminRights', icon: 'admin', items: [
    { route: 'admin/groups', labelKey: 'sidebar.item.adminGroups', icon: 'group' },
    { route: 'admin/users',  labelKey: 'sidebar.item.adminUsers',  icon: 'user'  },
  ]},
];

function loadCollapsed() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}'); } catch (_) { return {}; }
}
function saveCollapsed(state) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(state)); } catch (_) {}
}

// Render an inline SVG icon into a temporary container so we can adopt
// the DOM node into the rest of the sidebar tree.
function iconNode(name) {
  const tmp = document.createElement('span');
  tmp.className = 'nav-icon';
  tmp.innerHTML = i(name || 'dashboard', { size: 15 });
  // unwrap to keep the markup clean (the <span> wrapper is the icon container)
  return tmp;
}

export function renderSidebar(root) {
  if (!root) return;
  root.innerHTML = '';
  const collapsed = loadCollapsed();
  GROUPS.forEach((group, gIdx) => {
    const visibleItems = group.items.filter((it) => !it.hide);
    if (!visibleItems.length) return;
    if (group.labelKey) {
      const head = el('div', {
        class: 'sidebar-section-head',
        role: 'button',
        tabindex: '0',
        'data-toggle': group.id,
      });
      const label = el('span', { class: 'sidebar-section-label' });
      if (group.icon) {
        const iconWrap = el('span', { class: 'nav-icon' });
        iconWrap.innerHTML = i(group.icon, { size: 12 });
        label.appendChild(iconWrap);
      }
      label.appendChild(document.createTextNode(t(group.labelKey)));
      head.appendChild(label);
      const caret = el('span', { class: 'sidebar-section-caret' });
      caret.textContent = collapsed[group.id] ? '\u25b8' : '\u25be';
      head.appendChild(caret);
      head.addEventListener('click', () => toggleGroup(group.id));
      head.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); toggleGroup(group.id); }
      });
      root.appendChild(head);
    }

    const list = el('ul', {
      class: 'sidebar-list',
      'data-group': group.id,
      style: collapsed[group.id] ? 'display:none;' : '',
    });
    visibleItems.forEach((item) => {
      const li = el('li', { class: 'sidebar-item' + (item.planned ? ' planned' : '') });
      const a = el('a', {
        href: '#/' + item.route,
        class: 'sidebar-link',
        'data-route': item.route,
        tabindex: item.planned ? '-1' : '0',
      });
      if (item.icon) {
        const iconWrap = el('span', { class: 'nav-icon' });
        iconWrap.innerHTML = i(item.icon, { size: 14 });
        a.appendChild(iconWrap);
      }
      const span = el('span', { text: t(item.labelKey) });
      a.appendChild(span);
      if (item.planned) {
        li.classList.add('disabled');
        a.addEventListener('click', (e) => e.preventDefault());
      }
      li.appendChild(a);
      if (item.planned) {
        li.appendChild(el('span', { class: 'badge tag no-dot', title: t('common.planned'), text: t('common.planned') }));
      }
      list.appendChild(li);
    });
    if (gIdx === 0) list.classList.add('sidebar-list-overview');
    root.appendChild(list);
  });

  document.addEventListener('orvix:route', (ev) => {
    const route = ev.detail && ev.detail.route;
    if (!route) return;
    root.querySelectorAll('a.sidebar-link').forEach((a) => {
      const r = a.dataset.route;
      const li = a.closest('li');
      if (!li) return;
      // Allow prefix match so /settings/general highlights the settings item too.
      const isActive = route === r || (r && r.indexOf('/') >= 0 && route.indexOf(r) === 0);
      if (isActive) {
        li.classList.add('active');
        a.setAttribute('aria-current', 'page');
      } else {
        li.classList.remove('active');
        a.removeAttribute('aria-current');
      }
    });
  });
}

function toggleGroup(id) {
  const collapsed = loadCollapsed();
  collapsed[id] = !collapsed[id];
  saveCollapsed(collapsed);
  const head = document.querySelector(`[data-toggle="${id}"]`);
  if (head) head.querySelector('.sidebar-section-caret').textContent = collapsed[id] ? '\u25b8' : '\u25be';
  const list = document.querySelector(`[data-group="${id}"]`);
  if (list) list.style.display = collapsed[id] ? 'none' : '';
}

/**
 * routeToFirst returns the first non-planned route in the sidebar.
 * Used by the dashboard "missing route" fallback.
 */
export function firstActiveRoute() {
  for (const g of GROUPS) {
    for (const it of g.items) if (!it.planned && !it.hide) return it.route;
  }
  return 'dashboard';
}
