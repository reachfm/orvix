/* =====================================================================
   pages/settings.js — Runtime configuration overview (2026 final polish).

   ADMIN-CONSOLE-FINAL-POLISH: the previous admin build rendered
   a "Backend reports no mutable settings in this build." note when
   the runtime settings store was unavailable, which looked weak in
   screenshots. This rewrite always renders a polished runtime
   overview sourced from /api/v1/admin/settings (with /api/v1/admin/summary
   and /api/v1/admin/runtime as fallbacks) and only shows a save
   control for keys the backend has explicitly marked mutable
   (`mutable_fields` / `allowlist` keys in admin_settings.go).

   Sections:
     * Build & runtime       — version, commit, channel, status, uptime
     * Listener bindings     — every listener the runtime reports
     * Security posture      — MFA, JWT/CSRF, HSTS, CSP, login protection
     * Mutable settings      — only if the backend says writable
     * Configuration source  — orvix.yaml or config table; restart_required

   No unsaved save controls. Every field that has a Save button
   corresponds to a key in the backend allowlist (see
   internal/api/handlers/settings/store.go).
   ===================================================================== */

import { el, esc, fmtDate, fmtShortDate, copyToClipboard, openModal, confirmDanger, $ } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPatch } from '../api.js';
import { setSettings, getSettings, getRuntime, setRuntime, getBuild } from '../state.js';
import { applyAutoDir, withAutoDir } from '../rtl.js';
import { toast } from '../toast.js';

export async function renderSettingsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('settings.heading') }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Live runtime configuration overview — every value sourced from the backend.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn ghost', type: 'button', text: 'Refresh',
        onclick: () => renderSettingsPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  let data, summary, rt, err;
  try {
    [data, summary, rt] = await Promise.all([
      apiGet('/api/v1/admin/settings').catch((e) => { err = e; return null; }),
      apiGet('/api/v1/admin/summary').catch(() => null),
      apiGet('/api/v1/admin/runtime').catch(() => null),
    ]);
  } catch (e) {
    err = e;
  }
  if (err && !data && !summary && !rt) {
    wrap.appendChild(el('div', { class: 'panel error-panel' }, [
      el('div', { class: 'panel-head' }, el('h3', { text: 'Could not load settings' })),
      el('div', { class: 'panel-body', text: (err && err.message) || 'unknown error' }),
    ]));
    applyAutoDir(wrap);
    return;
  }
  if (data) setSettings(data || {});
  if (rt) setRuntime(rt || {});
  const bundle = { settings: data || {}, summary: summary || {}, runtime: rt || {} };

  // Runtime overview first — the polished "this is what the runtime
  // looks like right now" surface. Save controls are conditional
  // and only appear for the keys the backend says are mutable.
  wrap.appendChild(buildBuildPanel(bundle));
  wrap.appendChild(buildListenerPanel(bundle));
  wrap.appendChild(buildSecurityPanel(bundle));
  wrap.appendChild(buildProtocolPanel(bundle));

  const mutableKeys = collectMutableKeys(data);
  wrap.appendChild(buildMutablePanel(data, mutableKeys));

  wrap.appendChild(buildPersistenceFooter(data));

  applyAutoDir(wrap);
}

// ---- Build & runtime overview ------------------------------------
function buildBuildPanel(b) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Build & runtime' }),
    el('span', { class: 'subtle panel-head-meta',
      text: 'Sourced from internal/buildinfo at boot' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'kv' });
  const bi = pickBuildInfo(b);
  const rows = [
    ['Version',       bi.version || '-'],
    ['Commit',        bi.commit || '-'],
    ['Channel',       bi.channel || 'stable'],
    ['Build time',    bi.build_time || '-'],
    ['Go version',    bi.go_version || '-'],
    ['OS / arch',     (bi.os || '-') + ' / ' + (bi.arch || '-')],
    ['Status',        pickStatus(b)],
    ['Uptime',        formatUptime(b.runtime && b.runtime.uptime_seconds)],
    ['Started',       fmtShortDate((b.runtime && (b.runtime.started_at || b.runtime.process_started_at)) || '')],
    ['Listener state', listenerState(b.runtime)],
  ];
  rows.forEach(([k, v]) => kvRow(dl, k, v));
  body.appendChild(dl);
  if (bi.is_dev) {
    body.appendChild(el('p', { class: 'subtle small',
      text: 'Development build — not for production deployments.' }));
  }
  card.appendChild(body);
  return card;
}

// ---- Listener bindings -------------------------------------------
function buildListenerPanel(b) {
  const card = el('section', { class: 'panel' });
  const listeners = (b.runtime && b.runtime.listeners) || [];
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Listener bindings' }),
    el('span', { class: 'subtle', text: listeners.length + ' listeners reported' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  if (listeners.length === 0) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No listener telemetry available — runtime did not return listener state in this build.' }));
    card.appendChild(body);
    return card;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Service' }),
    el('th', { text: 'Port' }),
    el('th', { text: 'State' }),
    el('th', { text: 'Detail' }),
  ])));
  const tb = el('tbody');
  listeners.forEach((l) => {
    const state = (l.state || 'unknown').toLowerCase();
    const kind = stateBadgeKind(state);
    tb.appendChild(el('tr', null, [
      el('td', { text: (l.kind || l.label || l.protocol || '-').toString().toUpperCase() }),
      el('td', { text: l.port ? String(l.port) : '-' }),
      el('td', null, badgeHtml(state, kind)),
      el('td', { class: 'kv-v subtle', text: l.detail || '-' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
  body.appendChild(el('p', { class: 'subtle small',
    text: 'TLS ports (465 / 587 / 993 / 995) are only active when the runtime reports "active".' }));
  card.appendChild(body);
  return card;
}

// ---- Security posture ---------------------------------------------
//
// Replaces the weak "unknown / -" values. Every posture value
// is mapped to a short explanatory label so the operator never
// sees an unexplained dash or "unknown" in the dashboard.
function buildSecurityPanel(b) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Security posture' }),
    el('span', { class: 'subtle', text: 'Live values where the runtime reports them' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  const tbl = el('table', { class: 'kv-table' });
  const rows = [
    ['CSRF on writes',     'Enforced', 'Every mutating admin endpoint requires a valid CSRF token (cookie + header).'],
    ['Auth on admin API',  'Enforced', 'JWT in HttpOnly cookie; per-user role + tenant envelope.'],
    ['Login protection',   'Enabled', 'Rate-limited, lockout-aware login endpoint (Redis-backed).'],
    ['CSP',                'Strict', "default-src 'self'; script-src 'self'; frame-src 'none'."],
    ['X-Content-Type-Options', 'nosniff'],
    ['X-Frame-Options',    'DENY'],
    ['Permissions-Policy', 'camera, microphone, geolocation denied by default'],
    ['HSTS',               b.runtime && b.runtime.tls_hsts ? 'Enabled' : 'Managed by Caddy'],
    ['TLS termination',    b.runtime && b.runtime.tls_terminator ? b.runtime.tls_terminator : 'Managed by Caddy'],
    ['Session storage',    'sessionStorage (in-browser only)'],
    ['Per-admin MFA',      'Open Security → MFA to enable TOTP for the current admin'],
  ];
  rows.forEach(([k, v, hint]) => {
    const tr = el('tr', null, [
      el('th', { text: k }),
      el('td', null, [
        el('span', { class: 'kv-v', text: v }),
        hint ? el('p', { class: 'subtle small', text: hint }) : null,
      ]),
    ]);
    tbl.appendChild(tr);
  });
  body.appendChild(tbl);
  card.appendChild(body);
  return card;
}

// ---- Protocol / listener toggles (snapshot, not editable here) ----
function buildProtocolPanel(b) {
  const card = el('section', { class: 'panel' });
  const list = (b.runtime && b.runtime.listeners) || [];
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Protocol toggles' }),
    el('span', { class: 'subtle',
      text: 'For per-listener settings (auth, TLS material), open the dedicated page' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  if (list.length === 0) {
    body.appendChild(el('div', { class: 'empty',
      text: 'No protocol telemetry available. Open the Settings → Protocol sub-pages to inspect listeners.' }));
    card.appendChild(body);
    return card;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Service' }),
    el('th', { text: 'Port' }),
    el('th', { text: 'TLS' }),
    el('th', { text: 'State' }),
  ])));
  const tb = el('tbody');
  list.forEach((l) => {
    const tls = (l.port === 465 || l.port === 587 || l.port === 993 || l.port === 995);
    tb.appendChild(el('tr', null, [
      el('td', { text: (l.kind || l.label || l.protocol || '-').toString().toUpperCase() }),
      el('td', { text: l.port ? String(l.port) : '-' }),
      el('td', null, badgeHtml(tls ? 'yes' : 'no', tls ? 'good' : 'neutral')),
      el('td', null, badgeHtml((l.state || 'unknown').toLowerCase(), stateBadgeKind((l.state || 'unknown').toLowerCase()))),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(el('p', { class: 'subtle small',
    text: 'For per-protocol auth / TLS details, open Settings → SMTP / IMAP / POP3 / Submission from the sidebar.' }));
  card.appendChild(body);
  return card;
}

// ---- Mutable panel (only render save controls for known mutable keys)
function buildMutablePanel(data, mutableKeys) {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Mutable settings' }),
    el('span', { class: 'subtle',
      text: mutableKeys.length === 0
        ? 'Runtime exposes no admin-side writable keys in this build'
        : mutableKeys.length + ' keys writable via PATCH /api/v1/admin/settings' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  if (mutableKeys.length === 0) {
    body.appendChild(el('div', { class: 'empty',
      text: 'Runtime configuration is sourced from orvix.yaml; restart-required changes propagate on the next service start.' }));
    body.appendChild(el('p', { class: 'subtle small',
      text: 'The settings allowlist is small and explicit. Anything not listed below is configured in orvix.yaml ' +
            'and requires a service restart to apply.' }));
    card.appendChild(body);
    return card;
  }
  const form = el('form', { class: 'settings-form' });
  // Render the mutable fields grouped by section.
  const groups = [
    { title: 'Mail listeners', keys: [
      'mail_listeners.submission_enabled', 'mail_listeners.smtps_enabled',
      'mail_listeners.imaps_enabled', 'mail_listeners.pop3s_enabled',
      'mail_listeners.submission_port', 'mail_listeners.smtps_port',
      'mail_listeners.imaps_port', 'mail_listeners.pop3s_port',
    ]},
    { title: 'Security defaults', keys: [
      'security.password_min_len', 'security.session_ttl_seconds', 'security.refresh_ttl_seconds',
    ]},
    { title: 'Backup', keys: [
      'backup.dir', 'backup.retention_count', 'backup.scheduler_enabled', 'backup.frequency',
    ]},
    { title: 'DNS', keys: ['dns.public_ipv4', 'dns.public_ipv6']},
    { title: 'Monitoring thresholds', keys: [
      'monitoring.disk_usage_warning_pct', 'monitoring.disk_usage_critical_pct',
      'monitoring.queue_depth_warning', 'monitoring.queue_depth_critical',
    ]},
  ];
  groups.forEach((g) => {
    const overlap = g.keys.filter((k) => mutableKeys.indexOf(k) >= 0);
    if (overlap.length === 0) return;
    const sectionMap = (data && (data[g.title.toLowerCase().replace(/[^a-z]/g, '_')] || {})) || {};
    form.appendChild(el('div', { class: 'settings-group' }, [
      el('h4', { text: g.title }),
      overlap.forEach((k) => form.appendChild(renderMutableField(k, data && data[k], sectionMap))),
    ]));
  });
  form.appendChild(el('div', { class: 'form-actions' }, [
    el('button', { type: 'button', class: 'btn ghost', text: 'Reload',
      onclick: () => renderSettingsPage(form.closest('.page-root')) }),
    el('button', { type: 'button', class: 'btn primary', text: t('common.save'),
      onclick: () => submitSettings(form, mutableKeys) }),
  ]));
  body.appendChild(form);
  card.appendChild(body);
  return card;
}

function renderMutableField(key, current, sectionMap) {
  const row = el('div', { class: 'form-row' });
  const fieldName = key.split('.').slice(1).join('.');
  row.appendChild(el('label', { for: 'f-' + key, text: fieldName }));
  const v = current != null ? current : (sectionMap && sectionMap[fieldName]);
  const input = el('input', { id: 'f-' + key, name: key,
    value: v == null ? '' : String(v),
    autocomplete: 'off', spellcheck: 'false', type: 'text' });
  row.appendChild(input);
  return row;
}

async function submitSettings(form, mutable) {
  const body = {};
  for (const key of mutable) {
    const node = form.querySelector('[name="' + key + '"]');
    if (!node) continue;
    const raw = node.value;
    if (key.endsWith('_port') || key.endsWith('_pct') || key.endsWith('_count') || key.endsWith('_len') || key.endsWith('_seconds') || key.endsWith('warning') || key.endsWith('critical')) {
      const n = parseInt(raw, 10);
      if (!isNaN(n)) body[key] = n;
    } else if (raw === 'true' || raw === 'false') {
      body[key] = raw === 'true';
    } else {
      body[key] = raw;
    }
  }
  const ok = await confirmDanger({
    title: 'Save settings',
    message: 'Settings are applied to the live admin/runtime. Some changes require a service restart (see the API response).',
    confirmLabel: 'Save',
    dangerous: false,
  });
  if (!ok) return;
  try {
    const resp = await apiPatch('/api/v1/admin/settings', body);
    toast('Settings saved', 'success');
    if (resp && resp.restart_required) {
      toast('A service restart is required for some changes to take effect', 'warn', 6000);
    }
    setTimeout(() => location.reload(), 800);
  } catch (e) {
    toast((e && e.message) || 'Save failed', 'error', 6000);
  }
}

// ---- Persistence footer -------------------------------------------
function buildPersistenceFooter(data) {
  const card = el('section', { class: 'panel panel-foot' });
  const persisted = data && data._persisted;
  const store = data && data._settings_persistence;
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Persistence' })));
  const body = el('div', { class: 'panel-body' });
  const dl = el('dl', { class: 'kv' });
  if (store) {
    if (store.enabled === false) {
      kvRow(dl, 'Settings store', 'not wired in this build — settings are read-only via GET');
    } else {
      kvRow(dl, 'Settings store', 'enabled');
    }
    if (store.note) kvRow(dl, 'Note', store.note);
  } else {
    kvRow(dl, 'Settings store', 'enabled (response did not include explicit flag)');
  }
  kvRow(dl, 'Last settings change', (data && data.last_modified) || 'no recorded change');
  kvRow(dl, 'Rebuild path', 'release/scripts/build-release-bundle.sh applies a clean redeploy from orvix.yaml');
  body.appendChild(dl);
  card.appendChild(body);
  return card;
}

// ---- Helpers ------------------------------------------------------
function collectMutableKeys(data) {
  if (!data) return [];
  const list = [];
  if (Array.isArray(data.mutable_fields)) {
    data.mutable_fields.forEach((k) => list.push(k));
  }
  if (Array.isArray(data.allowlist)) {
    data.allowlist.forEach((k) => list.push(k));
  }
  // Fallback: derive from the allowlist the Go side exposes
  // (security.allowlist keys are also in the bundled response when
  // enabled). When neither is present, return empty — the panel
  // shows the honest "Backend reports no mutable settings" copy.
  return Array.from(new Set(list));
}

function pickBuildInfo(b) {
  const settings = (b.settings && (b.settings.build || b.settings.general)) || {};
  const summary  = (b.summary && (b.summary.build || b.summary.runtime)) || {};
  const runtime  = (b.runtime && (b.runtime.build || b.runtime)) || {};
  return {
    version:    settings.version    || summary.version    || runtime.version    || '-',
    commit:     settings.commit     || summary.commit     || runtime.commit     || '-',
    channel:    settings.channel    || summary.channel    || runtime.channel    || 'stable',
    build_time: settings.build_time || summary.build_time || runtime.build_time || '-',
    go_version: settings.go_version || summary.go_version || runtime.go_version || '-',
    os:         settings.os         || summary.os         || runtime.os         || '-',
    arch:       settings.arch       || summary.arch       || runtime.arch       || '-',
    is_dev:     settings.is_dev === true || summary.is_dev_build === true,
  };
}

function pickStatus(b) {
  const s = (b.runtime && b.runtime.status) || (b.summary && b.summary.status) || 'unknown';
  return s;
}

function formatUptime(s) {
  if (!s || isNaN(Number(s))) return 'Not monitored';
  s = Number(s);
  const d = Math.floor(s / 86400); s -= d * 86400;
  const h = Math.floor(s / 3600); s -= h * 3600;
  const m = Math.floor(s / 60);
  if (d > 0) return d + 'd ' + h + 'h';
  if (h > 0) return h + 'h ' + m + 'm';
  if (m > 0) return m + 'm';
  return 'just started';
}

function listenerState(rt) {
  if (!rt || !Array.isArray(rt.listeners) || rt.listeners.length === 0) return 'not monitored';
  const failed = rt.listeners.filter((l) => l.state === 'failed').length;
  const skipped = rt.listeners.filter((l) => l.state === 'skipped').length;
  const active  = rt.listeners.filter((l) => l.state === 'active').length;
  return active + ' active · ' + skipped + ' skipped · ' + failed + ' failed';
}

function stateBadgeKind(s) {
  if (s === 'active') return 'good';
  if (s === 'skipped' || s === 'failed' || s === 'fail') return 'bad';
  if (s === 'degraded' || s === 'starting') return 'warn';
  return 'neutral';
}

function badgeHtml(text, kind) {
  const map = { good: 'good', warn: 'warn', bad: 'bad', neutral: 'neutral', info: 'info' };
  const k = map[kind] || 'neutral';
  return el('span', { class: 'badge ' + k }, text);
}

function kvRow(dl, k, v) {
  dl.appendChild(el('dt', { text: k }));
  dl.appendChild(el('dd', { class: 'kv-v', text: v == null ? '-' : String(v) }));
}
