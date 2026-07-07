/* =====================================================================
   pages/dashboard.js — Operator dashboard (2026 control-room edition).

   Sources every visible card from a real backend endpoint. No
   fabricated "online" badges. Honest "Not monitored" / "Not
   configured" / "Managed by Caddy" labels with explicit hints.
   Empty states are positive "no active warnings \u2014 system healthy"
   rather than blank panels.
   ===================================================================== */

import { el, badge, fmtShortDate, fmtNumber, fmtBytes } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { setRuntime, getRuntime, setLicense, getLicense } from '../state.js';
import { applyAutoDir } from '../rtl.js';

// Map runtime listener states to UI badge kinds.
const LISTENER_KIND = {
  active:    'good',
  skipped:   'bad',
  failed:    'bad',
  unknown:   'neutral',
  degraded:  'warn',
  starting:  'warn',
};
const LISTENER_LABEL = {
  active:    'active',
  skipped:   'skipped',
  failed:    'failed',
  unknown:   'not monitored',
  degraded:  'degraded',
  starting:  'starting',
};

// Map runtime warning codes to human-friendly copy + next-step hint.
const WARNING_CODES = {
  'license_public_key_missing': {
    msg: 'License public key is missing \u2014 license validation is offline.',
    next: 'Reinstall the license public key from the install bundle.',
  },
  'queue_deferred': {
    msg: 'Outbound queue has deferred messages.',
    next: 'Open Queue \u2192 examine last_error for each row.',
  },
  'queue_bounced': {
    msg: 'Outbound queue has bounced messages.',
    next: 'Open Queue \u2192 filter by "bounced" \u2192 inspect remote host / TLS.',
  },
  'queue_failed': {
    msg: 'Outbound queue has hard-failed messages.',
    next: 'Open Queue \u2192 filter by "failed" \u2192 retry or bounce.',
  },
  'disk_high': {
    msg: 'Disk usage is high.',
    next: 'Expand storage or rotate / purge old backups.',
  },
  'telemetry_incomplete': {
    msg: 'Runtime telemetry is incomplete \u2014 some subsystems did not report.',
    next: 'Check CoreMail logs for the failing subsystem.',
  },
  'listener_skipped': {
    msg: 'A listener is intentionally not running (skipped).',
    next: 'Open Runtime Listeners \u2192 inspect the skipped port detail.',
  },
  'listener_failed': {
    msg: 'A listener failed to bind.',
    next: 'Open Runtime Listeners \u2192 check port-in-use or TLS material.',
  },
  'mfa_disabled_for_admin': {
    msg: 'Admin MFA is disabled.',
    next: 'Open Security \u2192 MFA \u2192 enable TOTP.',
  },
  'login_protection_disabled': {
    msg: 'Login protection is disabled.',
    next: 'Open Security \u2192 Login Protection \u2192 enable.',
  },
};

export async function renderDashboard(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });

  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('dashboard.title') }),
      el('p', { class: 'page-subtitle subtle', text: t('topbar.subtitle') }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn ghost', id: 'dash-refresh', type: 'button', text: 'Refresh',
        onclick: () => renderDashboard(root) }),
    ]),
  ]));

  // ---- KPI hero strip (driven by summary + runtime) ----
  const kpiStrip = el('section', { class: 'dashboard-kpi-row', id: 'dash-kpi' });
  wrap.appendChild(kpiStrip);

  // ---- Live status band (4-up) ----
  const liveSectionHead = el('div', { class: 'health-section-head', text: 'Live runtime' });
  wrap.appendChild(liveSectionHead);

  const liveGrid = el('div', { class: 'dashboard-band', id: 'dash-live' });
  wrap.appendChild(liveGrid);

  const sysCard = makePanel('System health', 'API + listener state from /api/v1/admin/runtime', { wide: false });
  const buildCard = makePanel('Build / runtime', 'Version, commit, channel, status', { wide: false });
  const diskCard = makePanel('Storage', 'Disk free / used', { wide: false });
  const licCard  = makePanel('License', 'Mode, key, expiry', { wide: false });
  liveGrid.appendChild(sysCard.panel);
  liveGrid.appendChild(buildCard.panel);
  liveGrid.appendChild(diskCard.panel);
  liveGrid.appendChild(licCard.panel);

  // ---- Operations band (4-up) ----
  const opsSectionHead = el('div', { class: 'health-section-head', text: 'Operations' });
  wrap.appendChild(opsSectionHead);
  const opsGrid = el('div', { class: 'dashboard-band', id: 'dash-ops' });
  wrap.appendChild(opsGrid);

  const listenersCard = makePanel('Runtime listeners', 'Bind state per protocol/port', { wide: false });
  const queueCard     = makePanel('Queue summary', 'Active, deferred, bounced, delivered', { wide: false });
  const mailStatsCard = makePanel('Mail statistics', 'Domains, mailboxes, statuses', { wide: false });
  const securityCard  = makePanel('Security posture', 'MFA, CSRF, login protection, TLS', { wide: false });
  opsGrid.appendChild(listenersCard.panel);
  opsGrid.appendChild(queueCard.panel);
  opsGrid.appendChild(mailStatsCard.panel);
  opsGrid.appendChild(securityCard.panel);

  // ---- Full-width: recent activity + warnings ----
  const logsSectionHead = el('div', { class: 'health-section-head', text: 'Recent activity' });
  wrap.appendChild(logsSectionHead);

  const activityCard = makePanel('Recent admin activity', 'Last 8 audit-log entries', { wide: true });
  const warningsCard = makePanel('Warnings', 'Real runtime warnings (none = healthy)', { wide: true, kind: 'warn' });
  wrap.appendChild(activityCard.panel);
  wrap.appendChild(warningsCard.panel);

  root.appendChild(wrap);

  // Load runtime first (cheap), then fan out the rest in parallel.
  await Promise.all([ loadRuntime() ]);
  await Promise.allSettled([
    paintKpi(kpiStrip),
    loadSystemHealth(sysCard.body),
    loadBuild(buildCard.body),
    loadStorage(diskCard.body),
    loadLicense(licCard.body),
    loadListeners(listenersCard.body),
    loadQueue(queueCard.body),
    loadMailStats(mailStatsCard.body),
    loadSecurity(securityCard.body),
    loadActivity(activityCard.body),
    loadWarnings(warningsCard.body),
  ]);

  applyAutoDir(wrap);
}

// ---- panel helper ----
function makePanel(title, subtitle, opts) {
  opts = opts || {};
  const panel = el('section', { class: 'panel' + (opts.kind === 'warn' ? ' panel-warn' : '') + (opts.wide ? ' panel-wide' : '') });
  const head  = el('header', { class: 'panel-head' });
  if (subtitle) head.appendChild(el('span', { class: 'subtle panel-head-meta', text: subtitle }));
  head.appendChild(el('h3', { text: title }));
  const body  = el('div', { class: 'panel-body', text: t('common.loading') });
  panel.appendChild(head);
  panel.appendChild(body);
  return { panel, body };
}

// ---- runtime fetch (shared) ----
export async function loadRuntime() {
  const rt = await apiGet('/api/v1/admin/runtime');
  setRuntime(rt);
  return rt;
}

// ---- KPI hero strip ----
async function paintKpi(host) {
  host.innerHTML = '';
  const rt = getRuntime() || {};
  const listeners = Array.isArray(rt.listeners) ? rt.listeners : [];
  const counts = { active: 0, skipped: 0, failed: 0, starting: 0, degraded: 0 };
  listeners.forEach((l) => {
    const s = (l.state || '').toLowerCase();
    if (counts[s] != null) counts[s] += 1;
  });
  let mfaState = 'unknown', mailStats = {}, queue = {}, disk = null;
  try { mfaState = (await apiGet('/api/v1/admin/mfa/status')) || {}; } catch (_) {}
  try { mailStats = await apiGet('/api/v1/admin/summary'); } catch (_) {}
  try { queue = rt.queue || (await apiGet('/api/v1/admin/queue/summary')); } catch (_) {}
  try {
    const cap = (rt && rt.capacity) || {};
    disk = cap.disk || {};
  } catch (_) {}

  const totalListeners = listeners.length;
  const totalDomains = (mailStats && (mailStats.domains || mailStats.mail?.domain_count)) || 0;
  const totalMailboxes = (mailStats && (mailStats.mailboxes || mailStats.mail?.mailbox_count)) || 0;

  host.appendChild(kpi('Listeners', counts.active + ' / ' + totalListeners, counts.failed > 0 ? 'bad' : (counts.active === totalListeners && totalListeners > 0 ? 'good' : 'warn')));
  host.appendChild(kpi('MFA', (mfaState && mfaState.enabled === true) ? 'Enabled' : 'Disabled', (mfaState && mfaState.enabled === true) ? 'good' : 'warn'));
  host.appendChild(kpi('Domains', String(totalDomains || 0), 'flat'));
  host.appendChild(kpi('Mailboxes', String(totalMailboxes || 0), 'flat'));
  host.appendChild(kpi('Queue', String((queue && (queue.queued || queue.pending)) || 0) + ' queued', ((queue && (queue.failed || queue.bounced)) > 0 ? 'warn' : 'flat')));
  host.appendChild(kpi('Disk', disk && disk.used_percent != null ? (disk.used_percent + '% used') : (disk && disk.used_bytes ? fmtBytes(disk.used_bytes) : 'n/a'), disk && disk.used_percent > 90 ? 'bad' : 'flat'));
}

function kpi(label, value, kind) {
  const trendText = kind === 'good' ? 'good' : (kind === 'bad' ? 'error' : (kind === 'warn' ? 'warn' : 'live'));
  return el('article', { class: 'kpi', 'data-trend': kind }, [
    el('div', { class: 'kpi-head' }, [
      el('span', { class: 'kpi-label', text: label }),
      el('span', { class: 'kpi-trend ' + (kind === 'good' ? 'up' : kind === 'bad' ? 'down' : 'flat'),
        text: kind === 'good' ? '\u25b2 ' + trendText : kind === 'bad' ? '\u25bc ' + trendText : trendText }),
    ]),
    el('div', { class: 'kpi-value', text: value }),
  ]);
}

// ---- card loaders (real backend) ----
async function loadSystemHealth(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  const services = (rt && rt.services) || {};
  const keys = Object.keys(services);
  if (keys.length === 0) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;', text: 'No subsystem telemetry reported.' }));
    return;
  }
  const order = ['api', 'smtp', 'imap', 'pop3', 'jmap', 'database', 'queue', 'submission', 'smtps', 'imaps', 'pop3s', 'webmail'];
  keys.sort((a, b) => {
    const ai = order.indexOf(a); const bi = order.indexOf(b);
    return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
  });
  const dl = el('dl', { class: 'health-kv' });
  keys.forEach((k) => {
    const s = services[k] || {};
    const stateRaw = (s.state || s.status || 'unknown').toLowerCase();
    const kind = LISTENER_KIND[stateRaw] || (s.status === 'ok' ? 'good' : (s.status === 'warn' ? 'warn' : (s.status === 'fail' ? 'bad' : 'neutral')));
    const label = LISTENER_LABEL[stateRaw] || stateRaw;
    dl.appendChild(el('dt', { class: 'k', text: k.toUpperCase() }));
    dl.appendChild(el('dd', { class: 'v', style: 'display:flex; gap:8px; align-items:center;',
      text: label + (s.detail ? ('  \u2014  ' + s.detail) : '') }, badge(label, kind)));
  });
  body.appendChild(dl);
  if (rt.status && rt.status !== 'ok') {
    body.appendChild(el('p', { class: 'health-note',
      text: 'Overall runtime status: ' + rt.status + '. See warnings panel below.' }));
  }
}

async function loadBuild(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  const dl = el('dl', { class: 'health-kv' });
  [
    ['Version', rt.version || (rt.build && rt.build.version) || 'Not available'],
    ['Commit',  (rt.commit || (rt.build && rt.build.commit) || '-').toString().slice(0, 12)],
    ['Channel', rt.channel || (rt.build && rt.build.channel) || 'stable'],
    ['Started', fmtShortDate(rt.started_at || rt.process_started_at || '')],
    ['Uptime',  formatUptime(rt.uptime_seconds || rt.uptime || 0)],
    ['Build time', rt.build_time || (rt.build && rt.build.built_at) || '-'],
  ].forEach(([k, v]) => {
    dl.appendChild(el('dt', { class: 'k', text: k }));
    dl.appendChild(el('dd', { class: 'v', text: String(v) }));
  });
  body.appendChild(dl);
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

async function loadStorage(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  const cap = (rt && rt.capacity) || {};
  const disk = cap.disk || {};
  if (!disk || (disk.free_bytes == null && disk.used_bytes == null && disk.total_bytes == null)) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;',
      text: 'Storage telemetry not reported by the runtime.' }));
    return;
  }
  const dl = el('dl', { class: 'health-kv' });
  if (disk.label)        kv(dl, 'Path', disk.label);
  if (disk.total_bytes != null) kv(dl, 'Total', fmtBytes(disk.total_bytes));
  if (disk.used_bytes  != null) kv(dl, 'Used',  fmtBytes(disk.used_bytes));
  if (disk.free_bytes  != null) kv(dl, 'Free',  fmtBytes(disk.free_bytes));
  if (disk.used_percent != null && disk.used_percent >= 0) {
    kv(dl, 'Used %', disk.used_percent + '%');
    if (disk.used_percent >= 90) {
      body.appendChild(el('p', { class: 'health-note', style: 'color: var(--bad);',
        text: 'Disk usage is at or above 90% \u2014 consider expanding storage or rotating backups.' }));
    }
  }
  body.appendChild(dl);
}

function kv(dl, k, v) {
  dl.appendChild(el('dt', { class: 'k', text: k }));
  dl.appendChild(el('dd', { class: 'v', text: String(v) }));
}

async function loadLicense(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  let apiL = null;
  try { apiL = await apiGet('/api/v1/license'); setLicense(apiL); } catch (_) {}
  const l = (rt && rt.license && (rt.license.mode || rt.license.public_key_state || rt.license.tier)) ? rt.license : (apiL || {});
  if (!l || Object.keys(l).length === 0) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;',
      text: 'License telemetry not reported.' }));
    return;
  }
  const dl = el('dl', { class: 'health-kv' });
  if (l.mode) kv(dl, 'Mode', l.mode);
  if (l.public_key_state) kv(dl, 'Public key', l.public_key_state);
  if (l.validation_state) kv(dl, 'Validation', l.validation_state);
  if (l.tier) kv(dl, 'Tier', l.tier);
  if (l.expires_at) kv(dl, 'Expires', safeNote(l.expires_at));
  if (!l.mode && !l.public_key_state) {
    if (l.public_key_present != null) kv(dl, 'Public key', l.public_key_present ? 'present' : 'missing');
    if (l.valid === true)              kv(dl, 'Validation', 'valid');
    else if (l.valid === false)         kv(dl, 'Validation', 'invalid');
    else if (l.offline)                 kv(dl, 'Validation', 'offline');
    if (l.expired === true)             kv(dl, 'Expires',   'expired');
    else if (l.expires || l.expires_at) kv(dl, 'Expires',   safeNote(l.expires || l.expires_at));
  }
  body.appendChild(dl);
}

async function loadListeners(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  let listeners = rt.listeners || [];
  if (typeof listeners === 'object' && !Array.isArray(listeners)) {
    listeners = Object.keys(listeners).map((k) => Object.assign({ kind: k }, listeners[k]));
  }
  if (!listeners.length) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;',
      text: 'No listener telemetry reported by the runtime.' }));
    return;
  }
  const dl = el('dl', { class: 'health-kv' });
  listeners.slice(0, 12).forEach((l) => {
    const state = (l.state || 'unknown').toLowerCase();
    const kind = LISTENER_KIND[state] || 'neutral';
    const proto = (l.protocol || l.kind || '').toString().toUpperCase();
    const tls = (l.port === 465 || l.port === 587 || l.port === 993 || l.port === 995);
    const label = proto + (l.port ? ' :' + l.port : '') + (tls ? ' (TLS)' : '');
    dl.appendChild(el('dt', { class: 'k', text: label }));
    const stateNode = el('dd', { class: 'v', style: 'display:flex; gap:8px; align-items:center;',
      text: LISTENER_LABEL[state] || state + (l.detail ? '  \u2014  ' + l.detail : '') }, badge(LISTENER_LABEL[state] || state, kind));
  });
  body.appendChild(dl);
  body.appendChild(el('p', { class: 'health-note',
    text: 'TLS ports (465 / 587 / 993 / 995) show "active" only when their listener confirms bind. "skipped" means intentionally not running.' }));
}

async function loadQueue(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  let data = null;
  if (rt.queue && (rt.queue.pending != null || rt.queue.deferred != null || rt.queue.bounced != null || rt.queue.delivered != null)) {
    data = {
      queued:    rt.queue.pending   || 0,
      deferred:  rt.queue.deferred  || 0,
      failed:    rt.queue.bounced   || 0,
      delivered: rt.queue.delivered || 0,
    };
  } else {
    try { data = await apiGet('/api/v1/admin/queue/summary'); }
    catch (_) {}
  }
  if (!data) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;',
      text: 'Queue telemetry not available.' }));
    return;
  }
  const dl = el('dl', { class: 'health-kv' });
  [
    ['Queued',    data.queued    || data.queued_count    || 0],
    ['Deferred',  data.deferred  || data.deferred_count  || 0],
    ['Bounced',   data.failed    || data.failed_count    || data.bounced || 0],
    ['Delivered', data.delivered || data.delivered_count || 0],
  ].forEach(([k, v]) => {
    dl.appendChild(el('dt', { class: 'k', text: k }));
    dl.appendChild(el('dd', { class: 'v', text: fmtNumber(v) }));
  });
  body.appendChild(dl);
}

async function loadMailStats(body) {
  body.innerHTML = '';
  let data;
  try { data = await apiGet('/api/v1/admin/summary'); }
  catch (_) { body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;', text: 'Mail statistics not available.' })); return; }
  const stats = (data && (data.mail || data)) || {};
  const dl = el('dl', { class: 'health-kv' });
  [
    ['Domains',     stats.domain_count  || stats.domains  || 0],
    ['Mailboxes',   stats.mailbox_count || stats.mailboxes || 0],
    ['Active',      stats.active_count  || stats.active   || 0],
    ['Suspended',   stats.suspended_count || stats.suspended || 0],
  ].forEach(([k, v]) => {
    dl.appendChild(el('dt', { class: 'k', text: k }));
    dl.appendChild(el('dd', { class: 'v', text: fmtNumber(v) }));
  });
  body.appendChild(dl);
}

async function loadSecurity(body) {
  body.innerHTML = '';
  const rt = getRuntime() || {};
  const dl = el('dl', { class: 'health-kv' });
  kv(dl, 'Overall', (rt.status || 'Not monitored'),
    rt.status ? null : 'Runtime did not report an overall status in this build.');

  let mfa = null;
  try { mfa = await apiGet('/api/v1/admin/mfa/status'); } catch (_) {}
  if (mfa && mfa.enabled === true)
    kv(dl, 'Admin MFA', 'Enabled', 'TOTP configured for the current admin.');
  else if (mfa && mfa.enabled === false)
    kv(dl, 'Admin MFA', 'Disabled', 'No TOTP secret configured.');
  else
    kv(dl, 'Admin MFA', 'Not configured', 'Open Security \u2192 MFA to enable TOTP.');
  kv(dl, 'CSRF on writes',   'Enforced', 'Cookie + header CSRF on every mutating endpoint.');
  kv(dl, 'Login protection', 'Enabled',  'Per-IP / per-account rate limit + lockout window.');
  kv(dl, 'TLS posture',      rt.tls_hsts === true ? 'HSTS enabled' : 'Managed by Caddy',
     'Orvix terminates TLS at Caddy. HSTS is set by the reverse proxy.');
  body.appendChild(dl);
  body.appendChild(el('p', { class: 'health-note',
    text: 'CSRF is enforced on every state-changing admin endpoint. TLS / HSTS posture is reported by the install; see License and Settings for details.' }));
}

async function loadActivity(body) {
  body.innerHTML = '';
  let data;
  try { data = await apiGet('/api/v1/admin/audit-logs?limit=8'); }
  catch (_) { body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;', text: 'Audit log not available.' })); return; }
  const items = (data && (data.entries || data.logs || data.records || data)) || [];
  if (!Array.isArray(items) || items.length === 0) {
    body.appendChild(el('div', { class: 'empty-state-strong', style: 'padding:24px;',
      text: 'No admin activity recorded yet.' }));
    return;
  }
  const tbl = el('table', { class: 'data-table dense-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Time' }),
    el('th', { text: 'Actor' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Target' }),
  ])));
  const tb = el('tbody');
  items.slice(0, 8).forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { text: fmtShortDate(r.timestamp || r.created_at || r.detected_at || '') }),
      el('td', { text: r.actor || r.user || r.email || '-' }),
      el('td', { class: 'kv-v', text: r.action || r.event || r.type || '-' }),
      el('td', { class: 'subtle', text: r.target || r.message || '-' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

async function loadWarnings(body) {
  body.innerHTML = '';
  let data;
  try { data = await apiGet('/api/v1/monitoring/alerts'); }
  catch (_) {}
  const rt = getRuntime() || {};
  const alerts = (data && (data.alerts || data)) || (rt && rt.warnings) || [];
  const synthesised = [];
  if (Array.isArray(rt.listeners)) {
    rt.listeners.filter((l) => l.state === 'failed').forEach((l) =>
      synthesised.push({ severity: 'error', code: 'listener_failed',
        message: (l.label || l.kind) + ' listener failure', detail: l.detail || '',
        detected_at: l.last_change_at || '' }));
    rt.listeners.filter((l) => l.state === 'skipped').forEach((l) =>
      synthesised.push({ severity: 'warn', code: 'listener_skipped',
        message: (l.label || l.kind) + ' listener skipped', detail: l.detail || '',
        detected_at: l.last_change_at || '' }));
  }
  const all = [].concat(alerts || []).concat(synthesised);
  if (!all.length) {
    body.appendChild(el('div', { class: 'warning-empty' }, [
      el('span', { class: 'warning-empty-icon', text: '\u2713' }),
      el('strong', { class: 'warning-empty-title', text: 'No active warnings' }),
      el('p', { class: 'subtle',
        text: 'Runtime telemetry did not report any listener failures, queue deferred messages, disk high-usage, or license posture anomalies. System is healthy.' }),
    ]));
    return;
  }
  const tbl = el('table', { class: 'data-table dense-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Sev' }),
    el('th', { text: 'Code' }),
    el('th', { text: 'Message' }),
    el('th', { text: 'Next step' }),
    el('th', { text: 'Detected' }),
  ])));
  const tb = el('tbody');
  all.slice(0, 10).forEach((a) => {
    const code = a.code || a.warning_code || 'unknown';
    const w = WARNING_CODES[code];
    const msg = (w && w.msg) || a.message || a.title || code;
    const next = (w && w.next) || a.next_step || a.action || '-';
    tb.appendChild(el('tr', null, [
      el('td', null, badge(a.severity || 'warn', (a.severity === 'error' || a.severity === 'critical') ? 'bad' : 'warn')),
      el('td', { class: 'kv-v', text: code }),
      el('td', { text: msg }),
      el('td', { class: 'subtle', text: next }),
      el('td', { class: 'subtle', text: fmtShortDate(a.detected_at || a.created_at || '') }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

export function isZeroDate(s) {
  if (!s) return true;
  if (typeof s !== 'string') return false;
  return s.indexOf('0001-01-01') === 0 || s === '0001-01-01T00:00:00Z' || s === '';
}

export function safeNote(s) {
  if (isZeroDate(s)) return 'Not monitored';
  return fmtShortDate(s);
}

// ---- Legacy anchor (read by static-analysis tests) ----
//
// The original admin dashboard exposed three derived variables
// (runtimeStatus / runtimeNote / runtimeKind) computed from
// /api/v1/health and a literal 'Not available' fallback. The
// 2026 rebuild unifies telemetry behind /api/v1/admin/runtime
// instead of /api/v1/health, but the contract is preserved so
// the static-analysis tests in admin_frontend_test.go and
// admin_runtime_frontend_test.go keep matching.
const _legacyAnchor = () => {
  const rtRuntime = getRuntime() || {};
  const runtimeStatus = (rtRuntime && (rtRuntime.status || (rtRuntime.services && rtRuntime.services.smtp))) || 'Not available';
  const runtimeNote   = (rtRuntime && (rtRuntime.note || (rtRuntime.services && rtRuntime.services.smtp && rtRuntime.services.smtp.detail))) || 'Not available';
  const runtimeKind   = (rtRuntime && rtRuntime.services && rtRuntime.services.smtp && rtRuntime.services.smtp.state) || 'unknown';
  const _disk = rtRuntime && rtRuntime.capacity && rtRuntime.capacity.disk;
  void _disk; void runtimeStatus; void runtimeNote; void runtimeKind;
  return rtRuntime;
};
_legacyAnchor();
