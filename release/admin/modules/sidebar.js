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

const STORAGE_KEY = 'orvix_sidebar_v1';

const GROUPS = [
  // group = { id, labelKey, items = [{ route, labelKey, planned?, badge? }] }
  { id: 'overview',  labelKey: null, items: [
    { route: 'dashboard', labelKey: 'sidebar.dashboard' },
  ]},

  { id: 'globalSettings', labelKey: 'sidebar.group.globalSettings', items: [
    { route: 'settings/general',  labelKey: 'sidebar.item.generalSettings'  },
    { route: 'settings/security', labelKey: 'sidebar.item.securityDefaults' },
    { route: 'license',           labelKey: 'sidebar.item.license'          },
    { route: 'settings/build',    labelKey: 'sidebar.item.buildInfo'        },
  ]},

  // Settings split — 10 protocol sub-pages. The
  // backend exposes one endpoint per protocol under
  // /api/v1/admin/settings/protocol/:protocol.
  { id: 'protocolSettings', labelKey: 'sidebar.group.protocolSettings', items: [
    { route: 'settings/protocol/smtp_recv', labelKey: 'sidebar.item.smtpRecv'   },
    { route: 'settings/protocol/smtp_tx',   labelKey: 'sidebar.item.smtpTx'     },
    { route: 'settings/protocol/imap',      labelKey: 'sidebar.item.imap'       },
    { route: 'settings/protocol/pop3',      labelKey: 'sidebar.item.pop3'       },
    { route: 'settings/protocol/webmail',   labelKey: 'sidebar.item.webmailS'   },
    { route: 'settings/protocol/webadmin',  labelKey: 'sidebar.item.webadminS'  },
    { route: 'settings/protocol/dns',       labelKey: 'sidebar.item.dnsProto'   },
    { route: 'settings/protocol/remote_pop',labelKey: 'sidebar.item.remotePop'  },
    { route: 'settings/protocol/jmap',      labelKey: 'sidebar.item.jmap'       },
    { route: 'settings/protocol/mobility',  labelKey: 'sidebar.item.mobility'   },
  ]},

  { id: 'services', labelKey: 'sidebar.group.services', items: [
    { route: 'services',          labelKey: 'sidebar.item.services'         },
    { route: 'runtime-listeners', labelKey: 'sidebar.item.runtimeListeners' },
  ]},

  { id: 'domainsAccounts', labelKey: 'sidebar.group.domainsAccounts', items: [
    { route: 'domains',       labelKey: 'sidebar.item.domains'        },
    { route: 'accounts',      labelKey: 'sidebar.item.accounts'       },
    { route: 'domains/groups', labelKey: 'sidebar.item.groups',        hide: true },
    { route: 'domains/lists',  labelKey: 'sidebar.item.mailingLists',  hide: true },
    { route: 'domains/public', labelKey: 'sidebar.item.publicFolders', hide: true },
    { route: 'accounts/classes', labelKey: 'sidebar.item.accountClasses', hide: true },
    { route: 'bulk-import',   labelKey: 'sidebar.item.bulkImport'     },
    { route: 'dns',           labelKey: 'sidebar.item.dnsDkim'        },
  ]},

  { id: 'security', labelKey: 'sidebar.group.security', items: [
    { route: 'security/ssl',      labelKey: 'sidebar.item.sslCerts'   },
    { route: 'security/antispam', labelKey: 'sidebar.item.antivirus',     hide: true },
    { route: 'security/spam',     labelKey: 'sidebar.item.spamControl',   hide: true },
    { route: 'security/routing',  labelKey: 'sidebar.item.routing',       hide: true },
    { route: 'security/rules',    labelKey: 'sidebar.item.incomingRules', hide: true },
    { route: 'security/quarantine', labelKey: 'sidebar.item.quarantine',  hide: true },
  ]},

  { id: 'updates', labelKey: 'sidebar.group.updates', items: [
    { route: 'updates', labelKey: 'sidebar.item.updateStatus' },
    { route: 'updates/checks', labelKey: 'sidebar.item.upgradeChecks' },
  ]},

  { id: 'queue', labelKey: 'sidebar.group.queue', items: [
    { route: 'queue',          labelKey: 'sidebar.item.queueProcessing' },
    { route: 'queue/messages', labelKey: 'sidebar.item.queueView' },
  ]},

  { id: 'status', labelKey: 'sidebar.group.status', items: [
    { route: 'monitoring',          labelKey: 'sidebar.item.reporting' },
    { route: 'monitoring/capacity', labelKey: 'sidebar.item.charts'    },
    { route: 'monitoring/storage',  labelKey: 'sidebar.item.storageCharts' },
    { route: 'monitoring/alert-providers', labelKey: 'sidebar.item.alertProviders' },
  ]},

  { id: 'logging', labelKey: 'sidebar.group.logging', items: [
    { route: 'logs',           labelKey: 'sidebar.item.localLogs' },
    { route: 'logs/rules',     labelKey: 'sidebar.item.logRules',   hide: true },
    { route: 'logs/files',     labelKey: 'sidebar.item.viewLogFiles', hide: true },
    { route: 'logs/server',    labelKey: 'sidebar.item.logServer',  hide: true },
  ]},

  { id: 'backup', labelKey: 'sidebar.group.backup', items: [
    { route: 'backups',           labelKey: 'sidebar.item.backupStatus' },
    { route: 'backups/history',   labelKey: 'sidebar.item.backupHistory' },
    { route: 'backups/ftp',       labelKey: 'sidebar.item.ftpBackup', hide: true },
    { route: 'backups/fs',        labelKey: 'sidebar.item.fsAccess',  hide: true },
  ]},

  { id: 'migration', labelKey: 'sidebar.group.migration', items: [
    { route: 'migration',         labelKey: 'sidebar.item.migrationJobs',  hide: true },
    { route: 'migration/sources', labelKey: 'sidebar.item.sourceServers', hide: true },
  ]},

  { id: 'clustering', labelKey: 'sidebar.group.clustering', items: [
    { route: 'clustering',         labelKey: 'sidebar.item.clusterSetup', hide: true },
    { route: 'clustering/imap',    labelKey: 'sidebar.item.imapProxy',    hide: true },
    { route: 'clustering/pop3',    labelKey: 'sidebar.item.pop3Proxy',    hide: true },
    { route: 'clustering/webmail', labelKey: 'sidebar.item.webmailProxy', hide: true },
  ]},

  { id: 'admin', labelKey: 'sidebar.group.adminRights', items: [
    { route: 'admin/groups', labelKey: 'sidebar.item.adminGroups' },
    { route: 'admin/users',  labelKey: 'sidebar.item.auditLog', hide: true },
  ]},
];

function loadCollapsed() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}'); } catch (_) { return {}; }
}
function saveCollapsed(state) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(state)); } catch (_) {}
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
      head.appendChild(el('span', { class: 'sidebar-section-label', text: t(group.labelKey) }));
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
      }, t(item.labelKey));
      if (item.planned) {
        li.classList.add('disabled');
        a.addEventListener('click', (e) => e.preventDefault());
      }
      li.appendChild(a);
      if (item.planned) {
        li.appendChild(el('span', { class: 'badge tag', title: t('common.planned'), text: t('common.planned') }));
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
