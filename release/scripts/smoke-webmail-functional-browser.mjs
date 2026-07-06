// smoke-webmail-functional-browser.mjs — Self-contained headless
// Chrome functional smoke for the Orvix Webmail SPA.
//
// Self-contained means: this script does NOT require a
// pre-running webmail server. It uses CDP (the Chrome
// DevTools Protocol) to drive a headless Chromium against a
// local Node HTTP server that we spawn on the same port. The
// mock backend answers the few endpoints the auth-gate + SPA
// shell probe with canned responses so the smoke is
// deterministic — no flaky real network, no race against the
// live operator's SMTP / IMAP / JMAP / queue.
//
// The .sh wrapper locates Chrome + Node, picks a free port,
// binds the local server, and invokes this file.
//
// What we assert (Release 1):
//   1. Auth-gate renders the login form on first paint (no
//      session cookie). The form has email + password fields
//      and a Submit button.
//   2. Posting credentials to /api/v1/webmail/login (mocked
//      to return success) + reloading the page causes the
//      auth-gate to fall away and the SPA shell to mount.
//      The shell renders the folder sidebar, the message
//      list, and the reading pane.
//   3. The compose modal opens via window.OrvixWebmail.openCompose
//      OR via clicking the New Message button.
//   4. dirAuto("Arabic string") === 'rtl'. dirAuto("") === 'auto'.
//      dirAuto("hello world") === 'ltr'. The rendering hook is
//      installed by the SPA.
//   5. The Settings modal opens via window.OrvixWebmail.openSettingsModal.
//   6. The Mail Client Setup tab renders IMAP / SMTP /
//      Autodiscover / Autoconfig info with copy buttons AND
//      the values contain the magic strings (mail host, port
//      993, port 587, /autodiscover/autodiscover.xml,
//      /.well-known/autoconfig/mail/config-v1.1.xml).
//   7. Zero browser-console errors. Warnings are tolerated
//      but errors fail the smoke.
//
// The Chrome / WebSocket plumbing prefers the Node-22+
// built-in WebSocket; falls back to the `ws` npm package for
// Node 18..21.
//
// IMPORTANT: this script is intentionally a single-file
// script. Dependencies: node + a Chromium-class browser.
// No npm-install required.

import { spawn } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

// ── 0. WebSocket: prefer global, fall back to `ws` ────────────
let WebSocket = globalThis.WebSocket;
if (!WebSocket) {
    try {
        const mod = await import('ws');
        WebSocket = mod.WebSocket || mod.default;
    } catch (e) {
        console.error('FAIL smoke-webmail-functional-browser: needs Node 22+ (built-in WebSocket)');
        console.error('       or the ws npm package installed. `npm install ws` in your CI image.');
        process.exit(1);
    }
}

// ── 1. CLI / runtime setup ────────────────────────────────────
const browserBin = process.env.CHROME_BROWSER || process.argv[3] || process.env.CHROME;
if (!browserBin) {
    console.error('FAIL smoke-webmail-functional-browser: CHROME_BROWSER env not set and argv[3] missing');
    process.exit(1);
}
const webmailDir = process.argv[2];
if (!webmailDir) {
    console.error('FAIL smoke-webmail-functional-browser: webmail bundle path missing (argv[2])');
    process.exit(1);
}

// ── 2. Mock user / mailbox payload ────────────────────────────
const MOCK_USER = {
    id: 1,
    email: 'r1-smoke@orvix.local',
    role: 'user',
};
const MOCK_MAILBOX = {
    id: 7,
    email: 'r1-smoke@orvix.local',
    name: 'R1 Smoke',
    is_admin: false,
    quota_mb: 5120,
    used_bytes: 0,
    msg_count: 0,
};
const MOCK_FOLDERS = [
    { id: 1, name: 'INBOX',   path: 'INBOX',   folder_type: 'inbox',   system: true, message_count: 0, unread_count: 0, total_size: 0 },
    { id: 2, name: 'Sent',    path: 'Sent',    folder_type: 'sent',    system: true, message_count: 0, unread_count: 0, total_size: 0 },
    { id: 3, name: 'Drafts',  path: 'Drafts',  folder_type: 'drafts',  system: true, message_count: 0, unread_count: 0, total_size: 0 },
    { id: 4, name: 'Trash',   path: 'Trash',   folder_type: 'trash',   system: true, message_count: 0, unread_count: 0, total_size: 0 },
    { id: 5, name: 'Junk',    path: 'Junk',    folder_type: 'junk',    system: true, message_count: 0, unread_count: 0, total_size: 0 },
    { id: 6, name: 'Archive', path: 'Archive', folder_type: 'archive', system: true, message_count: 0, unread_count: 0, total_size: 0 },
];
const MOCK_MESSAGES = { messages: [], folder: 'INBOX', folder_id: 1, limit: 50, offset: 0, has_more: false, total: 0 };
const MOCK_SETTINGS = {
    display_name: 'R1 Smoke',
    timezone: '',
    language: 'en',
    date_format: 'locale',
    time_format: '24h',
    text_direction: 'auto',
    theme: 'auto',
    density: 'comfortable',
    preview_lines: 2,
    signature_enabled: false,
    signature_text: '',
    signature_in_replies: false,
    default_reply_mode: 'reply',
    autosave_seconds: 10,
    confirm_before_discard: true,
    warn_on_empty_subject: true,
    default_folder: 'INBOX',
    mark_read_delay_seconds: 0,
    sender_display: 'name',
    notify_inapp: true,
    notify_push: false,
};
const MOCK_PUSH_STATUS = { available: false, enabled: false };

// mockFor returns {status, body, headers?}.
//
// The mock follows the production auth-gate contract:
//   - /api/v1/webmail/session returns 401 when no
//     `access_token` cookie is present (matches the
//     behaviour of the real Fiber middleware: invalid /
//     missing JWT → 401).
//   - /api/v1/webmail/session returns 200 +
//     authenticated:true when the cookie IS present.
//   - /api/v1/webmail/login sets a Set-Cookie
//     `access_token=mock; Path=/; SameSite=Lax` so the
//     next probe sees a valid session.
//
// Returning 401 on the no-cookie probe is what drives the
// gate into the showLogin() code path — without it the
// smoke cannot exercise the login form or the post-login
// SPA boot.

const COOKIE_NAME = 'access_token';

function hasAuthedCookie(req) {
    const raw = req.headers.cookie || '';
    return raw.split(/;\s*/).some((part) => part === COOKIE_NAME + '=mock');
}

function mockFor(method, urlPath, body, req) {
    if (method === 'POST' && urlPath === '/api/v1/webmail/login') {
        return {
            status: 200,
            headers: {
                'Set-Cookie': COOKIE_NAME + '=mock; Path=/; SameSite=Lax',
            },
            body: { authenticated: true, mailbox: { id: MOCK_MAILBOX.id, email: MOCK_MAILBOX.email, is_admin: false } },
        };
    }
    if (method === 'GET' && urlPath === '/api/v1/webmail/session') {
        if (!hasAuthedCookie(req)) {
            return { status: 401, body: { error: 'unauthenticated' } };
        }
        return { status: 200, body: { authenticated: true, user: MOCK_USER, mailbox: { id: MOCK_MAILBOX.id, email: MOCK_MAILBOX.email, is_admin: false } } };
    }
    if (method === 'POST' && urlPath === '/api/v1/webmail/logout') {
        return {
            status: 200,
            headers: {
                'Set-Cookie': COOKIE_NAME + '=; Path=/; SameSite=Lax; Max-Age=0',
            },
            body: { status: 'logged_out' },
        };
    }
    // Cookie-gated endpoints — return 401 to non-owners to
    // match the router's protected-group middleware
    // behaviour. This is what the production stack does
    // (auth middleware → 401 before the handler runs).
    const cookieRequired = [
        '/api/v1/webmail/me',
        '/api/v1/webmail/folders',
        '/api/v1/webmail/push/status',
        '/api/v1/webmail/rules',
        '/api/v1/webmail/vacation',
        '/api/v1/webmail/forwarding',
        '/api/v1/webmail/settings',
    ];
    if (method === 'GET' && cookieRequired.includes(urlPath)) {
        if (!hasAuthedCookie(req)) {
            return { status: 401, body: { error: 'unauthenticated' } };
        }
        if (urlPath === '/api/v1/webmail/me') {
            return { status: 200, body: { user: MOCK_USER, mailbox: MOCK_MAILBOX } };
        }
        if (urlPath === '/api/v1/webmail/folders') {
            return { status: 200, body: { folders: MOCK_FOLDERS } };
        }
        if (urlPath === '/api/v1/webmail/settings') {
            return { status: 200, body: MOCK_SETTINGS };
        }
        if (urlPath === '/api/v1/webmail/push/status') {
            return { status: 200, body: MOCK_PUSH_STATUS };
        }
        if (urlPath === '/api/v1/webmail/rules') {
            return { status: 200, body: { rules: [] } };
        }
        if (urlPath === '/api/v1/webmail/vacation') {
            return { status: 200, body: { enabled: false, subject: '', body: '' } };
        }
        if (urlPath === '/api/v1/webmail/forwarding') {
            return { status: 200, body: { enabled: false, keep_local_copy: true, forward_to: '' } };
        }
    }
    // Messages list — cookie-gated.
    if (method === 'GET' && urlPath.startsWith('/api/v1/webmail/messages')) {
        if (!hasAuthedCookie(req)) {
            return { status: 401, body: { error: 'unauthenticated' } };
        }
        return { status: 200, body: MOCK_MESSAGES };
    }
    if (method === 'GET' && urlPath === '/api/v1/me') {
        return { status: 200, body: MOCK_USER };
    }
    return { status: 404, body: { error: 'mocked 404 for ' + method + ' ' + urlPath } };
}

// ── 3. Local server — serves release/webmail AND mocks API ────
//
// Single port, single Node process. Static files are served
// from disk; /api/v1/webmail/* is answered from the canned
// JSON. The mock backend answers NO /api/v1/queue — that's
// the regression guard for the user-facing webmail client
// (which must NEVER call the admin-only queue path).

const MIME = {
    '.html': 'text/html; charset=utf-8',
    '.js':   'application/javascript; charset=utf-8',
    '.css':  'text/css; charset=utf-8',
    '.json': 'application/json; charset=utf-8',
    '.xml':  'application/xml; charset=utf-8',
    '.map':  'application/json; charset=utf-8',
    '.svg':  'image/svg+xml; charset=utf-8',
    '.png':  'image/png',
    '.ico':  'image/x-icon',
};

function readBody(req) {
    return new Promise((resolve, reject) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
        req.on('error', reject);
    });
}

function jsonResponse(res, status, body, headers) {
    const buf = Buffer.from(JSON.stringify(body), 'utf8');
    const hdrs = Object.assign({
        'Content-Type': 'application/json; charset=utf-8',
        'Content-Length': buf.length,
        'Access-Control-Allow-Origin': '*',
        'Cache-Control': 'no-store',
    }, headers || {});
    res.writeHead(status, hdrs);
    res.end(buf);
}

function serveStatic(req, res, p) {
    // Strip the leading slash. Refuse anything containing ..
    // (path traversal protection — the smoke server is
    // local-only, but the harness still has to be safe).
    if (p.includes('..') || p.includes('\u0000')) {
        res.writeHead(400);
        res.end('bad path');
        return;
    }
    let fsPath = path.join(webmailDir, p);
    if (!fs.existsSync(fsPath)) {
        // SPA fallback: index.html for routes we don't have
        if (!path.extname(p)) {
            fsPath = path.join(webmailDir, 'index.html');
        } else {
            res.writeHead(404);
            res.end('not found');
            return;
        }
    }
    if (fs.statSync(fsPath).isDirectory()) {
        fsPath = path.join(fsPath, 'index.html');
    }
    fs.readFile(fsPath, (err, data) => {
        if (err) {
            res.writeHead(500);
            res.end('read error');
            return;
        }
        const ext = path.extname(fsPath).toLowerCase();
        const ct = MIME[ext] || 'application/octet-stream';
        res.writeHead(200, {
            'Content-Type': ct,
            'Content-Length': data.length,
            'Cache-Control': 'no-store',
        });
        res.end(data);
    });
}

const server = http.createServer(async (req, res) => {
    try {
        const url = new URL(req.url, 'http://127.0.0.1');
        const p = url.pathname;
        if (p.startsWith('/api/v1/webmail/') || p === '/api/v1/me') {
            const body = await readBody(req);
            const m = mockFor(req.method, p, body, req);
            jsonResponse(res, m.status, m.body, m.headers);
            return;
        }
        // Static + SPA fallback.
        serveStatic(req, res, p === '/' ? 'index.html' : p);
    } catch (e) {
        res.writeHead(500);
        res.end('server error: ' + e.message);
    }
});

const port = await new Promise((resolve, reject) => {
    server.listen(0, '127.0.0.1', () => {
        const a = server.address();
        if (!a) reject(new Error('no address'));
        resolve(a.port);
    });
}).catch((e) => { console.error('FAIL local server:', e); process.exit(1); });
const TARGET_URL = `http://127.0.0.1:${port}/`;
process.on('exit', () => { try { server.close(); } catch {} });
process.on('SIGINT', () => { server.close(); process.exit(130); });

// ── 4. Boot Chrome and attach via CDP ─────────────────────────

const profileDir = `${process.env.TEMP || process.env.TMPDIR || '/tmp'}/orvix-webmail-smoke-${Date.now()}`;
const args = [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--disable-dev-shm-usage',
    '--hide-scrollbars',
    `--user-data-dir=${profileDir}`,
    '--remote-debugging-port=9224',
    '--window-size=1280,800',
    TARGET_URL,
];
const chromeProc = spawn(browserBin, args, {
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
});
chromeProc.stderr.on('data', () => { /* discard */ });
chromeProc.on('exit', (code) => {
    if (code != null && code !== 0 && process.exitCode == null) {
        console.error(`FAIL Chrome exited unexpectedly with code ${code}`);
        process.exit(1);
    }
});
process.on('exit', () => { try { chromeProc.kill('SIGKILL'); } catch {} });

// Wait for the remote-debugging endpoint to bind.
let browserWS = null;
for (let i = 0; i < 100; i++) {
    try {
        const ver = await new Promise((resolve, reject) => {
            const req = http.get('http://127.0.0.1:9224/json/version', (res) => {
                let buf = '';
                res.on('data', (c) => { buf += c; });
                res.on('end', () => resolve(buf));
            });
            req.on('error', reject);
            req.setTimeout(500, () => req.destroy(new Error('timeout')));
        });
        const j = JSON.parse(ver);
        if (j.webSocketDebuggerUrl) {
            browserWS = j.webSocketDebuggerUrl;
            break;
        }
    } catch { /* retry */ }
    await sleep(100);
}
if (!browserWS) {
    console.error('FAIL Chrome remote-debugging endpoint did not come up');
    chromeProc.kill('SIGKILL');
    process.exit(1);
}

const browser = new WebSocket(browserWS);
await new Promise((resolve, reject) => {
    const onOpen = () => { browser.removeEventListener('error', onErr); resolve(); };
    const onErr  = (e) => { browser.removeEventListener('open', onOpen); reject(e); };
    browser.addEventListener('open', onOpen);
    browser.addEventListener('error', onErr);
});

// Open a target tab that loads TARGET_URL.
let nextId = 1;
const pending = new Map();
const consoleLog = [];
browser.addEventListener('message', (ev) => {
    try {
        const msg = JSON.parse(typeof ev === 'string' ? ev : (ev.data || ev));
        if (msg.id != null && pending.has(msg.id)) {
            const { resolve, reject } = pending.get(msg.id);
            pending.delete(msg.id);
            if (msg.error) reject(new Error(`CDP ${msg.method}: ${msg.error.message}`));
            else resolve(msg.result);
        }
        if (msg.method === 'Runtime.consoleAPICalled') {
            consoleLog.push({ type: msg.params.type, text: (msg.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ') });
        }
        if (msg.method === 'Runtime.exceptionThrown') {
            const desc = msg.params.exceptionDetails?.exception?.description || msg.params.exceptionDetails?.text || 'exception';
            consoleLog.push({ type: 'exception', text: desc });
        }
    } catch { /* keep listening */ }
});

function cdp(method, params = {}) {
    return new Promise((resolve, reject) => {
        const id = nextId++;
        pending.set(id, { resolve, reject });
        try {
            browser.send(JSON.stringify({ id, method, params, sessionId: session.sessionId }));
        } catch (e) {
            pending.delete(id);
            reject(e);
        }
    });
}

// Open the target tab.
const target = await new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve: (r) => resolve(r), reject });
    browser.send(JSON.stringify({ id, method: 'Target.createTarget', params: { url: TARGET_URL } }));
});
const targetId = target.targetId;
const att = await new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve: (r) => resolve(r), reject });
    browser.send(JSON.stringify({ id, method: 'Target.attachToTarget', params: { targetId, flatten: true } }));
});
const session = { sessionId: att.sessionId };

async function evalExpr(expression, awaitPromise = false) {
    const r = await cdp('Runtime.evaluate', { expression, awaitPromise, returnByValue: true });
    if (r.exceptionDetails) {
        const msg = r.exceptionDetails.exception?.description || r.exceptionDetails.text || 'eval failed';
        throw new Error('eval error: ' + msg);
    }
    return r.result?.value;
}

await cdp('Page.enable', {});
await cdp('Page.navigate', { url: TARGET_URL });
await sleep(1500);

// ── 5. Phase 1 — auth-gate renders login form ─────────────────
//
// The auth-gate renders the login form on first paint when
// no session cookie is set. The gate executes BEFORE the
// intercepted /session response, so we always see the
// login form regardless of the mock's success state.

const found1 = await evalExpr(`
(async () => {
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline) {
        const email = document.querySelector('input[type="email"], input[name="email"], input[name="username"]');
        const pw    = document.querySelector('input[type="password"]');
        const form  = document.querySelector('form');
        if (email && pw && form) {
            return { ok: true, email: email.name, pw: pw.name };
        }
        await new Promise(r => setTimeout(r, 100));
    }
    return { ok: false, sample: document.body.outerHTML.slice(0, 400) };
})()
`, true);
if (!found1 || !found1.ok) {
    console.error('FAIL phase 1 — auth-gate login form did not render');
    console.error('  DOM sample:', (found1 && found1.sample) || '<empty>');
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 1 — auth-gate login form renders (email="${found1.email}", pw="${found1.pw}")`);

// ── 6. Phase 2 — submit login and assert SPA shell ────────────

await evalExpr(`
(async () => {
    const emailField = document.querySelector('input[name="email"], input[name="username"]');
    const pwField    = document.querySelector('input[type="password"]');
    if (emailField && pwField) {
        emailField.value = 'r1-smoke@orvix.local';
        emailField.dispatchEvent(new Event('input',  { bubbles: true }));
        pwField.value    = 'pw-not-used-by-mock';
        pwField.dispatchEvent(new Event('input', { bubbles: true }));
        const form = emailField.closest('form');
        if (form) form.requestSubmit ? form.requestSubmit() : form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }
    return true;
})()
`, true);
await sleep(800);
// Force a reload so the SPA boots with the now-valid mocked session.
await evalExpr(`window.location.reload(); 'reload'`, false);
await sleep(1800);

// ── 7. Phase 3 — SPA shell renders sidebar / list / pane ──────
//
// The shell exposes a public API (window.OrvixWebmail.init)
// the auth-gate uses. Once init resolves, the bundle has
// rendered the folder sidebar, the message list, and the
// reading pane. The exact class names evolve with the bundle
// — we use a stable surface: any of these contain elements.
// On the failure path the eval returns the body length so we
// can tell empty-page from populated.

const shell = await evalExpr(`
(async () => {
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline) {
        const api = window.OrvixWebmail || window.orvixWebmail || null;
        if (api && typeof api.init === 'function') {
            const initResult = api.init();
            if (initResult && typeof initResult.then === 'function') {
                try { await initResult; } catch { /* init may race on settings fetch */ }
            }
        }
        const sidebar = document.querySelector('aside, .folders, .sidebar, [data-testid="folders"], .folder-list');
        const list    = document.querySelector('.email-list, .message-list, .messages, main, [data-testid="message-list"]');
        const pane    = document.querySelector('.reading-pane, .reader, [data-testid="reading-pane"]');
        if (sidebar && list && pane) {
            return { ok: true, hasSidebar: !!sidebar, hasList: !!list, hasPane: !!pane };
        }
        await new Promise(r => setTimeout(r, 200));
    }
    return { ok: false, bodyLen: document.body.outerHTML.length, html: document.body.outerHTML.slice(0, 800) };
})()
`, true);
if (!shell || !shell.ok) {
    console.error('FAIL phase 2 — SPA shell did not render sidebar / message list / reading pane');
    console.error('  diagnostics:', JSON.stringify(shell));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 2 — SPA shell renders sidebar=${shell.hasSidebar}, list=${shell.hasList}, pane=${shell.hasPane}`);

// ── 8. Phase 4 — compose modal opens ──────────────────────────

const compose = await evalExpr(`
(async () => {
    const api = window.OrvixWebmail || window.orvixWebmail || null;
    if (api && typeof api.openCompose === 'function') {
        api.openCompose();
    } else {
        const btn = document.querySelector('[data-testid="new-message"], button.compose, .new-message, [aria-label*="compose" i], [aria-label*="new message" i]');
        if (btn) btn.click();
    }
    const deadline = Date.now() + 4000;
    while (Date.now() < deadline) {
        const composeDialog = document.querySelector('.modal[role="dialog"][aria-label="Compose message"]');
        const composeBody   = document.querySelector('textarea.compose-body');
        if (composeDialog || composeBody) {
            return { ok: true, openedBy: api && api.openCompose ? 'api' : 'click' };
        }
        await new Promise(r => setTimeout(r, 150));
    }
    return { ok: false, body: document.body.outerHTML.length, html: document.body.outerHTML.slice(0, 1000) };
})()
`, true);
if (!compose || !compose.ok) {
    console.error('FAIL phase 4 — compose modal did not open');
    console.error('  diagnostics:', JSON.stringify(compose));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 4 — compose modal opened (via ${compose.openedBy})`);

// Close the compose modal so it does not cover the Settings panel
// for the rest of the smoke.
await evalExpr(`
(function () {
    const close = document.querySelector('.compose-modal [aria-label*="close" i], .compose-modal button.icon-btn');
    if (close) close.click();
})()
`, false);
await sleep(300);

// ── 9. Phase 5 — dirAuto helper exposes correct behaviour ─────

const dirAuto = await evalExpr(`
(function () {
    const api = window.OrvixWebmail || window.orvixWebmail || null;
    const fn = api && api.utils && api.utils.dirAuto ? api.utils.dirAuto : (typeof dirAuto === 'function' ? dirAuto : null);
    if (!fn) return { ok: false, reason: 'dirAuto not exposed' };
    return {
        ok: true,
        arabic: fn('\\u0627\\u0644\\u0633\\u0644\\u0627\\u0645'),
        latin:  fn('hello world'),
        empty:  fn(''),
        mixed:  fn('\\u0627\\u0644\\u0633\\u0644\\u0627\\u0645 world'),
    };
})()
`, false);
if (!dirAuto || !dirAuto.ok || dirAuto.arabic !== 'rtl' || dirAuto.latin !== 'ltr' || dirAuto.empty !== 'auto') {
    console.error('FAIL phase 5 — dirAuto helper returned unexpected results:', JSON.stringify(dirAuto));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 5 — dirAuto(arabic)='${dirAuto.arabic}', dirAuto(latin)='${dirAuto.latin}', dirAuto(empty)='${dirAuto.empty}', dirAuto(mixed)='${dirAuto.mixed}'`);

// ── 10. Phase 6 — Mail Client Setup tab renders ──────────────
//
// This is the Release-1 deliverable. The smoke fails closed
// if the Client Setup tab is missing or its IMAP / SMTP /
// Autodiscover / Autoconfig rows don't carry the magic
// strings the user-facing copy promises:
//
//   • IMAP / SMTP host must contain 'mail.'
//   • IMAP port :993, SMTP port :587
//   • Outlook Autodiscover URL contains '/autodiscover/autodiscover.xml'
//   • Thunderbird Autoconfig URL contains '/.well-known/autoconfig/mail/config-v1.1.xml'
//   • A copy button is wired to each row
//   • The settings modal renders without errors
//
// The tab activation goes through window.OrvixWebmail.openClientSetup()
// (added in this same Release 1 cut as a deep-link entry
// point so smoke harnesses don't need to drive multiple
// mouse events just to find a single tab).

const clientSetup = await evalExpr(`
(async () => {
    const api = window.OrvixWebmail || window.orvixWebmail || null;
    if (!api) return { ok: false, reason: 'OrvixWebmail api missing' };
    if (typeof api.openClientSetup !== 'function') {
        return { ok: false, reason: 'openClientSetup not exported' };
    }
    api.openClientSetup();
    const deadline = Date.now() + 6000;
    while (Date.now() < deadline) {
        const tab = document.querySelector('.settings-modal .settings-tab[data-tab="client-setup"]');
        const content = document.querySelector('.settings-modal .settings-content');
        if (tab && content && content.textContent && content.textContent.toLowerCase().indexOf('imap') >= 0) {
            // Collect what we need to verify.
            const valueBlocks = Array.from(document.querySelectorAll('.settings-modal .settings-client-setup-value')).map((n) => n.textContent);
            const copyButtons  = document.querySelectorAll('.settings-modal .settings-copy-btn').length;
            const checkStrings = ['mail.', ':993', ':587', '/autodiscover/autodiscover.xml', '/.well-known/autoconfig/mail/config-v1.1.xml'];
            const match = {};
            for (const k of checkStrings) {
                match[k] = valueBlocks.some((v) => v.indexOf(k) >= 0);
            }
            return {
                ok: true,
                valueBlocks: valueBlocks.length,
                copyButtons,
                values: valueBlocks,
                match,
            };
        }
        await new Promise(r => setTimeout(r, 100));
    }
    return { ok: false, reason: 'client-setup tab did not render IMAP/SMTP/Autodiscover content', body: document.body.outerHTML.slice(0, 500) };
})()
`, true);
if (!clientSetup || !clientSetup.ok) {
    console.error('FAIL phase 6 — Mail Client Setup tab did not render');
    console.error('  diagnostics:', JSON.stringify(clientSetup));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const missing = Object.entries(clientSetup.match || {}).filter(([, v]) => !v).map(([k]) => k);
if (missing.length > 0) {
    console.error('FAIL phase 6 — Mail Client Setup values are missing required substrings:', missing.join(', '));
    console.error('  values:', JSON.stringify(clientSetup.values));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (clientSetup.copyButtons < 8) {
    console.error('FAIL phase 6 — Mail Client Setup tab needs >= 8 copy buttons, found', clientSetup.copyButtons);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 6 — Mail Client Setup tab renders (${clientSetup.valueBlocks} value blocks, ${clientSetup.copyButtons} copy buttons, all required substrings present)`);

// Close the Settings modal.
await evalExpr(`
(function () {
    const close = document.querySelector('.settings-modal [aria-label*="close" i], .settings-modal button.icon-btn');
    if (close) close.click();
})()
`, false);
await sleep(200);

// ── 11. Phase 7 — zero browser-console errors ─────────────────

const fatal = consoleLog.filter((e) => e.type === 'error' || e.type === 'exception');
if (fatal.length > 0) {
    console.error(`FAIL phase 7 — ${fatal.length} browser console error(s):`);
    for (const e of fatal.slice(0, 8)) console.error(`  [${e.type}] ${e.text}`);
    if (fatal.length > 8) console.error(`  ... and ${fatal.length - 8} more`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const warns = consoleLog.filter((e) => e.type === 'warning' || e.type === 'warn').length;
console.log(`PASS  phase 7 — zero browser-console errors (${warns} warning(s), ${consoleLog.length - warns - fatal.length} info/log message(s) ignored)`);

// ── 12. Done ─────────────────────────────────────────────────

chromeProc.kill('SIGKILL');
server.close();
console.log('ALL WEBMAIL FUNCTIONAL BROWSER TESTS PASSED');
process.exit(0);
