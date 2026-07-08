/* =====================================================================
   scripts/smoke-admin-visual-screenshots.mjs — Visual Renaissance v2
   Screenshot harness.

   Spins up the admin bundle (mocked API), launches headless Chrome,
   navigates to each premium page, opens each major modal, and saves
   a PNG screenshot per route to artifacts/admin-visual/.

   Output:
     artifacts/admin-visual/dashboard.png
     artifacts/admin-visual/domains.png
     artifacts/admin-visual/domain-create-modal.png
     artifacts/admin-visual/runtime-listeners.png
     artifacts/admin-visual/antivirus.png
     artifacts/admin-visual/admin-groups-modal.png
     artifacts/admin-visual/acl-modal.png
     artifacts/admin-visual/acceptance-modal.png
     artifacts/admin-visual/incoming-rules-modal.png
     artifacts/admin-visual/mailing-list-modal.png
     artifacts/admin-visual/public-folder-modal.png
     artifacts/admin-visual/settings.png
     artifacts/admin-visual/security.png

   Zero external dependencies beyond the existing Chrome detection
   in the admin functional browser smoke.
   ===================================================================== */

import http from 'node:http';
import fs from 'node:fs/promises';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';

const adminDir = path.resolve(process.argv[2] || 'release/admin');
const outDir = path.resolve(process.argv[3] || 'artifacts/admin-visual');
const failures = [];
const consoleErrors = [];
const requests = [];

function fail(msg) { throw new Error(msg); }
function log(...a) { process.stdout.write(a.join(' ') + '\n'); }
function err(...a) { process.stderr.write(a.join(' ') + '\n'); }

function findChrome() {
  const env = process.env.CHROME || process.env.CHROMIUM || process.env.CHROME_BIN;
  const candidates = [
    env,
    'google-chrome',
    'google-chrome-stable',
    'chromium',
    'chromium-browser',
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
    'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  ].filter(Boolean);
  for (const c of candidates) {
    if (c.includes('\\') || c.includes('/')) {
      if (existsSync(c)) return c;
      continue;
    }
    const pathEnv = process.env.PATH || '';
    const exts = process.platform === 'win32' ? ['.exe', '.cmd', '.bat', ''] : [''];
    for (const dir of pathEnv.split(path.delimiter)) {
      for (const ext of exts) {
        const p = path.join(dir, c + ext);
        if (existsSync(p)) return p;
      }
    }
  }
  return '';
}

function contentType(file) {
  if (file.endsWith('.html')) return 'text/html; charset=utf-8';
  if (file.endsWith('.js'))   return 'text/javascript; charset=utf-8';
  if (file.endsWith('.css'))  return 'text/css; charset=utf-8';
  if (file.endsWith('.svg'))  return 'image/svg+xml';
  if (file.endsWith('.png'))  return 'image/png';
  if (file.endsWith('.json')) return 'application/json';
  return 'application/octet-stream';
}

async function readBody(req) {
  const chunks = [];
  for await (const c of req) chunks.push(c);
  const raw = Buffer.concat(chunks).toString('utf8');
  if (!raw) return {};
  try { return JSON.parse(raw); } catch { return {}; }
}

function sendJSON(res, status, body) {
  res.writeHead(status, {
    'content-type': 'application/json',
    'cache-control': 'no-store',
    'access-control-allow-origin': '*',
  });
  res.end(JSON.stringify(body));
}

function send(res, status, type, body) {
  res.writeHead(status, {
    'content-type': type,
    'cache-control': 'no-store',
    'access-control-allow-origin': '*',
  });
  res.end(body);
}

// Realistic mock dataset — used by every page render.
const MOCK = {
  profile: {
    id: 1, email: 'admin@orvix.local', roles: ['admin'],
    username: 'admin', mfa_enabled: true,
  },
  runtime: {
    status: 'ok',
    version: '1.0.3',
    commit: '339028e56f0f659f6c90da62c5d4a13929fa8a64',
    channel: 'stable',
    build_time: '2026-06-06T14:00:00Z',
    started_at: '2026-07-08T09:00:00Z',
    uptime_seconds: 18360,
    tls_hsts: true,
    services: {
      api:       { state: 'active', detail: 'Fiber v3 listening on :8080' },
      smtp:      { state: 'active', detail: 'Submission on :587' },
      smtps:     { state: 'active', detail: 'Implicit TLS on :465' },
      imap:      { state: 'active', detail: 'STARTTLS on :143' },
      imaps:     { state: 'active', detail: 'Implicit TLS on :993' },
      pop3:      { state: 'skipped', detail: 'POP3 not configured for this deployment' },
      pop3s:     { state: 'skipped', detail: 'POP3S not configured' },
      jmap:      { state: 'active', detail: 'JMAP on :443' },
      database:  { state: 'active', detail: 'SQLite primary, 4.2MB' },
      queue:     { state: 'active', detail: 'Redis-backed retry queue' },
      webmail:   { state: 'active', detail: 'Vite-bundled webmail SPA on :3000' },
    },
    listeners: [
      { protocol: 'smtp',  kind: 'smtp',  port: 587, state: 'active', detail: 'Submission, STARTTLS required', bind: '0.0.0.0:587', last_change_at: '2026-07-08T09:00:30Z' },
      { protocol: 'smtps', kind: 'smtps', port: 465, state: 'active', detail: 'Implicit TLS',                 bind: '0.0.0.0:465', last_change_at: '2026-07-08T09:00:30Z' },
      { protocol: 'imap',  kind: 'imap',  port: 143, state: 'active', detail: 'STARTTLS',                      bind: '0.0.0.0:143', last_change_at: '2026-07-08T09:00:30Z' },
      { protocol: 'imaps', kind: 'imaps', port: 993, state: 'active', detail: 'Implicit TLS',                  bind: '0.0.0.0:993', last_change_at: '2026-07-08T09:00:30Z' },
      { protocol: 'pop3',  kind: 'pop3',  port: 110, state: 'skipped',detail: 'POP3 disabled in this build',   bind: '-',          last_change_at: '2026-07-08T08:00:00Z' },
      { protocol: 'pop3s', kind: 'pop3s', port: 995, state: 'skipped',detail: 'POP3S disabled in this build',  bind: '-',          last_change_at: '2026-07-08T08:00:00Z' },
      { protocol: 'jmap',  kind: 'jmap',  port: 443, state: 'active', detail: 'JMAP on the public listener',   bind: '0.0.0.0:443', last_change_at: '2026-07-08T09:00:30Z' },
    ],
    capacity: {
      disk: { label: '/var/lib/orvix', total_bytes: 107374182400, used_bytes: 41055272960, free_bytes: 66318909440, used_percent: 38 },
    },
    queue: { pending: 7, deferred: 2, bounced: 1, delivered: 18432 },
    license: { mode: 'enterprise', tier: 'enterprise', valid: true, validation_state: 'valid', public_key_state: 'present', expires_at: '2027-06-06T00:00:00Z' },
    collected_at: '2026-07-08T13:55:00Z',
  },
  mfa: { enabled: true, totp_configured: true, recovery_codes: 8 },
  license: { mode: 'enterprise', tier: 'enterprise', valid: true, validation_state: 'valid', public_key_state: 'present', expires_at: '2027-06-06T00:00:00Z', seats: 250, seats_used: 47 },
  summary: { domains: 4, mailboxes: 47, active_count: 44, suspended_count: 3 },
  queue: { queued_count: 7, deferred_count: 2, failed_count: 1, bounced_count: 1, delivered_count: 18432 },
  domains: {
    domains: [
      { name: 'reachfm.io',     status: 'active',    plan: 'enterprise', mailbox_count: 23, max_mailboxes: 200, quota_mb: 5120 },
      { name: 'orvix.email',    status: 'active',    plan: 'enterprise', mailbox_count: 14, max_mailboxes: 200, quota_mb: 5120 },
      { name: 'staging.local',  status: 'active',    plan: 'smb',        mailbox_count: 6,  max_mailboxes: 50,  quota_mb: 1024 },
      { name: 'legacy.test',    status: 'suspended', plan: 'smb',        mailbox_count: 4,  max_mailboxes: 25,  quota_mb: 512  },
    ],
  },
  mailboxes: {
    mailboxes: [
      { id: 1, email: 'admin@reachfm.io',  domain: 'reachfm.io',    quota_mb: 5120, used_mb: 412, status: 'active',    last_login_at: '2026-07-08T08:00:00Z' },
      { id: 2, email: 'ops@reachfm.io',    domain: 'reachfm.io',    quota_mb: 5120, used_mb: 1204,status: 'active',    last_login_at: '2026-07-08T11:30:00Z' },
      { id: 3, email: 'sales@orvix.email', domain: 'orvix.email',   quota_mb: 5120, used_mb: 3180,status: 'active',    last_login_at: '2026-07-07T17:00:00Z' },
      { id: 4, email: 'archive@staging.local', domain: 'staging.local', quota_mb: 1024, used_mb: 998, status: 'suspended', last_login_at: '2026-05-01T00:00:00Z' },
    ],
  },
  auditLogs: {
    entries: [
      { timestamp: '2026-07-08T13:50:00Z', actor: 'admin@orvix.local', action: 'domain.update',  target: 'reachfm.io',     message: 'Updated DNS records' },
      { timestamp: '2026-07-08T13:42:00Z', actor: 'admin@orvix.local', action: 'mailbox.create', target: 'sales@orvix.email', message: 'Created mailbox' },
      { timestamp: '2026-07-08T12:11:00Z', actor: 'admin@orvix.local', action: 'login.success',  target: 'admin@orvix.local', message: 'IP 65.75.203.74' },
      { timestamp: '2026-07-08T11:05:00Z', actor: 'admin@orvix.local', action: 'queue.retry',    target: 'msg #18432',     message: 'Retried after TLS failure' },
      { timestamp: '2026-07-08T10:00:00Z', actor: 'admin@orvix.local', action: 'backup.run',     target: 'scheduled',      message: 'Nightly backup completed' },
    ],
  },
  alerts: { alerts: [] },
  antivirus: {
    engine: 'clamav',
    engine_configured: true,
    engine_reachable: true,
    engine_active: true,
    clamav_host: '127.0.0.1',
    clamav_port: 3310,
    clamav_response: 'PONG',
    last_probe_at: '2026-07-08T13:55:00Z',
    antispam_engine: 'rspamd',
    antispam_active: false,
    antispam_reachable: false,
    antispam_response: 'not configured',
    routing_engine: 'in-process',
    routing_active: true,
    incoming_msg_rules: 'in-process',
    incoming_msg_active: true,
    honest_notes: [
      'Anti-spam engine is not wired in this build — per-mailbox webmail filters are the active filter pipeline.',
      'Routing rules are stored; the runtime walker executes them at SMTP-time.',
    ],
  },
  aclRules: {
    rules: [
      { id: 1, priority: 10, action: 'deny',  protocol: 'smtp', source: '198.51.100.0/24', note: 'Known spam CIDR' },
      { id: 2, priority: 50, action: 'allow', protocol: 'imap', source: '203.0.113.42',    note: 'Office VPN' },
      { id: 3, priority: 90, action: 'allow', protocol: 'all',  source: '*',               note: 'Default allow' },
    ],
  },
  acceptanceRules: {
    rules: [
      { id: 1, priority: 10, scope: 'global', action: 'accept',    enabled: true, note: 'Default' },
      { id: 2, priority: 50, scope: 'domain', scope_target: 'reachfm.io', action: 'quarantine', enabled: true, note: 'Quarantine inbound until review' },
    ],
  },
  incomingMsgRules: {
    rules: [
      { id: 1, field: 'subject', operator: 'contains', value: 'invoice', action: 'tag', enabled: true, note: 'Auto-tag finance' },
      { id: 2, field: 'from',    operator: 'equals',   value: 'noreply@spam.example', action: 'quarantine', enabled: true, note: 'Quarantine known spam sender' },
    ],
  },
  mailingLists: {
    lists: [
      { address: 'all@reachfm.io',         domain: 'reachfm.io',  members: 23, status: 'active' },
      { address: 'engineering@orvix.email', domain: 'orvix.email', members: 12, status: 'active' },
      { address: 'announce@reachfm.io',    domain: 'reachfm.io',  members: 47, status: 'active' },
    ],
  },
  publicFolders: {
    folders: [
      { owner_mailbox: 'archive@reachfm.io', folder_path: '/shared/announcements', display_name: 'Announcements', description: 'Read-only company-wide announcements', read_only: true },
      { owner_mailbox: 'archive@reachfm.io', folder_path: '/shared/handbook',      display_name: 'Handbook',      description: 'Read-write shared engineering handbook', read_only: false },
    ],
  },
  adminGroups: {
    groups: [
      { id: 1, name: 'admins',     description: 'Full admin grants', is_builtin: true,  members: ['admin@orvix.local'],         grants: ['domains:*', 'mailboxes:*', 'admin:*', 'security:*'] },
      { id: 2, name: 'operators',  description: 'Day-to-day operators', is_builtin: false, members: ['ops@reachfm.io'],             grants: ['domains:read', 'mailboxes:write', 'queue:retry', 'logs:read'] },
      { id: 3, name: 'auditors',   description: 'Read-only access',      is_builtin: false, members: ['audit@reachfm.io'],           grants: ['*:read'] },
    ],
  },
  adminUsers: {
    users: [
      { id: 1, email: 'admin@orvix.local',  role: 'admin',    mfa_enabled: true,  last_login_at: '2026-07-08T11:00:00Z' },
      { id: 2, email: 'ops@reachfm.io',     role: 'operator', mfa_enabled: true,  last_login_at: '2026-07-08T08:30:00Z' },
      { id: 3, email: 'audit@reachfm.io',   role: 'auditor',  mfa_enabled: false, last_login_at: '2026-07-06T15:00:00Z' },
    ],
  },
  settings: {
    settings: {
      site_name: 'ReachFM Mail',
      default_quota_mb: 5120,
      max_mailbox_quota_mb: 51200,
      password_min_length: 12,
      session_timeout_minutes: 60,
      login_max_attempts: 5,
      login_lockout_minutes: 15,
      tls_min_version: '1.2',
      enforce_mfa_admins: true,
      audit_log_retention_days: 365,
    },
  },
  security: {
    overall: 'hardened',
    mfa_admins: 'enabled',
    csrf: 'enforced',
    rate_limit: 'enabled',
    tls: 'caddy-managed',
    login_protection: 'enabled',
    audit_logging: 'on',
  },
};

// Mock server: serves release/admin and a realistic API.
function startServer() {
  return new Promise((resolve, reject) => {
    const server = http.createServer(async (req, res) => {
      try {
        const url = new URL(req.url, 'http://127.0.0.1');
        requests.push(`${req.method} ${url.pathname}`);
        // API mocks
        if (url.pathname.startsWith('/api/')) {
          if (url.pathname === '/api/v1/health') return sendJSON(res, 200, { status: 'ok' });
          if (url.pathname === '/api/v1/me') return sendJSON(res, 200, MOCK.profile);
          if (url.pathname === '/api/v1/csrf-token') return sendJSON(res, 200, { token: 'csrf-mock-token' });
          if (url.pathname === '/api/v1/auth/login') {
            const body = await readBody(req);
            if (!body || !body.email || !body.password) return sendJSON(res, 400, { error: 'missing credentials' });
            return sendJSON(res, 200, { token: 'mock-admin-token', refresh_token: 'mock-refresh', profile: MOCK.profile });
          }
          if (url.pathname === '/api/v1/admin/runtime')         return sendJSON(res, 200, MOCK.runtime);
          if (url.pathname === '/api/v1/admin/mfa/status')      return sendJSON(res, 200, MOCK.mfa);
          if (url.pathname === '/api/v1/license')               return sendJSON(res, 200, MOCK.license);
          if (url.pathname === '/api/v1/admin/summary')         return sendJSON(res, 200, MOCK.summary);
          if (url.pathname === '/api/v1/admin/queue/summary')   return sendJSON(res, 200, MOCK.queue);
          if (url.pathname === '/api/v1/admin/audit-logs')      return sendJSON(res, 200, MOCK.auditLogs);
          if (url.pathname === '/api/v1/monitoring/alerts')     return sendJSON(res, 200, MOCK.alerts);
          if (url.pathname === '/api/v1/admin/security/antivirus') return sendJSON(res, 200, MOCK.antivirus);
          if (url.pathname === '/api/v1/domains')               return sendJSON(res, 200, MOCK.domains);
          if (url.pathname === '/api/v1/mailboxes')             return sendJSON(res, 200, MOCK.mailboxes);
          if (url.pathname === '/api/v1/admin/acl-rules')       return sendJSON(res, 200, MOCK.aclRules);
          if (url.pathname === '/api/v1/admin/acceptance-rules') return sendJSON(res, 200, MOCK.acceptanceRules);
          if (url.pathname === '/api/v1/admin/incoming-msg-rules') return sendJSON(res, 200, MOCK.incomingMsgRules);
          if (url.pathname === '/api/v1/admin/mailing-lists')   return sendJSON(res, 200, MOCK.mailingLists);
          if (url.pathname === '/api/v1/admin/public-folders')  return sendJSON(res, 200, MOCK.publicFolders);
          if (url.pathname === '/api/v1/admin/admin-groups')    return sendJSON(res, 200, MOCK.adminGroups);
          if (url.pathname === '/api/v1/admin/admin-users')     return sendJSON(res, 200, MOCK.adminUsers);
          if (url.pathname === '/api/v1/admin/settings')        return sendJSON(res, 200, MOCK.settings);
          if (url.pathname.startsWith('/api/v1/admin/settings')) return sendJSON(res, 200, { settings: MOCK.settings.settings });
          // Default 200 stub for unmodelled API
          return sendJSON(res, 200, { ok: true });
        }
        // Static admin assets
        const safe = url.pathname.replace(/\.\.+/g, '');
        let p = safe;
        if (p.startsWith('/admin/')) p = p.slice('/admin'.length);
        if (p === '' || p === '/') p = '/index.html';
        const full = path.join(adminDir, p);
        if (!existsSync(full)) {
          res.writeHead(404, { 'content-type': 'text/plain' });
          return res.end('not found: ' + p);
        }
        const stat = await fs.stat(full);
        if (stat.isDirectory()) {
          const idx = path.join(full, 'index.html');
          if (existsSync(idx)) {
            const data = await fs.readFile(idx);
            return send(res, 200, contentType(idx), data);
          }
          res.writeHead(404, { 'content-type': 'text/plain' });
          return res.end('directory listing denied: ' + p);
        }
        const data = await fs.readFile(full);
        return send(res, 200, contentType(full), data);
      } catch (e) {
        res.writeHead(500, { 'content-type': 'text/plain' });
        res.end('server error: ' + e.message);
      }
    });
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      resolve({ server, port, baseUrl: 'http://127.0.0.1:' + port + '/admin/' });
    });
    server.on('error', reject);
  });
}

async function launchChrome(userDataDir) {
  const chrome = findChrome();
  if (!chrome) fail('Chrome / Edge not found on this machine');
  const port = 9222 + Math.floor(Math.random() * 1000);
  const args = [
    '--headless=new',
    '--no-sandbox',
    '--disable-gpu',
    '--disable-dev-shm-usage',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-timer-throttling',
    '--disable-backgrounding-occluded-windows',
    '--disable-features=TranslateUI',
    '--force-prefers-color-scheme=dark',
    '--enable-features=WebContentsForceDark',
    '--window-size=1440,900',
    '--user-data-dir=' + userDataDir,
    '--remote-debugging-port=' + port,
    '--remote-allow-origins=*',
    'about:blank',
  ];
  log('[chrome]', chrome);
  log('[chrome]', args.join(' '));
  const proc = spawn(chrome, args, { stdio: ['ignore', 'pipe', 'pipe'] });
  proc.stdout.on('data', (d) => process.stderr.write('[chrome] ' + d.toString()));
  proc.stderr.on('data', (d) => {
    const s = d.toString();
    if (/DevTools listening on/.test(s)) {
      log('[chrome] devtools ready on port ' + port);
    }
  });
  // Wait for DevTools to be reachable.
  for (let i = 0; i < 60; i++) {
    await new Promise((r) => setTimeout(r, 500));
    try {
      const r = await fetch('http://127.0.0.1:' + port + '/json/version');
      if (r.ok) {
        const j = await r.json();
        log('[chrome] webSocketDebuggerUrl', j.webSocketDebuggerUrl);
        return { proc, port, wsBase: 'ws://127.0.0.1:' + port };
      }
    } catch (_) {}
  }
  fail('Chrome did not expose DevTools within 30s');
}

async function fetchJson(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error('http ' + r.status + ' for ' + url);
  return r.json();
}

async function connectCDP(port) {
  // Use the dynamic import of the ws shim — Node 22+ has a global WebSocket.
  if (typeof WebSocket === 'undefined') fail('Node WebSocket is not available; use Node 22+');
  const tabs = await fetchJson('http://127.0.0.1:' + port + '/json');
  const tab = tabs.find((t) => t.type === 'page') || tabs[0];
  if (!tab) fail('no CDP tab found');
  return new WebSocket(tab.webSocketDebuggerUrl);
}

function cdpSend(ws, id, method, params = {}) {
  return new Promise((resolve, reject) => {
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id === id) {
        ws.removeEventListener('message', onMsg);
        if (m.error) reject(new Error(m.error.message + ' [' + method + ']'));
        else resolve(m.result);
      }
    };
    ws.addEventListener('message', onMsg);
    ws.send(JSON.stringify({ id, method, params }));
  });
}

let _id = 0;
function nextId() { return ++_id; }

async function cdpNavigate(ws, url) {
  return cdpSend(ws, nextId(), 'Page.navigate', { url });
}

async function cdpWaitLoad(ws, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => { ws.removeEventListener('message', onLoad); reject(new Error('load timeout')); }, timeoutMs);
    const onLoad = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.method === 'Page.loadEventFired') { clearTimeout(t); ws.removeEventListener('message', onLoad); resolve(); }
    };
    ws.addEventListener('message', onLoad);
  });
}

async function cdpEval(ws, expr) {
  const r = await cdpSend(ws, nextId(), 'Runtime.evaluate', {
    expression: expr,
    awaitPromise: true,
    returnByValue: true,
  });
  if (r.exceptionDetails) {
    const text = (r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'eval error';
    throw new Error(text);
  }
  return r.result.value;
}

async function cdpScreenshot(ws, file) {
  const r = await cdpSend(ws, nextId(), 'Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  if (!r || !r.data) fail('no screenshot data');
  await fs.writeFile(file, Buffer.from(r.data, 'base64'));
  log('[shot]', file);
}

async function waitMs(ms) { return new Promise((r) => setTimeout(r, ms)); }

async function ensureLoggedIn(ws, baseUrl) {
  // Pre-seed sessionStorage so the admin boots into the app shell
  // without going through the login UI. We set the session token
  // and CSRF token that the apiFetch helpers expect.
  const seed = `
    (() => {
      try { sessionStorage.setItem('orvix_admin_token', 'mock-admin-token'); } catch (e) {}
      try { sessionStorage.setItem('orvix_admin_csrf',  'mock-csrf-token'); } catch (e) {}
      try { sessionStorage.setItem('orvix_admin_refresh', 'mock-refresh'); } catch (e) {}
    })();
  `;
  // First navigation: load admin, then seed.
  await cdpNavigate(ws, baseUrl);
  await waitMs(3000);
  await cdpEval(ws, seed);
  // Second navigation: reload so boot() picks up the session.
  await cdpNavigate(ws, baseUrl);
  await waitMs(3500);
  const result = await cdpEval(ws, `(() => {
    return {
      hasApp: !!document.getElementById('app-view') && !document.getElementById('app-view').classList.contains('hidden'),
      hasLogin: !!document.getElementById('login-view') && !document.getElementById('login-view').classList.contains('hidden'),
      title: document.title,
      appViewClass: document.getElementById('app-view') ? document.getElementById('app-view').className : null,
      loginViewClass: document.getElementById('login-view') ? document.getElementById('login-view').className : null,
      sidebarItems: document.querySelectorAll('.sidebar-link').length,
      token: (() => { try { return sessionStorage.getItem('orvix_admin_token'); } catch (e) { return null; } })(),
      hash: location.hash,
      readyState: document.readyState,
      hasAppView: !!document.getElementById('app-view'),
      bodyChildren: document.body ? document.body.children.length : 0,
    };
  })()`);
  err('[debug]', JSON.stringify(result, null, 2));
  // Dump first 500 chars of the document body to see what we actually have.
  const bodySample = await cdpEval(ws, 'document.body && document.body.innerHTML ? document.body.innerHTML.slice(0, 800) : "(no body)"');
  err('[debug-body]', bodySample);
  if (!result || !result.hasApp) fail('admin app did not boot');
}

async function navigateTo(ws, route) {
  await cdpEval(ws, `location.hash = '#/${route}'`);
  await waitMs(900); // give the page module time to render + fetch
}

async function openCreateModal(ws, selector) {
  await cdpEval(ws, `document.querySelector(${JSON.stringify(selector)})?.click()`);
  await waitMs(700);
  return cdpEval(ws, `!!document.querySelector('.modal-overlay .modal')`);
}

async function closeModal(ws) {
  await cdpEval(ws, `document.querySelector('.modal-overlay .icon-btn')?.click()`);
  await waitMs(400);
}

async function captureRoute(ws, route, name) {
  await navigateTo(ws, route);
  await waitMs(1500);
  // Validate the page actually rendered before capturing.
  const probe = await cdpEval(ws, `(() => {
    const root = document.getElementById('page-root');
    return {
      hash: location.hash,
      pageRootExists: !!root,
      pageRootChildren: root ? root.children.length : 0,
      pageRootHTML: root ? root.innerHTML.slice(0, 400) : null,
      pageInner: !!document.querySelector('.page-inner'),
      pageInnerOps: !!document.querySelector('.page-inner.ops-page'),
      pageOpsHero: !!document.querySelector('.ops-hero'),
    };
  })()`);
  if (!probe.pageInner || probe.pageRootChildren === 0) {
    err('[probe] route ' + route + ' rendered EMPTY:', JSON.stringify(probe));
  }
  await cdpScreenshot(ws, path.join(outDir, name + '.png'));
  // Also record any console errors that fired
  if (consoleErrors.length) {
    err('[console] errors after ' + route + ':', consoleErrors.length);
    for (const c of consoleErrors) err('  ', c);
    consoleErrors.length = 0;
  }
}

async function main() {
  // Reset out dir
  mkdirSync(outDir, { recursive: true });

  // Start the mock server
  const { server, baseUrl } = await startServer();
  log('[server]', baseUrl);

  // Launch Chrome
  const userDataDir = path.join(os.tmpdir(), 'orvix-screenshot-' + Date.now());
  mkdirSync(userDataDir, { recursive: true });
  const { proc, port } = await launchChrome(userDataDir);

  // Connect CDP
  const ws = await connectCDP(port);
  await new Promise((r) => ws.addEventListener('open', r, { once: true }));
  log('[cdp] connected');

  await cdpSend(ws, nextId(), 'Page.enable');
  await cdpSend(ws, nextId(), 'Runtime.enable');
  await cdpSend(ws, nextId(), 'Log.enable');
  await cdpSend(ws, nextId(), 'Network.enable');

  try {
    // Pre-login navigation
    await ensureLoggedIn(ws, baseUrl);

    // Capture each route
    log('[phase] capturing routes');
    await captureRoute(ws, 'dashboard',                 'dashboard');
    await captureRoute(ws, 'domains',                  'domains');
    await captureRoute(ws, 'runtime-listeners',        'runtime-listeners');
    await captureRoute(ws, 'security/antispam',        'antivirus');
    await captureRoute(ws, 'settings',                 'settings');
    await captureRoute(ws, 'security',                 'security');

    // Capture modals
    log('[phase] capturing modals');
    await navigateTo(ws, 'domains');
    await waitMs(800);
    const domainOk = await openCreateModal(ws, '.add-domain-btn');
    if (!domainOk) fail('domain create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'domain-create-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'admin/groups');
    await waitMs(800);
    const agOk = await openCreateModal(ws, '[data-ag-action="create"]');
    if (!agOk) fail('admin groups create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'admin-groups-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'security/spam');
    await waitMs(800);
    const aclOk = await openCreateModal(ws, '[data-acl-action="create"]');
    if (!aclOk) fail('ACL create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'acl-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'security/routing');
    await waitMs(800);
    const accOk = await openCreateModal(ws, '[data-acc-action="create"]');
    if (!accOk) fail('acceptance create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'acceptance-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'security/rules');
    await waitMs(800);
    const irrOk = await openCreateModal(ws, '[data-irr-action="create"]');
    if (!irrOk) fail('incoming rules create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'incoming-rules-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'domains/lists');
    await waitMs(800);
    const mlOk = await openCreateModal(ws, '[data-ml-action="create"]');
    if (!mlOk) fail('mailing list create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'mailing-list-modal.png'));
    await closeModal(ws);

    await navigateTo(ws, 'domains/public');
    await waitMs(800);
    const pfOk = await openCreateModal(ws, '[data-pf-action="create"]');
    if (!pfOk) fail('public folder create modal did not open');
    await cdpScreenshot(ws, path.join(outDir, 'public-folder-modal.png'));
    await closeModal(ws);

    log('[done] all screenshots captured');
  } finally {
    try { ws.close(); } catch (_) {}
    try { proc.kill(); } catch (_) {}
    try { server.close(); } catch (_) {}
    // best-effort: clean up the user-data-dir
    try { await fs.rm(userDataDir, { recursive: true, force: true }); } catch (_) {}
  }
}

main().catch((e) => {
  err('[fatal]', e && e.stack || e);
  process.exit(1);
});
