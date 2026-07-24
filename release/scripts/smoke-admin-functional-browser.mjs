import http from 'node:http';
import https from 'node:https';
import fs from 'node:fs/promises';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';

const adminDir = path.resolve(process.argv[2] || 'release/admin');
const failures = [];
const requests = [];
const SMOKE_SESSION = 'admin-functional-session';
const SMOKE_CERT = `-----BEGIN CERTIFICATE-----
MIIDJTCCAg2gAwIBAgIUM5Q9uaK6lz1xFWj1N68005cDmV8wDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJMTI3LjAuMC4xMB4XDTI2MDcwOTE0MTIwMFoXDTM2MDcw
NjE0MTIwMFowFDESMBAGA1UEAwwJMTI3LjAuMC4xMIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAjBKQxRW4uZP4xXdGr0sYPTqN93F94fL+HotLz8fMDBI+
uCyOwllFTJIg78LkDq7UPSh954zW4eecsdJGG52jdGxOzcrUP13eYEw5LyU9KrTI
um80kyLcomWOPSePiFErCrrMlcHTlhO3UE+6M1Fu0A3FAKQIQZr6SuqBv/lGS2NE
oELx30pa1maPq9p9zpM/n1wwQNljBq2jX0Bq/OQwfeNdhFTpcFkiksq03XXMBjDR
RATyYRbe4hnGeRHwqsq5/y0u+ag3FRvvmSG5DvUt2UWmfbHlLT+APBq0JIWg+4AQ
J1vcYrYLTgugulnkgoyj478KpFzp6anUUGmKeBBnEQIDAQABo28wbTAdBgNVHQ4E
FgQUl7QTCrXR51S9jGGNKQCciJS4VAIwHwYDVR0jBBgwFoAUl7QTCrXR51S9jGGN
KQCciJS4VAIwDwYDVR0TAQH/BAUwAwEB/zAaBgNVHREEEzARhwR/AAABgglsb2Nh
bGhvc3QwDQYJKoZIhvcNAQELBQADggEBAC2Bpc8M7q2uo7N73+U1s7CmWbeTz9w4
vEBeY7JsLghkE9j5eBl0kUux7HkGnLd6mjDHQhgtpxAyvZV6vQCGEwD1kMFHU41U
pn01MjZQD77LGjN1wy0nccJFog1rMOW2O0iIm/Ph0SCDhgP26LSEVolUO7cOjb18
ZPtWe4pVxACGZzc7NQeRHzky7vWyOZQNWnGigQKQwD/6suK6ekztdvO1F/1VjLeq
v+CSK3PzRBAnAlaN5e8FLm0sG8933p0LSbbMAemGNCPPifh56Z6E/ENKkYxm9ua7
CoHIt7t6vnwV/IjprY9KEkMyzb9rTgDeJ4qqJ3m4pSk8CbKpXXYSaeM=
-----END CERTIFICATE-----`;
const SMOKE_KEY = `-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCMEpDFFbi5k/jF
d0avSxg9Oo33cX3h8v4ei0vPx8wMEj64LI7CWUVMkiDvwuQOrtQ9KH3njNbh55yx
0kYbnaN0bE7NytQ/Xd5gTDkvJT0qtMi6bzSTItyiZY49J4+IUSsKusyVwdOWE7dQ
T7ozUW7QDcUApAhBmvpK6oG/+UZLY0SgQvHfSlrWZo+r2n3Okz+fXDBA2WMGraNf
QGr85DB9412EVOlwWSKSyrTddcwGMNFEBPJhFt7iGcZ5EfCqyrn/LS75qDcVG++Z
IbkO9S3ZRaZ9seUtP4A8GrQkhaD7gBAnW9xitgtOC6C6WeSCjKPjvwqkXOnpqdRQ
aYp4EGcRAgMBAAECggEAQkjC01DtInyge6lu/KLXrJnZ9p9xR4w6ru+SB5hvucKk
hXkocVXXUl3QUkVysHQRIYPY2MswIKT+5LMx0/2sDPr367Cw8e+UvRM0+Fdx85Sr
bHYVdg9IQ101i0D+Ti7C5IfzKmcXnmxkEhA4d/JwMuphMGVvNsSE7xC8J8Fpf2Ce
Ly2NzEX70GWKtBFdwMefK6n+IPRfc8dY4m6u+E5rsdj0V885dguJhKpanQkSe+qR
tanhRQSsxnjQg4iK8+HQFI+sHfbTs5BDmZ2FaFW1XWBIDgbMbUIfi79bMTqkqk9t
QtqJetLrmeZ1EiQjz/0hN0+N/MyT/8R6BL8/8BTTBQKBgQC91/XqmfkVaIGlWVOA
oRJEckkgpzkpS5U7itX0p+zEUClsJ51noaxm/ElhVxG0uSxTEvNcVtd+T+xwpx8L
tpXWxWc6MCehs9uFyc7j+X24shPCu4u4GqvyRwwS3YsYJJ5DWapGaMWsqdy26+jn
BK3C0l/Z+uSgjmodrTlVVgkvOwKBgQC84oDPShtgTZlo3mBPM9k5KFOjbqX1h7IR
Wzmw0vhDuHj8W/gfMhOhR2CrM7+dH+Oia1EKLuHHrZPXTId5IxYffUxHGuKG8pc3
pcstEAagwNL/3N7YI/bb6BLOrDkDtaPWFFBAySz6z8iZBZ0HZWmnY6nzFoZnUVse
13qgGgG2IwKBgHvFmWdjC6qRgDU1j+OFIEvP1y2a2QG7bYhsdCIWeZ9kRB1nlpBC
MAzU32K/SaPyNpvS9yd01vpbUWQBEZSpbfegrDSbwLsEcFNBx8mKmBUaxRdo/ycA
/Knw+EY0esM63JQ8mW9eT8LK3EPGewpjWoZyclvD39tt/nFqxr6EYWiRAoGAatKO
lq0KnoREZpKdVS21hCXSZ3OEWD/N7RLypZYq4eHKSq6YvMvNXkDH4wr5KxuF2a1n
v6KT/iGkovadB11YfaaXJP+HbVp1OvuA1JNjrDZhHmMDhKmSSvwM5uVvuTFY3xHN
8VXVImOwxxntnOk1v30V+GycxoG0TtT+fN04apECgYADV/SBlAS2dTBfyUIp5Dr+
IQhmS1iqFDSm+WJ6tm4zNnYKgjYY9UzPLvPmRUN9ptulWudVyT18qnuriBRcu3XZ
jP21J0VEXnwJ1bo+QaZZP3LKQwppnH2Mx3ozm7dttPf3eF417PcNG6/bT7huz0JR
5CLINSMZH6rZALFyHimdhQ==
-----END PRIVATE KEY-----`;

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

function sendJSON(res, status, body, headers = {}) {
  res.writeHead(status, {
    'content-type': 'application/json',
    'cache-control': 'no-store',
    ...headers,
  });
  res.end(JSON.stringify(body));
}

function startServer() {
  return new Promise((resolve, reject) => {
    const server = https.createServer({ key: SMOKE_KEY, cert: SMOKE_CERT }, async (req, res) => {
      try {
        const url = new URL(req.url, 'https://127.0.0.1');
        const pathname = url.pathname;
        requests.push(`${req.method} ${pathname}`);
        // web/admin/src/api.ts uses BASE = "/api/v1" — the React app never
        // requests /admin/api/...; no path normalization needed here.
        if (pathname.startsWith('/api/')) {
          if (pathname === '/api/v1/me') {
            const cookie = req.headers.cookie || '';
            if (cookie.includes(`__Host-orvix_session=${SMOKE_SESSION}`)) {
              return sendJSON(res, 200, { email: 'admin@example.com', roles: ['admin'], role: 'admin' });
            }
            return sendJSON(res, 401, { error: 'unauthorized' });
          }
          if (pathname === '/api/v1/auth/login' && req.method === 'POST') {
            const body = await readBody(req);
            // Match the real backend contract (internal/api/handlers/handlers.go
            // Login): success sets the session cookie; failure returns
            // {"error": "invalid credentials"} — the exact field web/admin/src/api.ts
            // reads to build the thrown Error's .message.
            if (body.email === 'admin@example.com' && body.password === 'correct horse battery staple') {
              return sendJSON(res, 200, { status: 'ok' }, {
                'set-cookie': `__Host-orvix_session=${SMOKE_SESSION}; Path=/; HttpOnly; Secure; SameSite=Lax`,
              });
            }
            return sendJSON(res, 401, { error: 'invalid credentials' });
          }
          if (pathname === '/api/v1/auth/logout' && req.method === 'POST') {
            return sendJSON(res, 200, { status: 'ok' }, { 'set-cookie': '__Host-orvix_session=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0' });
          }
          // Generic 200 for any other API call the authenticated shell makes
          // while rendering its default (Dashboard) tab — the functional
          // smoke only asserts the login/logout contract and that the
          // authenticated shell renders, not every page's data.
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
    '--window-size=1440,900',
    '--no-sandbox',
    '--disable-gpu',
    '--disable-software-rasterizer',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-timer-throttling',
    '--disable-backgrounding-occluded-windows',
    '--disable-features=TranslateUI',
    '--force-prefers-color-scheme=dark',
    '--enable-features=WebContentsForceDark',
    '--disable-dev-shm-usage',
    '--disable-extensions',
    '--disable-setuid-sandbox',
    '--disable-dbus',
    '--remote-allow-origins=*',
    '--ignore-certificate-errors',
    '--allow-insecure-localhost',
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
    // Track document/script/style response status codes so a 404 or wrong
    // MIME type fails loudly instead of surfacing only as a later timeout.
    // Registered before navigation so the initial document/asset requests
    // are not missed.
    const assetStatuses = new Map();
    cdp.on('Network.responseReceived', (p) => {
      const r = p.response || {};
      const type = p.type; // Document, Script, Stylesheet, ...
      if (['Document', 'Script', 'Stylesheet'].includes(type)) {
        assetStatuses.set(r.url, { status: r.status, mimeType: r.mimeType });
      }
    });
    await cdp.send('Network.enable');
    await cdp.send('Page.navigate', { url: `https://127.0.0.1:${port}/admin/` });
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
    const isVisible = (sel) => `(() => {
      const el = document.querySelector(${JSON.stringify(sel)});
      if (!el) return false;
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.display !== 'none';
    })()`;
    const bodyText = () => evalJS(`document.body?.innerText || ''`);
    // React's controlled <input> tracks value changes via its own value
    // setter; a plain `el.value = x; dispatchEvent(new Event('input'))`
    // is invisible to React because it bypasses that tracked setter. Use
    // the native HTMLInputElement setter directly so React's onChange fires.
    const setInputValue = (sel, value) => `(() => {
      const el = document.querySelector(${JSON.stringify(sel)});
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event('input', { bubbles: true }));
    })()`;

    // 1. /admin/ loads successfully — CDP already confirmed via
    // Page.loadEventFired above. Verify document/script/style requests
    // resolved 2xx with sane MIME types.
    for (const [reqUrl, info] of assetStatuses) {
      if (info.status < 200 || info.status >= 300) {
        fail(`asset did not load with 2xx: ${reqUrl} -> ${info.status}`);
      }
    }
    if (assetStatuses.size < 2) fail(`expected document + script/style asset responses to be tracked, got ${assetStatuses.size}`);

    // 2. Unauthenticated /api/v1/me (401, no cookie) must produce a visible
    // login screen: email input, password input, submit button.
    await waitFor(() => evalJS(isVisible('#login-email')), 'visible email input', 15000);
    await waitFor(() => evalJS(isVisible('#login-password')), 'visible password input');
    await waitFor(() => evalJS(isVisible('#login-button')), 'visible submit button');

    // 3. Invalid credentials: submit, expect the API call, expect a visible
    // error state (no page error, no silent failure).
    await evalJS(setInputValue('#login-email', 'admin@example.com'));
    await evalJS(setInputValue('#login-password', 'wrong-password'));
    await evalJS(`document.querySelector('#login-button').click()`);
    await waitFor(() => evalJS(isVisible('#login-error')), 'visible login error after invalid credentials');
    if (!requests.includes('POST /api/v1/auth/login')) fail('invalid-credentials login POST was not sent');

    // 4. Valid mocked credentials: submit, expect the authenticated shell.
    // LoginPage's onSuccess does window.location.reload() — a legitimate
    // React pattern (re-run the /api/v1/me auth check on a fresh load)
    // rather than in-place SPA state toggling, so we simply poll for the
    // authenticated shell to appear after the reload completes.
    await evalJS(setInputValue('#login-email', 'admin@example.com'));
    await evalJS(setInputValue('#login-password', 'correct horse battery staple'));
    await evalJS(`document.querySelector('#login-button').click()`);
    await waitFor(async () => {
      const got = await cdp.send('Network.getCookies', { urls: [`https://127.0.0.1:${port}/admin/`] });
      const cookie = (got.cookies || []).find((c) => c.name === '__Host-orvix_session');
      return cookie && cookie.secure === true && cookie.httpOnly === true && cookie.path === '/' && !cookie.domain.startsWith('.');
    }, 'secure __Host-orvix_session cookie', 15000);

    // 5. At least one stable authenticated navigation element is visible
    // (the sidebar <nav> with the Dashboard entry). Reconnect Runtime.evaluate
    // after the page's own full reload — CDP automatically targets the
    // current execution context, so waitFor's retry loop naturally rides
    // past the reload without a separate load-event handshake.
    await waitFor(() => evalJS(exists('nav')), 'authenticated nav element', 15000);
    const navText = await evalJS(`document.querySelector('nav')?.innerText || ''`);
    if (!/dashboard/i.test(navText)) fail(`authenticated nav did not render expected content: ${navText.slice(0, 120)}`);
    if (!requests.includes('POST /api/v1/auth/login')) fail('valid-credentials login POST was not sent');

    // 6. No uncaught page errors (console.error / Runtime.exceptionThrown).
    if (failures.length) fail(`browser errors:\n${failures.join('\n')}`);

    // 7. Logout returns to login.
    await evalJS(`document.querySelectorAll('button').forEach(b => { if (/logout/i.test(b.textContent || '')) b.click(); })`);
    await waitFor(() => evalJS(isVisible('#login-email')), 'login screen after logout', 15000);
    if (!requests.includes('POST /api/v1/auth/logout')) fail('logout POST was not sent');

    // Banned-string DOM check: scan rendered page text for placeholder
    // strings that should never appear in production UI.
    const BANNED_RE = /coming soon|future release|not implemented|will be added later|unavailable in this build|fake|mock/i;
    const pageText = await bodyText();
    if (BANNED_RE.test(pageText)) {
      fail(`BANNED_STRING_IN_DOM: production UI rendered a forbidden placeholder string`);
    }
    cdp.close();
  } finally {
    cleanup();
  }
  console.log('PASS admin functional browser smoke: login (invalid + valid), authenticated shell, logout');
}

main().catch((err) => {
  const msg = (err && err.message) || String(err) || 'unknown error';
  console.error(`FAIL admin functional browser smoke: ${msg}`);
  if (err && err.stack) console.error(err.stack.split('\n').slice(0, 5).join('\n'));
  if (failures.length) console.error(`Browser failures:\n${failures.join('\n')}`);
  console.error(`Requests: ${requests.join(', ')}`);
  process.exit(1);
});
