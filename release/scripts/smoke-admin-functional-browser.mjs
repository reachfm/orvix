import http from 'node:http';
import fs from 'node:fs/promises';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';

const adminDir = path.resolve(process.argv[2] || 'release/admin');
const failures = [];
const requests = [];

function fail(msg) {
  throw new Error(msg);
}

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
  if (file.endsWith('.js')) return 'text/javascript; charset=utf-8';
  if (file.endsWith('.css')) return 'text/css; charset=utf-8';
  if (file.endsWith('.svg')) return 'image/svg+xml';
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
  });
  res.end(JSON.stringify(body));
}

function startServer() {
  return new Promise((resolve, reject) => {
    const server = http.createServer(async (req, res) => {
      try {
        const url = new URL(req.url, 'http://127.0.0.1');
        requests.push(`${req.method} ${url.pathname}`);
        if (url.pathname.startsWith('/api/')) {
          if (url.pathname === '/api/v1/me') {
            const auth = req.headers.authorization || '';
            if (auth === 'Bearer admin-functional-token') return sendJSON(res, 200, { email: 'admin@example.com', roles: ['admin'] });
            return sendJSON(res, 401, { code: 'unauthorized', message: 'unauthorized' });
          }
          if (url.pathname === '/api/v1/auth/login' && req.method === 'POST') {
            const body = await readBody(req);
            if (body.email && body.password) return sendJSON(res, 200, {
              access_token: 'admin-functional-token',
              refresh_token: 'admin-functional-refresh',
              token_type: 'Bearer',
            });
            return sendJSON(res, 401, { code: 'invalid_credentials', message: 'invalid credentials' });
          }
          if (url.pathname === '/api/v1/csrf-token') return sendJSON(res, 200, { csrf_token: 'csrf-functional' });
          if (url.pathname === '/api/v1/health') return sendJSON(res, 200, { status: 'ok' });
          if (url.pathname === '/api/v1/domains') {
            if (req.method === 'GET') return sendJSON(res, 200, { domains: [{
              domain: 'example.com', name: 'example.com',
              status: 'active', plan: 'smb',
              max_mailboxes: 50, max_aliases: 20, max_quota_mb: 1024,
              dkim_enabled: true, dkim_selector: 'default',
              dmarc_enabled: true, mtasts_enabled: false,
              mailbox_count: 1, updated_at: '2026-01-01T00:00:00Z',
            }] });
            if (req.method === 'POST') {
              const body = await readBody(req);
              // Echo every advanced field the new modal sends so
              // the modal can be honest about what it persisted.
              return sendJSON(res, 201, {
                domain: body.name || 'example.com',
                name: body.name || 'example.com',
                status: body.status || 'active',
                plan: body.plan || 'smb',
                description: body.description || '',
                max_mailboxes: body.max_mailboxes || 0,
                max_aliases: body.max_aliases || 0,
                max_quota_mb: body.max_quota_mb || 0,
                dkim_enabled: !!body.dkim_enabled,
                dkim_selector: body.dkim_selector || '',
                dmarc_enabled: !!body.dmarc_enabled,
                mtasts_enabled: !!body.mtasts_enabled,
                catchall_address: body.catchall_address || '',
                abuse_contact: body.abuse_contact || '',
                mailbox_count: 0,
              });
            }
          }
          if (url.pathname.startsWith('/api/v1/domains/')) {
            const parts = url.pathname.split('/');
            const dn = decodeURIComponent(parts[parts.length - 1] || 'example.com');
            if (req.method === 'GET') return sendJSON(res, 200, {
              domain: dn, name: dn,
              status: 'active', plan: 'smb',
              description: 'smoke test fixture',
              max_mailboxes: 50, max_aliases: 20, max_quota_mb: 1024,
              mailbox_count: 1,
              dkim_enabled: true, dkim_selector: 'default',
              dmarc_enabled: true, mtasts_enabled: false,
              catchall_address: '', abuse_contact: '',
              created_at: '2026-01-01T00:00:00Z',
              updated_at: '2026-01-01T00:00:00Z',
              mailboxes: [{ mailbox_id: 1, email: 'admin@example.com', status: 'active', is_admin: true }],
            });
            if (req.method === 'PATCH') {
              const body = await readBody(req);
              return sendJSON(res, 200, { applied: Object.keys(body || {}), domain: dn });
            }
            if (req.method === 'DELETE') return sendJSON(res, 204, '');
          }
          if (url.pathname === '/api/v1/mailboxes') {
            if (req.method === 'GET') return sendJSON(res, 200, { mailboxes: [] });
            if (req.method === 'POST') return sendJSON(res, 201, { id: 'mbox-1', email: 'admin@example.com' });
          }
          if (url.pathname === '/api/v1/admin/account-classes') return sendJSON(res, 200, { classes: [] });
          if (url.pathname === '/api/v1/admin/users') return sendJSON(res, 200, { users: [] });
          if (url.pathname === '/api/v1/admin/audit-logs') return sendJSON(res, 200, { logs: [] });
          if (url.pathname === '/api/v1/admin/runtime') return sendJSON(res, 200, {
            hostname: 'mock-host', version: '1.0.0', status: 'ok', listeners: [],
          });
          if (url.pathname === '/api/v1/admin/summary') return sendJSON(res, 200, {
            mail: { domain_count: 1, mailbox_count: 1, active_count: 1, suspended_count: 0 },
            status: 'ok', version: '1.0.0',
          });
          if (url.pathname === '/api/v1/admin/settings') return sendJSON(res, 200, {
            general:   { hostname: 'mock-host', primary_domain: 'example.com', version: '1.0.0' },
            build:     { version: '1.0.0', commit: 'mock-commit', channel: 'stable', go_version: 'go1.22' },
            security:  { password_min_len: 8, session_ttl_seconds: 3600, refresh_ttl_seconds: 86400 },
            backup:    { dir: '/var/backups/orvix/', retention_count: 7 },
            dns:       { public_ipv4: '127.0.0.1', public_ipv6: '' },
            mail_listeners: { smtp_port: 25, submission_port: 587, imap_port: 143 },
            mutable_fields: [],
            _settings_persistence: { enabled: false, note: 'mock' },
          });
          if (url.pathname === '/api/v1/admin/mfa/status') return sendJSON(res, 200, { enabled: false });
          if (url.pathname === '/api/v1/license') return sendJSON(res, 200, {
            mode: 'community', tier: 'community', public_key_present: true, valid: true, expired: false,
          });
          if (url.pathname.startsWith('/api/v1/admin/dns/') && url.pathname.endsWith('/plan')) {
            return sendJSON(res, 200, { records: [
              { type: 'MX', host: '@', value: '10 mail.example.com.', priority: 10 },
              { type: 'TXT', host: '@', value: 'v=spf1 mx -all' },
            ] });
          }
          if (url.pathname.startsWith('/api/v1/domains/') && url.pathname.endsWith('/audit')) {
            return sendJSON(res, 200, { entries: [] });
          }
          return sendJSON(res, 200, {});
        }
        let rel = url.pathname;
        if (rel === '/' || rel === '/admin') rel = '/admin/';
        if (rel.startsWith('/admin/')) rel = rel.slice('/admin/'.length);
        else rel = rel.replace(/^\/+/, '');
        if (!rel) rel = 'index.html';
        const full = path.resolve(adminDir, rel);
        if (!full.startsWith(adminDir)) {
          res.writeHead(403); res.end('forbidden'); return;
        }
        const data = await fs.readFile(full);
        res.writeHead(200, { 'content-type': contentType(full), 'cache-control': 'no-store' });
        res.end(data);
      } catch (err) {
        res.writeHead(404, { 'content-type': 'text/plain' });
        res.end(String(err && err.message || err));
      }
    });
    server.listen(0, '127.0.0.1', () => resolve(server));
    server.on('error', reject);
  });
}

function fetchJSON(url) {
  return new Promise((resolve, reject) => {
    http.get(url, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8'))); }
        catch (e) { reject(e); }
      });
    }).on('error', reject);
  });
}

class CDP {
  constructor(wsURL) {
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    this.ws = new WebSocket(wsURL);
  }
  async open() {
    await new Promise((resolve, reject) => {
      this.ws.addEventListener('open', resolve, { once: true });
      this.ws.addEventListener('error', reject, { once: true });
    });
    this.ws.addEventListener('message', (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(msg.error.message || JSON.stringify(msg.error)));
        else resolve(msg.result || {});
        return;
      }
      if (msg.method && this.listeners.has(msg.method)) {
        for (const fn of this.listeners.get(msg.method)) fn(msg.params || {});
      }
    });
  }
  on(method, fn) {
    if (!this.listeners.has(method)) this.listeners.set(method, []);
    this.listeners.get(method).push(fn);
  }
  send(method, params = {}) {
    const id = this.nextId++;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  close() {
    try { this.ws.close(); } catch {}
  }
}

async function waitFor(fn, label, timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const out = await fn();
      if (out) return out;
      last = String(out);
    } catch (err) {
      last = err && err.message || String(err);
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  fail(`timed out waiting for ${label}${last ? ` (${last})` : ''}`);
}

async function main() {
  if (!existsSync(path.join(adminDir, 'index.html'))) fail(`admin index not found: ${adminDir}`);
  const chrome = findChrome();
  if (!chrome) fail('Chrome/Chromium not found; set CHROME=/path/to/chrome');
  const server = await startServer();
  const port = server.address().port;
  const debugPort = 41000 + Math.floor(Math.random() * 10000);
  const profile = mkdtempSync(path.join(os.tmpdir(), 'orvix-admin-func-'));
  const chromeArgs = [
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profile}`,
    '--headless',
    '--no-sandbox',
    '--disable-gpu',
    '--disable-software-rasterizer',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-dev-shm-usage',
    '--disable-extensions',
    '--disable-setuid-sandbox',
    '--disable-dbus',
    'about:blank',
  ];
  const proc = spawn(chrome, chromeArgs, {
    stdio: ['ignore', 'inherit', 'inherit'],
    env: { ...process.env, DBUS_SESSION_BUS_ADDRESS: '/dev/null' },
  });
  proc.on('exit', (code, signal) => {
    if (code !== 0 || signal) console.error(`Chrome exited: code=${code} signal=${signal}`);
  });
  const cleanup = () => {
    try { proc.kill(); } catch {}
    try { server.close(); } catch {}
    try { rmSync(profile, { recursive: true, force: true }); } catch {}
  };
  process.on('exit', cleanup);
  try {
    await waitFor(() => fetchJSON(`http://127.0.0.1:${debugPort}/json/version`).catch(() => null), 'Chrome DevTools', 30000);
    const targets = await waitFor(async () => {
      const list = await fetchJSON(`http://127.0.0.1:${debugPort}/json/list`).catch(() => []);
      return list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl);
    }, 'Chrome page target');
    const cdp = new CDP(targets.webSocketDebuggerUrl);
    await cdp.open();
    cdp.on('Runtime.exceptionThrown', (p) => failures.push(`exception: ${p.exceptionDetails?.text || p.exceptionDetails?.exception?.description || 'unknown'}`));
    cdp.on('Runtime.consoleAPICalled', (p) => {
      if (p.type === 'error') failures.push(`console.error: ${(p.args || []).map((a) => a.value || a.description || '').join(' ')}`);
    });
    cdp.on('Log.entryAdded', (p) => {
      const text = p.entry && p.entry.text || '';
      if (!p.entry || p.entry.level !== 'error') return;
      if (text.includes('/api/v1/me') && text.includes('401')) return;
      if (text.includes('/favicon.ico') && text.includes('404')) return;
      if (text.includes('Failed to load resource') && (text.includes('401') || text.includes('404'))) return;
      failures.push(`log.error: ${text}`);
    });
    await cdp.send('Runtime.enable');
    await cdp.send('Page.enable');
    await cdp.send('Log.enable');
    await cdp.send('Console.enable');
    // Capture all console messages from the browser for diagnostics
    cdp.on('Console.messageAdded', (p) => {
      const msg = p.message || {};
      if (msg.level === 'error' || msg.level === 'warning') {
        console.error(`browser ${msg.level}: ${(msg.text || '')} ${(msg.url || '')}:${msg.line || 0}`);
      }
    });
    await cdp.send('Page.navigate', { url: `http://127.0.0.1:${port}/admin/` });
    await new Promise((resolve) => cdp.on('Page.loadEventFired', resolve));
    // After load event, wait a moment for deferred module scripts to execute
    await new Promise((r) => setTimeout(r, 500));

    const evalJS = async (expression) => {
      const res = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (res.exceptionDetails) {
        const ed = res.exceptionDetails;
        const errMsg = ed.text || ed.exception?.description || JSON.stringify(ed);
        console.error(`CDP eval error for: ${expression.slice(0, 120)}`);
        console.error(`  exceptionDetails: ${errMsg}`);
        if (ed.stackTrace) console.error(`  stack: ${(ed.stackTrace.callFrames || []).map(f => `${f.url}:${f.lineNumber}:${f.columnNumber}`).join(' > ')}`);
        fail(`evaluation failed: ${errMsg}`);
      }
      return res.result && res.result.value;
    };
    const exists = (sel) => `!!document.querySelector(${JSON.stringify(sel)})`;
    const mainText = () => evalJS(`document.querySelector('#page-root')?.innerText?.trim() || ''`);

    // Verify CDP evaluation works at all
    const docType = await evalJS(`typeof document`);
    console.error(`bootstrap: typeof document = ${docType}`);
    const loginView = await evalJS(`document.querySelector('#login-view')?.classList?.value || 'no-login-view'`);
    console.error(`bootstrap: login-view classes = ${loginView}`);

    await waitFor(() => evalJS(exists('#login-email')), '#login-email visible', 15000);
    await waitFor(() => evalJS(exists('#login-password')), '#login-password visible');
    await waitFor(() => evalJS(exists('#login-button')), '#login-button visible');

    // Login card dimensions must be enterprise-ready
    const cardWidth = await evalJS(`document.querySelector('.login-card')?.getBoundingClientRect()?.width || 0`);
    if (cardWidth < 380) fail(`LOGIN_CARD_TOO_NARROW: login card is ${Math.round(cardWidth)}px wide (min 380px required)`);
    console.error(`login card width: ${Math.round(cardWidth)}px`);

    const inputWidth = await evalJS(`document.querySelector('#login-email')?.getBoundingClientRect()?.width || 0`);
    const emailFull = await evalJS(`document.querySelector('#login-email')?.offsetWidth || 0`);
    if (inputWidth < 300 && cardWidth > 400) fail(`LOGIN_INPUT_TOO_NARROW: email input is ${Math.round(inputWidth)}px wide on ${Math.round(cardWidth)}px card`);
    console.error(`login email input width: ${Math.round(inputWidth)}px`);

    const btnWidth = await evalJS(`document.querySelector('#login-button')?.getBoundingClientRect()?.width || 0`);
    const btnFull = await evalJS(`document.querySelector('#login-button')?.offsetWidth || 0`);
    if (btnWidth < inputWidth - 10) fail(`LOGIN_BUTTON_NOT_FULL_WIDTH: button is ${Math.round(btnWidth)}px, input is ${Math.round(inputWidth)}px`);
    console.error(`login button width: ${Math.round(btnWidth)}px`);

    // Set email value and verify it fits without clipping
    await evalJS(`document.querySelector('#login-email').value = 'admin@orvix.email'`);
    const emailScroll = await evalJS(`document.querySelector('#login-email')?.scrollWidth || 0`);
    if (emailScroll > inputWidth + 5) fail(`LOGIN_EMAIL_CLIPPED: email scrollWidth ${Math.round(emailScroll)}px exceeds input ${Math.round(inputWidth)}px`);
    console.error(`login email scrollWidth: ${Math.round(emailScroll)}px vs inputWidth: ${Math.round(inputWidth)}px`);

    const emptyErrorHidden = await evalJS(`!!document.querySelector('#login-message') && document.querySelector('#login-message').style.display === 'none'`);
    if (!emptyErrorHidden) fail('empty login error alert is visible before submit');

    await evalJS(`
      document.querySelector('#login-email').value='admin@example.com';
      document.querySelector('#login-password').value='correct horse battery staple';
      document.querySelector('#login-form').requestSubmit();
    `);
    await waitFor(() => evalJS(`!document.querySelector('#app-view')?.classList.contains('hidden')`), 'app shell after login');
    await waitFor(async () => (await mainText()).length > 10, 'nonblank dashboard');
    const dashText = await mainText();
    if (!/dashboard/i.test(dashText)) fail(`dashboard did not render expected content: ${dashText.slice(0, 120)}`);
    if (dashText.includes('forEach is not a function') || dashText.includes('TypeError')) fail(`dashboard has JS error in rendered text: ${dashText.slice(0, 200)}`);

    const navigateRoute = async (route, routeName) => {
      await evalJS(`location.hash = '#/${route}'`);
      await waitFor(async () => (await mainText()).trim().length > 10, `${routeName} route content`);
      const text = await mainText();
      if (text.trim().length < 10) fail(`${routeName} route blank: "${text.slice(0, 80)}"`);
    };

    await navigateRoute('domains', 'Domains');
    const addDomainBtn = await evalJS(exists('.add-domain-btn'));
    if (!addDomainBtn) fail('Domains Add Domain button not visible');
    await evalJS(`document.querySelector('.add-domain-btn').click()`);
    await waitFor(() => evalJS(exists('.modal-overlay .modal')), 'Domains add modal');
    const domainModalInputs = await evalJS(`document.querySelectorAll('.modal-overlay input, .modal-overlay select').length`);
    // ADMIN-CONSOLE-FINAL-POLISH: a "Domain only" modal with one
    // input is no longer acceptable. The new modal wires every
    // advanced field the backend persists: status, plan, description,
    // max_mailboxes, max_aliases, max_quota_mb, dkim_enabled,
    // dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address,
    // abuse_contact.
    if (domainModalInputs < 6) fail(`WEAK_DOMAIN_MODAL: Domains add modal has only ${domainModalInputs} inputs — every advanced field must be exposed`);
    await evalJS(`document.querySelector('.modal-overlay .btn.ghost')?.click()`);
    await waitFor(() => evalJS(`!document.querySelector('.modal-overlay')`), 'Domains modal close');

    await navigateRoute('accounts', 'Accounts');
    const addAcctBtn = await evalJS(exists('.add-mailbox-btn'));
    if (!addAcctBtn) fail('Accounts Add Mailbox button not visible');
    await evalJS(`document.querySelector('.add-mailbox-btn').click()`);
    await waitFor(() => evalJS(exists('.modal-overlay .modal')), 'Accounts add modal');
    const acctModalInputs = await evalJS(`document.querySelectorAll('.modal-overlay input, .modal-overlay select').length`);
    if (acctModalInputs < 2) fail(`EMPTY_MODAL: Accounts add modal has too few inputs (${acctModalInputs})`);
    await evalJS(`document.querySelector('.modal-overlay .btn.ghost')?.click()`);
    await waitFor(() => evalJS(`!document.querySelector('.modal-overlay')`), 'Accounts modal close');

    await navigateRoute('dns', 'DNS & DKIM');
    await navigateRoute('queue', 'Queue');
    await navigateRoute('logs', 'Logs');
    await navigateRoute('monitoring', 'Monitoring');
    await navigateRoute('updates', 'Updates');
    await navigateRoute('settings', 'Settings');
    // ADMIN-CONSOLE-FINAL-POLISH: the Settings page now renders a
    // polished runtime overview. Old weak copy
    // ("no mutable settings in this build") must never render.
    const settingsText = await mainText();
    if (/no mutable settings in this build/i.test(settingsText)) {
      fail('WEAK_SETTINGS_COPY: Settings page still renders the deprecated "no mutable settings in this build" copy');
    }
    if (!/Listener bindings|Runtime/i.test(settingsText)) {
      fail('WEAK_SETTINGS_OVERVIEW: Settings page did not render a runtime overview (Listener bindings / Runtime)');
    }
    await navigateRoute('services', 'Services');
    await navigateRoute('license', 'License');
    await navigateRoute('backups', 'Backups');
    await navigateRoute('admin/users', 'Admin Users');      // B-1 regression guard
    await navigateRoute('admin/audit-log', 'Audit Log');   // audit-log accessible at its own route
    await navigateRoute('runtime-listeners', 'Runtime Listeners');

    const sidebarLinks = await evalJS(`Array.from(document.querySelectorAll('.sidebar-link')).map(a => a.getAttribute('data-route')).join(',')`);
    const hiddenRoutes = ['migration', 'migration/sources', 'clustering', 'clustering/imap', 'clustering/pop3', 'clustering/webmail'];
    for (const hr of hiddenRoutes) {
      if (sidebarLinks.includes(hr)) fail(`HIDDEN route '${hr}' still appears in sidebar`);
    }

    if (!requests.includes('POST /api/v1/auth/login')) fail('login POST was not called');
    if (!requests.includes('GET /api/v1/domains')) fail('domains API was not called');
    if (!requests.includes('GET /api/v1/mailboxes')) fail('mailboxes API was not called');
    if (!requests.includes('GET /api/v1/admin/settings')) fail('admin settings API was not called');
    if (!requests.includes('GET /api/v1/admin/runtime')) fail('admin runtime API was not called');
    if (failures.length) fail(`browser errors:\n${failures.join('\n')}`);

    // Banned-string DOM check: after login, scan rendered page text for
    // placeholder strings that should never appear in production UI.
    const BANNED_RE = /coming soon|future release|not implemented|will be added later|unavailable in this build|fake|mock/i;
    const pageText = await evalJS(`document.body?.innerText || ''`);
    if (BANNED_RE.test(pageText)) {
      fail(`BANNED_STRING_IN_DOM: production UI rendered a forbidden placeholder string`);
    }
    cdp.close();
  } finally {
    cleanup();
  }
  console.log('PASS admin functional browser smoke: login, dashboard, v1 routes, no empty modals');
}

main().catch((err) => {
  const msg = (err && err.message) || String(err) || 'unknown error';
  console.error(`FAIL admin functional browser smoke: ${msg}`);
  if (err && err.stack) console.error(err.stack.split('\n').slice(0, 5).join('\n'));
  if (failures.length) console.error(`Browser failures:\n${failures.join('\n')}`);
  console.error(`Requests: ${requests.join(', ')}`);
  process.exit(1);
});
