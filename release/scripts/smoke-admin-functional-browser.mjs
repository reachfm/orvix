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
            if (req.method === 'GET') return sendJSON(res, 200, { domains: [] });
            if (req.method === 'POST') return sendJSON(res, 201, { name: 'example.com', status: 'active' });
          }
          if (url.pathname === '/api/v1/mailboxes') {
            if (req.method === 'GET') return sendJSON(res, 200, { mailboxes: [] });
            if (req.method === 'POST') return sendJSON(res, 201, { id: 'mbox-1', email: 'admin@example.com' });
          }
          if (url.pathname === '/api/v1/admin/account-classes') return sendJSON(res, 200, { classes: [] });
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
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-dev-shm-usage',
    '--disable-extensions',
    'about:blank',
  ];
  if (process.platform !== 'win32') chromeArgs.splice(3, 0, '--no-sandbox');
  const proc = spawn(chrome, chromeArgs, { stdio: ['ignore', 'pipe', 'pipe'] });
  const cleanup = () => {
    try { proc.kill(); } catch {}
    try { server.close(); } catch {}
    try { rmSync(profile, { recursive: true, force: true }); } catch {}
  };
  process.on('exit', cleanup);
  try {
    await waitFor(() => fetchJSON(`http://127.0.0.1:${debugPort}/json/version`).catch(() => null), 'Chrome DevTools');
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
    await cdp.send('Page.navigate', { url: `http://127.0.0.1:${port}/admin/` });
    await new Promise((resolve) => cdp.on('Page.loadEventFired', resolve));

    const evalJS = async (expression) => {
      const res = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (res.exceptionDetails) fail(res.exceptionDetails.text || 'evaluation failed');
      return res.result && res.result.value;
    };
    const visible = (sel) => `(function(){const n=document.querySelector(${JSON.stringify(sel)});if(!n)return false;const s=getComputedStyle(n);const r=n.getBoundingClientRect();return s.display!=='none'&&s.visibility!=='hidden'&&r.width>=0&&r.height>=0;})()`;
    const mainText = () => evalJS(`document.querySelector('#page-root')?.innerText?.trim() || ''`);

    await waitFor(() => evalJS(visible('#login-email')), '#login-email visible');
    await waitFor(() => evalJS(visible('#login-password')), '#login-password visible');
    await waitFor(() => evalJS(visible('#login-button')), '#login-button visible');
    const emptyErrorHidden = await evalJS(`!(document.querySelector('#login-message')?.classList.contains('visible')) && !(document.querySelector('#login-message')?.textContent || '').trim()`);
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

    await evalJS(`location.hash = '#/domains'`);
    await waitFor(() => evalJS(`document.querySelector('.page[data-route="domains"] .page-title')?.textContent === 'Domains'`), 'domains route title');
    await waitFor(() => evalJS(visible('.page[data-route="domains"] .add-domain-btn')), 'Add Domain button');
    const domainsText = await mainText();
    if (!/Domains/.test(domainsText) || !/Add Domain/.test(domainsText) || domainsText.trim().length < 20) fail(`domains route blank or incomplete: ${domainsText}`);

    await evalJS(`location.hash = '#/accounts'`);
    await waitFor(() => evalJS(`document.querySelector('.page[data-route="accounts"] .page-title')?.textContent === 'Accounts'`), 'accounts route title');
    await waitFor(() => evalJS(visible('.page[data-route="accounts"] .add-mailbox-btn')), 'Add Mailbox button');
    const accountsText = await mainText();
    if (!/Accounts/.test(accountsText) || !/Add Mailbox/.test(accountsText) || accountsText.trim().length < 20) fail(`accounts route blank or incomplete: ${accountsText}`);

    await evalJS(`document.querySelector('.page[data-route="accounts"] .add-mailbox-btn').click()`);
    await waitFor(() => evalJS(visible('.modal-overlay .modal')), 'mailbox modal');
    await waitFor(() => evalJS(`document.body.innerText.includes('Create a domain first') || document.body.innerText.includes('No domains')`), 'no-domain mailbox guidance');

    if (!requests.includes('POST /api/v1/auth/login')) fail('login POST was not called');
    if (!requests.includes('GET /api/v1/domains')) fail('domains API was not called');
    if (!requests.includes('GET /api/v1/mailboxes')) fail('mailboxes API was not called');
    if (failures.length) fail(`browser errors:\n${failures.join('\n')}`);
    cdp.close();
  } finally {
    cleanup();
  }
  console.log('PASS admin functional browser smoke: login, dashboard, domains, accounts');
}

main().catch((err) => {
  console.error(`FAIL admin functional browser smoke: ${err && err.message || err}`);
  if (failures.length) console.error(failures.join('\n'));
  console.error(`Requests: ${requests.join(', ')}`);
  process.exit(1);
});
