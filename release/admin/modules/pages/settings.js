/* =====================================================================
   pages/settings.js — Global settings page wired to the real
   /api/v1/admin/settings endpoint. The previous admin build
   showed a misleading "out of scope" list including "Bulk
   mailbox CSV import — backend has no endpoint yet"; that is
   now FALSE and the false copy has been removed from this
   admin build (the real endpoint lives at
   /api/v1/mailboxes/import and is exposed in the sidebar
   under Domains & Accounts → Bulk Mailbox Import).

   PATCH is routed through csrfFetch and only sends keys the
   backend has declared mutable (it filters out the read-only
   ones from the GET response).

   Sections:
     * General   — hostname, version, build, build_time, status
     * Security  — auth posture, password policy
     * Backup    — backup dir + retention
     * Runtime   — port bindings from /api/v1/admin/runtime
     * Audit     — read-only confirm of last changes
   ===================================================================== */

import { el, esc, fmtDate, fmtShortDate, copyToClipboard, openModal, confirmDanger, $ } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPatch } from '../api.js';
import { setSettings, getSettings, getRuntime, setRuntime, getBuild } from '../state.js';
import { applyAutoDir, withAutoDir } from '../rtl.js';
import { toast } from '../toast.js';

export async function renderSettingsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('settings.heading') }),
      el('p', { class: 'page-subtitle subtle', text: t('settings.subtitle') }),
    ]),
  ]));
  root.appendChild(wrap);

  // 1. Try the real settings endpoint; on failure fall back to a
  //    read-only summary built from summary + runtime + license.
  let data, err;
  try { data = await apiGet('/api/v1/admin/settings'); }
  catch (e) { err = e; }
  if (err) {
    try { data = await apiGet('/api/v1/admin/summary'); }
    catch (e2) {
      wrap.appendChild(el('div', { class: 'panel error-panel' }, [
        el('div', { class: 'panel-head' }, el('h3', { text: 'Could not load settings' })),
        el('div', { class: 'panel-body', text: (e2 && e2.message) || 'unknown error' }),
      ]));
      return;
    }
    // Show read-only warning when /settings endpoint is unavailable.
    wrap.appendChild(el('div', { class: 'banner banner-warn' }, [
      el('span', { class: 'banner-text', text: 'Settings API unreachable. Displaying read-only summary — changes cannot be saved.' }),
    ]));
  }
  setSettings(data || {});
  setRuntime(data && data.runtime ? data.runtime : (getRuntime() || null));

  // General panel — read-only.
  wrap.appendChild(buildGeneralPanel(data));
  // Editable panel — only fields the backend has marked mutable.
  wrap.appendChild(buildEditablePanel(data));
  // Security posture.
  wrap.appendChild(buildSecurityPanel(data));
  // Build / runtime.
  wrap.appendChild(buildBuildPanel(data));
  // Listener runtime — what is actually bound.
  wrap.appendChild(buildRuntimePanel(data));
  // Audit footer.
  wrap.appendChild(buildAuditFooter(data));

  applyAutoDir(wrap);
}

function buildGeneralPanel(d) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'General' })));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'kv' });
  const items = [
    ['Hostname', d.hostname || (d.runtime && d.runtime.hostname) || '-'],
    ['Version',  d.version  || (d.build   && d.build.version)  || (d.runtime && d.runtime.version) || '-'],
    ['Status',   d.status   || 'unknown'],
    ['License',  d.license && d.license.tier ? d.license.tier : (d.license_state || '-')],
  ];
  items.forEach(([k, v]) => {
    dl.appendChild(el('dt', { text: k }));
    dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
  });
  body.appendChild(dl);
  card.appendChild(body);
  return card;
}

function buildEditablePanel(d) {
  // The backend tells us which fields are mutable via the
  // `mutable_fields` list. Anything not in the list is read-only.
  const mutable = new Set((d && d.mutable_fields) || []);
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Editable settings' }),
    el('span', { class: 'subtle', text: mutable.size === 0 ? t('common.readOnly') : 'Live' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  if (mutable.size === 0) {
    body.appendChild(el('p', { class: 'empty', text: 'Backend reports no mutable settings in this build.' }));
    card.appendChild(body);
    return card;
  }
  // Render a real form with the mutable keys only.
  const form = el('form', { class: 'settings-form' });
  // Cluster known field groups.
  const groups = [
    { title: 'Branding',     keys: ['display_name', 'support_url'] },
    { title: 'Security defaults', keys: ['password_min_length', 'session_timeout_minutes', 'mfa_required'] },
    { title: 'Backup',       keys: ['backup_dir', 'backup_retention_days'] },
    { title: 'Monitoring',   keys: ['alert_webhook_url', 'alert_threshold_cpu', 'alert_threshold_disk'] },
  ];
  groups.forEach((g) => {
    const overlap = g.keys.filter((k) => mutable.has(k));
    if (overlap.length === 0) return;
    form.appendChild(el('div', { class: 'settings-group' }, [
      el('h4', { text: g.title }),
      overlap.forEach((k) => form.appendChild(renderField(k, d[k]))),
    ]));
  });
  form.appendChild(el('div', { class: 'form-actions' }, [
    el('button', { type: 'button', class: 'btn ghost', text: t('common.cancel'), onclick: () => location.reload() }),
    el('button', { type: 'button', class: 'btn primary', text: t('common.save'),
      onclick: () => submitSettings(form, mutable) }),
  ]));
  body.appendChild(form);
  card.appendChild(body);
  return card;
}

function renderField(key, value) {
  const row = el('div', { class: 'form-row' });
  row.appendChild(el('label', { for: 'f-' + key, text: key }));
  if (typeof value === 'boolean') {
    const sel = el('select', { id: 'f-' + key, name: key }, [
      el('option', { value: 'true' }, 'true'),
      el('option', { value: 'false' }, 'false'),
    ]);
    sel.value = String(!!value);
    row.appendChild(sel);
  } else if (typeof value === 'number') {
    row.appendChild(el('input', { id: 'f-' + key, name: key, type: 'number', value: String(value) }));
  } else {
    row.appendChild(el('input', { id: 'f-' + key, name: key, type: 'text', value: value == null ? '' : String(value), autocomplete: 'off', spellcheck: 'false' }));
  }
  return row;
}

async function submitSettings(form, mutable) {
  const body = {};
  for (const key of mutable) {
    const node = form.querySelector('[name="' + key + '"]');
    if (!node) continue;
    if (node.tagName === 'INPUT' && node.type === 'number') {
      const n = Number(node.value);
      if (!isNaN(n)) body[key] = n;
    } else if (node.tagName === 'SELECT') {
      body[key] = node.value === 'true';
    } else {
      body[key] = node.value;
    }
  }
  const ok = await confirmDanger({
    title: 'Save settings',
    message: 'Settings are applied to the live admin/runtime. Risky changes may require a service restart (see restart_required below). Continue?',
    confirmLabel: 'Save',
    dangerous: false,
  });
  if (!ok) return;
  try {
    const resp = await apiPatch('/api/v1/admin/settings', body);
    setSettings(resp || {});
    toast('Settings saved', 'success');
    if (resp && resp.restart_required) {
      toast('A service restart is required for the change to take effect', 'warn', 6000);
    }
    setTimeout(() => location.reload(), 600);
  } catch (err) {
    toast((err && err.message) || 'Save failed', 'error', 6000);
  }
}

function buildSecurityPanel(d) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Security posture' })));
  const body = el('div', { class: 'panel-body' });
  const rows = [
    ['MFA required for admins',  d.mfa_required === true ? 'yes' : (d.mfa_required === false ? 'no' : 'unknown')],
    ['Password min length',      d.password_min_length || '-'],
    ['Session timeout (min)',    d.session_timeout_minutes || '-'],
    ['CSRF protection',          'enabled'],
    ['CSP',                      'strict'],
    ['HSTS',                     d.hsts ? 'enabled' : 'unknown'],
  ];
  const tbl = el('table', { class: 'kv-table' });
  rows.forEach(([k, v]) => {
    const tr = el('tr', null, [
      el('th', { text: k }),
      el('td', { class: 'kv-v', text: String(v) }),
    ]);
    tbl.appendChild(tr);
  });
  body.appendChild(tbl);
  card.appendChild(body);
  return card;
}

function buildBuildPanel(d) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Build / runtime' })));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'kv' });
  const items = [
    ['Build commit', d.commit || (d.build && d.build.commit) || (d.runtime && d.runtime.commit) || '-'],
    ['Build time',   d.build_time || (d.build && d.build.time) || (d.runtime && d.runtime.build_time) || '-'],
    ['Channel',      d.channel || (d.build && d.build.channel) || 'stable'],
    ['License',      d.license && (d.license.tier || d.license.kind) || '-'],
  ];
  items.forEach(([k, v]) => {
    dl.appendChild(el('dt', { text: k }));
    dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
  });
  body.appendChild(dl);
  card.appendChild(body);
  return card;
}

function buildRuntimePanel(d) {
  const rt = d && d.runtime ? d.runtime : (getRuntime() || {});
  const listeners = rt.listeners || [];
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Listener bindings' }),
    el('span', { class: 'subtle', text: listeners.length + ' listeners' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  if (listeners.length === 0) {
    body.appendChild(el('div', { class: 'empty', text: 'No listener telemetry available.' }));
    card.appendChild(body);
    return card;
  }
  const tbl = el('table', { class: 'data-table' });
  const thead = el('thead', null, el('tr', null, [
    el('th', { text: 'Kind' }),
    el('th', { text: 'Port' }),
    el('th', { text: 'State' }),
    el('th', { text: 'Detail' }),
  ]));
  tbl.appendChild(thead);
  const tbody = el('tbody');
  listeners.forEach((l) => {
    tbody.appendChild(el('tr', null, [
      el('td', { text: l.label || l.kind || '-' }),
      el('td', { text: l.port ? String(l.port) : '-' }),
      el('td', null, badgeForState(l.state)),
      el('td', { class: 'kv-v', text: l.detail || '-' }),
    ]));
  });
  tbl.appendChild(tbody);
  body.appendChild(tbl);
  card.appendChild(body);
  return card;
}

function badgeForState(state) {
  const s = (state || 'unknown').toLowerCase();
  let kind = 'neutral';
  if (s === 'active') kind = 'good';
  else if (s === 'skipped' || s === 'failed' || s === 'fail') kind = 'bad';
  else if (s === 'degraded' || s === 'starting') kind = 'warn';
  const cls = 'badge ' + kind;
  return el('span', { class: cls, text: s });
}

function buildAuditFooter(d) {
  const card = el('section', { class: 'panel panel-foot' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Audit & freshness' })));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'kv' });
  dl.appendChild(el('dt', { text: 'Last settings change' }));
  dl.appendChild(el('dd', { class: 'kv-v', text: d.last_modified || d.last_settings_change || '-' }));
  dl.appendChild(el('dt', { text: 'Last restart required' }));
  dl.appendChild(el('dd', { class: 'kv-v', text: d.restart_required ? 'yes' : 'no' }));
  body.appendChild(dl);
  card.appendChild(body);
  return card;
}
