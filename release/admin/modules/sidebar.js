/* =====================================================================
   modules/sidebar.js — Two-console navigation (Customer / Internal).

   The sidebar renders a Zoho-style navigation surface for tenant customer
   admins. Orvix internal staff (superadmin) sees the Internal Ops
   surface; the route guards in router.js refuse internal routes for any
   non-internal role, so the sidebar simply hides them.

   Both experiences use the same collapsible-section machinery; only the
   group / item definitions change.
   ===================================================================== */

import { el } from './components.js';
import { t } from './i18n.js';
import { i } from './icons.js';

const STORAGE_KEY = 'orvix_sidebar_v1';
const MODE_KEY    = 'orvix_console_mode_v1';

const CUSTOMER_GROUPS = [
  { id: 'overview', labelKey: null, items: [
    { route: 'customer/dashboard', labelKey: 'sidebar.dashboard', icon: 'dashboard' },
  ]},

  { id: 'domainsAccounts', labelKey: 'sidebar.group.domainsAccounts', icon: 'domain', items: [
    { route: 'customer/domains', labelKey: 'sidebar.item.domains',  icon: 'domain'  },
    { route: 'customer/users',   labelKey: 'sidebar.item.accounts', icon: 'mailbox' },
    { route: 'customer/groups',  labelKey: 'sidebar.item.groups',  icon: 'group'   },
  ]},

  { id: 'security', labelKey: 'sidebar.group.security', icon: 'shield', items: [
    { route: 'customer/security', labelKey: 'sidebar.item.security', icon: 'shield' },
  ]},

  { id: 'mailFlow', labelKey: 'sidebar.group.mailflow', icon: 'mail', items: [
    { route: 'customer/mail-flow', labelKey: 'sidebar.item.mailflow', icon: 'mail' },
  ]},

  { id: 'reports', labelKey: 'sidebar.group.reports', icon: 'monitoring', items: [
    { route: 'customer/reports', labelKey: 'sidebar.item.reports', icon: 'monitoring' },
  ]},

  { id: 'settings', labelKey: 'sidebar.group.settings', icon: 'settings', items: [
    { route: 'customer/settings', labelKey: 'sidebar.item.settings', icon: 'settings' },
  ]},
];

const INTERNAL_GROUPS = [
  { id: 'overview', labelKey: null, items: [
    { route: 'internal/overview', labelKey: 'sidebar.dashboard', icon: 'dashboard' },
  ]},

  { id: 'tenants', labelKey: 'sidebar.group.tenants', icon: 'domain', items: [
    { route: 'internal/tenants', labelKey: 'sidebar.item.tenants', icon: 'domain' },
    { route: 'internal/domain-intelligence', labelKey: 'sidebar.item.domainIntel', icon: 'monitoring' },
  ]},

  { id: 'ops', labelKey: 'sidebar.group.ops', icon: 'monitoring', items: [
    { route: 'internal/mail-flow-ops', labelKey: 'sidebar.item.mailflowOps', icon: 'queue' },
    { route: 'internal/security-ops',  labelKey: 'sidebar.item.securityOps', icon: 'shield' },
  ]},

  { id: 'branding', labelKey: 'sidebar.group.branding', icon: 'paint', items: [
    { route: 'internal/branding', labelKey: 'sidebar.item.branding', icon: 'paint' },
  ]},

  { id: 'legacyAdmin', labelKey: 'sidebar.group.admin', icon: 'admin', items: [
    { route: 'settings/general', labelKey: 'sidebar.item.generalSettings', icon: 'settings' },
    { route: 'admin/users',      labelKey: 'sidebar.item.adminUsers',     icon: 'user'   },
    { route: 'admin/groups',     labelKey: 'sidebar.item.adminGroups',    icon: 'group'  },
    { route: 'admin/audit-log',  labelKey: 'sidebar.item.auditLog',       icon: 'log'    },
  ]},
];

const GROUPS_BY_MODE = {
  customer: CUSTOMER_GROUPS,
  internal: INTERNAL_GROUPS,
};

let currentGroups = CUSTOMER_GROUPS;

function loadCollapsed() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}'); } catch (_) { return {}; }
}
function saveCollapsed(state) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(state)); } catch (_) {}
}
function loadMode() {
  try { return localStorage.getItem(MODE_KEY) || 'customer'; } catch (_) { return 'customer'; }
}
function saveMode(m) {
  try { localStorage.setItem(MODE_KEY, m); } catch (_) {}
}

function iconNode(name) {
  const tmp = document.createElement('span');
  tmp.className = 'nav-icon';
  tmp.innerHTML = i(name || 'dashboard', { size: 15 });
  return tmp;
}

function buildGroupHead(group) {
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
  caret.textContent = '\u25be';
  head.appendChild(caret);
  head.addEventListener('click', () => toggleGroup(group.id));
  head.addEventListener('keydown', (ev) => {
    if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); toggleGroup(group.id); }
  });
  return head;
}

function buildItem(item) {
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
  a.appendChild(el('span', { text: t(item.labelKey) }));
  if (item.planned) {
    li.classList.add('disabled');
    a.addEventListener('click', (e) => e.preventDefault());
  }
  li.appendChild(a);
  if (item.planned) {
    li.appendChild(el('span', { class: 'badge tag no-dot', title: t('common.planned'), text: t('common.planned') }));
  }
  return li;
}

function buildModeSwitcher(host) {
  const switcher = el('div', { class: 'sidebar-mode' });
  const customerBtn = el('button', {
    class: 'sidebar-mode-btn', type: 'button',
    'data-mode': 'customer',
    onclick: () => setConsoleMode('customer'),
  }, t('sidebar.mode.customer'));
  const internalBtn = el('button', {
    class: 'sidebar-mode-btn', type: 'button',
    'data-mode': 'internal',
    onclick: () => setConsoleMode('internal'),
  }, t('sidebar.mode.internal'));
  switcher.appendChild(customerBtn);
  switcher.appendChild(internalBtn);
  host.appendChild(switcher);
  const update = () => {
    switcher.querySelectorAll('.sidebar-mode-btn').forEach((b) => {
      const m = b.getAttribute('data-mode');
      b.classList.toggle('active', m === currentMode());
    });
  };
  return update;
}

let _modeSwitcher = null;

function currentMode() {
  return currentGroups === INTERNAL_GROUPS ? 'internal' : 'customer';
}

export function setConsoleMode(mode) {
  if (mode !== 'customer' && mode !== 'internal') return;
  saveMode(mode);
  const root = document.getElementById('sidebar-nav');
  if (!root) return;
  renderSidebar(root, { mode });
}

export function isInternalMode() {
  return currentMode() === 'internal';
}

export function getGroupsForMode(mode) {
  return GROUPS_BY_MODE[mode] || CUSTOMER_GROUPS;
}

export function renderSidebar(root, opts) {
  if (!root) return;
  const mode = (opts && opts.mode) || loadMode();
  currentGroups = (mode === 'internal' && opts && opts.allowInternal !== false)
    ? INTERNAL_GROUPS
    : CUSTOMER_GROUPS;
  root.innerHTML = '';
  if (opts && opts.allowInternal) {
    _modeSwitcher = buildModeSwitcher(root);
  }
  const collapsed = loadCollapsed();
  currentGroups.forEach((group, gIdx) => {
    if (group.labelKey) {
      root.appendChild(buildGroupHead(group));
    }
    const list = el('ul', {
      class: 'sidebar-list',
      'data-group': group.id,
      style: collapsed[group.id] ? 'display:none;' : '',
    });
    group.items.forEach((item) => list.appendChild(buildItem(item)));
    if (gIdx === 0) list.classList.add('sidebar-list-overview');
    root.appendChild(list);
  });
  if (_modeSwitcher) _modeSwitcher();

  document.addEventListener('orvix:route', (ev) => {
    const route = ev.detail && ev.detail.route;
    if (!route) return;
    root.querySelectorAll('a.sidebar-link').forEach((a) => {
      const r = a.dataset.route;
      const li = a.closest('li');
      if (!li) return;
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
  if (head) {
    const caret = head.querySelector('.sidebar-section-caret');
    if (caret) caret.textContent = collapsed[id] ? '\u25b8' : '\u25be';
  }
  const list = document.querySelector(`[data-group="${id}"]`);
  if (list) list.style.display = collapsed[id] ? 'none' : '';
}

export function firstActiveRoute() {
  for (const g of currentGroups) {
    for (const it of g.items) if (!it.planned && !it.hide) return it.route;
  }
  return 'customer/dashboard';
}
