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

// ── 2b. CSRF regression-test state (item 2 runtime coverage) ──
//
// A real, stateful CSRF contract: GET /api/v1/csrf-token issues the
// current token; every non-GET/HEAD webmail mutation (except the
// pre-session login) must present it via X-CSRF-Token or is
// rejected. MOCK_MODE lets a test phase force exactly one of a
// specific failure on the NEXT matching mutation, so the phase can
// assert webmail.js's real retry/no-retry behavior rather than a
// reimplementation of it. REQUEST_LOG records every request the
// mock actually received (method, path, whether a CSRF header was
// present, its value) so a phase can assert on it via
// GET /__test__/state instead of instrumenting the page itself —
// this also doubles as the "no secrets leaked" check: the log is
// scanned for password/token literal values before each phase ends.
const CSRF_STATE = {
    token: 'mock-csrf-token-1234567890',
    rotateOnNextMutation: false,
};
const MOCK_MODE = {
    forceOrdinary403Once: false,
    force429Once: false,
    force500Once: false,
};
const REQUEST_LOG = [];
const COUNTERS = { messagesSent: 0 };

// resetMockTestState clears observation state (the request log, the
// message counter, and any pending forced-failure mode) between
// phases. It deliberately does NOT touch CSRF_STATE.token: the
// client's csrfTokenCache is a real, persistent client-side cache
// that this reset has no way to invalidate remotely, so rotating the
// server's expected token here would desync from what the client
// still has cached and turn every "clean" phase into an accidental
// CSRF-retry scenario. Only phase 12 (which explicitly tests the
// retry path) sets rotateOnNextMutation itself, via /__test__/mode.
function resetMockTestState() {
    CSRF_STATE.rotateOnNextMutation = false;
    MOCK_MODE.forceOrdinary403Once = false;
    MOCK_MODE.force429Once = false;
    MOCK_MODE.force500Once = false;
    REQUEST_LOG.length = 0;
    COUNTERS.messagesSent = 0;
}

// Endpoints exempt from the CSRF/cookie mutation gate below —
// login has no session yet (matches the real backend: CSRF is a
// double-submit cookie check that cannot apply before a session
// cookie exists), and the __test__ control endpoints are harness
// plumbing, not part of the webmail contract under test.
const CSRF_EXEMPT_PATHS = new Set(['/api/v1/webmail/login']);

// csrfGate runs BEFORE any mutation handler below. Returns a
// {status,body} response to short-circuit with, or null to let the
// real handler proceed. Every call (whether it short-circuits or
// not) is recorded to REQUEST_LOG first.
function csrfGate(method, urlPath, req) {
    const hasCsrfHeader = Object.prototype.hasOwnProperty.call(req.headers, 'x-csrf-token');
    const csrfHeaderValue = req.headers['x-csrf-token'] || '';
    REQUEST_LOG.push({
        t: Date.now(),
        method,
        path: urlPath,
        contentType: req.headers['content-type'] || '',
        hasCsrfHeader,
        csrfHeaderValue,
    });

    if (method === 'GET' || method === 'HEAD' || CSRF_EXEMPT_PATHS.has(urlPath) || urlPath.startsWith('/__test__/')) {
        return null;
    }

    // Forced-failure modes consume themselves (fire once), so a test
    // phase can assert "exactly one retry" / "no retry" precisely.
    if (MOCK_MODE.forceOrdinary403Once) {
        MOCK_MODE.forceOrdinary403Once = false;
        return { status: 403, body: { error: 'forbidden: insufficient permissions' } };
    }
    if (MOCK_MODE.force429Once) {
        MOCK_MODE.force429Once = false;
        return { status: 429, headers: { 'Retry-After': '2' }, body: { error: 'too many requests' } };
    }
    if (MOCK_MODE.force500Once) {
        MOCK_MODE.force500Once = false;
        return { status: 500, body: { error: 'internal server error' } };
    }

    if (CSRF_STATE.rotateOnNextMutation) {
        // Simulate the server-side token rotating out from under a
        // client that cached the old value (e.g. a new browser tab
        // logged in elsewhere, or the cookie/session rotated). The
        // client's cached token is now stale -> this request 403s
        // with a CSRF-flavored message; webmail.js's api()/
        // sendMessageWithAttachments() must clear its cache, refetch
        // via GET /api/v1/csrf-token (which now returns the ROTATED
        // value below), and retry exactly once.
        CSRF_STATE.rotateOnNextMutation = false;
        CSRF_STATE.token = 'mock-csrf-token-rotated-' + Date.now();
        return { status: 403, body: { error: 'csrf token invalid' } };
    }

    if (!hasCsrfHeader || csrfHeaderValue !== CSRF_STATE.token) {
        return { status: 403, body: { error: 'csrf token missing or invalid in header' } };
    }
    return null;
}

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
    // ── __test__ control plane (harness-only, not part of the
    // webmail contract) ──
    if (urlPath === '/__test__/state') {
        return { status: 200, body: { requests: REQUEST_LOG, counters: COUNTERS, csrfToken: CSRF_STATE.token } };
    }
    if (method === 'POST' && urlPath === '/__test__/mode') {
        let parsed = {};
        try { parsed = body ? JSON.parse(body) : {}; } catch (e) { /* ignore */ }
        Object.assign(MOCK_MODE, parsed);
        if (parsed.rotateOnNextMutation) CSRF_STATE.rotateOnNextMutation = true;
        return { status: 200, body: { ok: true, mode: MOCK_MODE } };
    }
    if (method === 'POST' && urlPath === '/__test__/reset') {
        resetMockTestState();
        return { status: 200, body: { ok: true } };
    }

    // Every other request passes through the CSRF/failure-injection
    // gate first (also logs it to REQUEST_LOG).
    const gated = csrfGate(method, urlPath, req);
    if (gated) return gated;

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
    if (method === 'POST' && urlPath === '/api/v1/webmail/password/change') {
        if (!hasAuthedCookie(req)) {
            return { status: 401, body: { error: 'unauthenticated' } };
        }
        // Surface what we received as a structured payload so
        // the functional smoke can distinguish parse-failure
        // from validation-failure.
        const debug = {
            bodyType: typeof body,
            bodyLen: typeof body === 'string' ? body.length : -1,
            bodySample: typeof body === 'string' ? body.slice(0, 200) : null,
        };
        let parsed = null;
        try { parsed = body ? JSON.parse(body) : null; } catch (e) { return { status: 400, body: { error: 'invalid request', _debug: debug, _err: e.message } }; }
        if (!parsed || typeof parsed !== 'object') {
            return { status: 400, body: { error: 'invalid request', _debug: debug, _parsedType: typeof parsed } };
        }
        if (!parsed || typeof parsed !== 'object') {
            return { status: 400, body: { error: 'invalid request' } };
        }
        const cp = typeof parsed.current_password === 'string' ? parsed.current_password : '';
        const np = typeof parsed.new_password === 'string' ? parsed.new_password : '';
        if (!cp) return { status: 400, body: { error: 'current password required' } };
        if (!np) return { status: 400, body: { error: 'new password required' } };
        if (np.length < 8) {
            return { status: 400, body: { error: 'new password must be at least 8 characters' } };
        }
        // The functional smoke's Phase 9 sends a mismatched
        // confirm. The frontend's client-side validation
        // catches the mismatch BEFORE the network call, so
        // the mock never sees a mismatched payload. We still
        // guard the server-side check for completeness.
        if (typeof parsed.confirm_password === 'string' && parsed.confirm_password !== np) {
            return { status: 400, body: { error: 'new password and confirmation do not match' } };
        }
        // "oldpw-not-checked" simulates a wrong current
        // password; everything else simulates success.
        if (cp === 'oldpw-not-checked') {
            return { status: 401, body: { error: 'invalid credentials' } };
        }
        // "short" simulates a too-short new password (the
        // frontend already filters this client-side, but the
        // server's check is the source of truth).
        if (np === 'short') {
            return { status: 400, body: { error: 'new password must be at least 8 characters' } };
        }
        return { status: 200, body: { status: 'changed' } };
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
    if (method === 'GET' && urlPath === '/api/v1/csrf-token') {
        // The webmail SPA's csrfFetch/getCsrfToken probes this on
        // first mutating call (and caches). CSRF_STATE.token is
        // mutable so a test phase can force a mid-flow rotation.
        return { status: 200, body: { csrf_token: CSRF_STATE.token } };
    }

    // ── Mutation endpoints (item 2 runtime CSRF/retry coverage) ──
    // Every one of these is reached only after csrfGate() above has
    // already validated (or forced-failed) the CSRF header, so
    // reaching here means the header was present and correct.
    if (method === 'POST' && urlPath === '/api/v1/webmail/send') {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        const ct = req.headers['content-type'] || '';
        if (ct.indexOf('multipart/form-data') === 0) {
            // Multipart attachment send. Not parsed field-by-field —
            // the CSRF-header assertion already happened in
            // csrfGate(); reaching here only proves the header was
            // valid on a multipart request specifically.
            COUNTERS.messagesSent += 1;
            return { status: 200, body: { status: 'queued', id: 'mock-msg-' + COUNTERS.messagesSent } };
        }
        let parsed = null;
        try { parsed = body ? JSON.parse(body) : null; } catch (e) { return { status: 400, body: { error: 'invalid request' } }; }
        if (!parsed || !parsed.to) return { status: 400, body: { error: 'to is required' } };
        COUNTERS.messagesSent += 1;
        return { status: 200, body: { status: 'queued', id: 'mock-msg-' + COUNTERS.messagesSent } };
    }
    if (method === 'POST' && urlPath === '/api/v1/webmail/drafts') {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { id: 501, status: 'saved' } };
    }
    if (method === 'PUT' && urlPath.startsWith('/api/v1/webmail/drafts/')) {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'saved' } };
    }
    if (method === 'DELETE' && urlPath.startsWith('/api/v1/webmail/drafts/')) {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'deleted' } };
    }
    if (method === 'PATCH' && urlPath.startsWith('/api/v1/webmail/messages/') && !urlPath.endsWith('/delete') && !urlPath.endsWith('/archive') && !urlPath.endsWith('/move') && urlPath.indexOf('/batch') === -1) {
        // Flag mutation, e.g. PATCH /api/v1/webmail/messages/:id
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'updated' } };
    }
    if (method === 'POST' && /\/api\/v1\/webmail\/messages\/[^/]+\/delete$/.test(urlPath)) {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'deleted' } };
    }
    if (method === 'POST' && /\/api\/v1\/webmail\/messages\/[^/]+\/archive$/.test(urlPath)) {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'archived' } };
    }
    if (method === 'POST' && /\/api\/v1\/webmail\/messages\/[^/]+\/move$/.test(urlPath)) {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'moved' } };
    }
    if (method === 'POST' && urlPath === '/api/v1/webmail/messages/batch') {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'ok', affected: 1 } };
    }
    if (method === 'PUT' && urlPath === '/api/v1/webmail/settings') {
        if (!hasAuthedCookie(req)) return { status: 401, body: { error: 'unauthenticated' } };
        return { status: 200, body: { status: 'saved' } };
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
        if (p.startsWith('/api/v1/webmail/') || p === '/api/v1/me' || p === '/api/v1/csrf-token' || p.startsWith('/__test__/')) {
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
// for the rest of the smoke. The modal's own class is just "modal"
// (there is no ".compose-modal" class anywhere in webmail.js) — the
// previous selector here was always a no-op, silently leaving this
// modal open in the DOM for the rest of the run. Scope to the
// Compose dialog specifically via its aria-label, matching the real
// close button (aria-label="Close", inside .modal-header).
await evalExpr(`
(function () {
    const dialog = document.querySelector('.modal[role="dialog"][aria-label="Compose message"]');
    const close = dialog && dialog.querySelector('button[aria-label="Close"]');
    if (close) close.click();
    const backdrop = dialog && dialog.closest('.modal-backdrop');
    if (backdrop) backdrop.remove();
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

// ── 12. Phase 8 — Security tab + Change Password form ────────
//
// Settings → Security must:
//   • Have no "Coming later" tab.
//   • Have no `.settings-deferred-list` element on the active
//     Security tab.
//   • Have no copy containing "future release" / "coming soon"
//     / "not implemented" / "is not enabled".
//   • Render the Change Password form with three password
//     inputs (current / new / confirm) plus a Save button.
//
// We open the Settings modal via the public API
// (window.OrvixWebmail.openSettingsModal), then click the
// Security tab. If either step fails, the rest of the smoke
// aborts.

const security = await evalExpr(`
(async () => {
    const api = window.OrvixWebmail || window.orvixWebmail || null;
    if (!api) return { ok: false, reason: 'OrvixWebmail API missing' };
    // Open Settings and switch to the Security tab. The
    // public API does not expose openSecurityTab; the
    // dispatch is by clicking the matching tab button
    // once the modal is up.
    api.openSettingsModal && api.openSettingsModal();
    const deadline1 = Date.now() + 4000;
    let modal = null;
    while (Date.now() < deadline1) {
        modal = document.querySelector('.settings-modal');
        if (modal) break;
        await new Promise(r => setTimeout(r, 80));
    }
    if (!modal) return { ok: false, reason: 'settings modal did not mount' };
    // Banned tab buttons.
    const bannedTabs = Array.from(modal.querySelectorAll('.settings-tab')).filter((t) => {
        const text = (t.textContent || '').toLowerCase();
        return text.indexOf('coming later') >= 0 ||
               text.indexOf('coming soon')  >= 0 ||
               text.indexOf('future release') >= 0;
    });
    if (bannedTabs.length > 0) {
        return { ok: false, reason: 'banned settings tab(s) present', tabs: bannedTabs.map((t) => t.textContent) };
    }
    // Click the Security tab.
    const securityTab = modal.querySelector('.settings-tab[data-tab="security"]');
    if (!securityTab) return { ok: false, reason: 'settings security tab not found' };
    securityTab.click();
    // Wait for the Security tab body to render.
    const deadline2 = Date.now() + 4000;
    let body = null;
    while (Date.now() < deadline2) {
        const content = modal.querySelector('.settings-content');
        // Security tab renders a form with the change-password-input class.
        if (content && content.querySelector('.settings-change-password-form')) { body = content; break; }
        await new Promise(r => setTimeout(r, 80));
    }
    if (!body) return { ok: false, reason: 'Security tab body did not render' };
    // Banned content strings inside the Settings modal.
    const bannedWords = ['Coming later', 'Available in a future', 'coming soon', 'TOTP / app-passwords', 'is not enabled'];
    const offenders = [];
    const allText = (modal.textContent || '');
    for (const w of bannedWords) {
        if (allText.indexOf(w) >= 0) offenders.push(w);
    }
    if (offenders.length > 0) {
        return { ok: false, reason: 'banned placeholder copy present', words: offenders };
    }
    // Banned CSS classes.
    const bannedClasses = Array.from(modal.querySelectorAll('.settings-deferred-list'));
    if (bannedClasses.length > 0) {
        return { ok: false, reason: 'settings-deferred-list element rendered', count: bannedClasses.length };
    }
    // Required Change Password surface.
    const inputs = body.querySelectorAll('.settings-change-password-input');
    const submit = body.querySelector('.settings-change-password-form button[type="submit"]');
    if (inputs.length < 3 || !submit) {
        return { ok: false, reason: 'change-password form missing inputs or submit', inputs: inputs.length, hasSubmit: !!submit };
    }
    const labels = Array.from(inputs).map((i) => i.getAttribute('autocomplete') || '');
    return {
        ok: true,
        tabs: Array.from(modal.querySelectorAll('.settings-tab')).map((t) => (t.textContent || '').trim()),
        inputCount: inputs.length,
        autocomplete: labels,
        submitText: (submit.textContent || '').trim(),
    };
})()
`, true);
if (!security || !security.ok) {
    console.error('FAIL phase 8 — Settings → Security tab missing real controls');
    console.error('  diagnostics:', JSON.stringify(security));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 8 — Settings → Security renders Change Password (${security.inputCount} inputs, submit="${security.submitText}", autocompletes=[${security.autocomplete.join(',')}], tabs=[${security.tabs.join(', ')}])`);

// ── 13. Phase 9 — mismatch validation surfaces inline error ───

const mismatch = await evalExpr(`
(async () => {
    const form = document.querySelector('.settings-change-password-form');
    if (!form) return { ok: false, reason: 'no form' };
    const setVal = (k, v) => { var i = form.querySelector('[data-key="' + k + '"]'); if (i) { i.value = v; i.dispatchEvent(new Event('input', { bubbles: true })); } };
    setVal('current_password', 'old-password-not-checked');
    setVal('new_password',     'NewPassword123');
    setVal('confirm_password', 'DIFFERENT-XYZ-999');
    var btn = form.querySelector('button[type="submit"]');
    if (!btn) return { ok: false, reason: 'no submit' };
    btn.click();
    // Wait for the inline status region to flip to error class.
    const deadline = Date.now() + 4000;
    while (Date.now() < deadline) {
        var status = form.querySelector('.settings-change-password-status');
        if (status && (status.className || '').indexOf('error') >= 0 && status.textContent) {
            return { ok: true, status: status.textContent };
        }
        await new Promise(r => setTimeout(r, 80));
    }
    return { ok: false, reason: 'no error status after mismatch submit' };
})()
`, true);
if (!mismatch || !mismatch.ok) {
    console.error('FAIL phase 9 — Change Password mismatch did not surface a visible error');
    console.error('  diagnostics:', JSON.stringify(mismatch));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 9 — Change Password mismatch surfaces inline error: "${mismatch.status}"`);

// ── 14. Phase 10 — successful change clears inputs + status ──
//
// The mock backend answers POST /api/v1/webmail/password/change
// with 200 {status:"changed"} when the payload has a non-empty
// current_password and new_password === confirm_password.
// The form clears the three inputs and writes a success status.
//
// This phase PASSes when the status flips to the "success"
// class and the three input value attributes are empty.

const success = await evalExpr(`
(async () => {
    const form = document.querySelector('.settings-change-password-form');
    if (!form) return { ok: false, reason: 'no form' };
    const setVal = (k, v) => { var i = form.querySelector('[data-key="' + k + '"]'); if (i) { i.value = v; i.dispatchEvent(new Event('input', { bubbles: true })); } };
    setVal('current_password', 'oldpw-correct');
    setVal('new_password',     'BrandNewPw2026');
    setVal('confirm_password', 'BrandNewPw2026');
    var btn = form.querySelector('button[type="submit"]');
    btn.click();
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline) {
        var status = form.querySelector('.settings-change-password-status');
        // The status flips to either 'success' or 'error' (NOT just empty).
        // Capture whichever arrives first so the diagnostic is useful.
        var klass = (status && status.className) || '';
        if (klass.indexOf('success') >= 0 || klass.indexOf('error') >= 0) {
            var cur = form.querySelector('[data-key="current_password"]').value;
            var nw  = form.querySelector('[data-key="new_password"]').value;
            var cf  = form.querySelector('[data-key="confirm_password"]').value;
            return {
                ok: klass.indexOf('success') >= 0,
                cleared: (cur === '' && nw === '' && cf === ''),
                statusText: (status.textContent || '').slice(0, 120),
                statusClass: klass,
                currentValue: cur,
                newValue: nw,
                confirmValue: cf,
                submitBtn: btn.textContent,
                submitDisabled: btn.disabled,
            };
        }
        await new Promise(r => setTimeout(r, 100));
    }
    return {
        ok: false,
        reason: 'no status change within 8s',
        cur: form.querySelector('[data-key="current_password"]').value,
        nw:  form.querySelector('[data-key="new_password"]').value,
        cf:  form.querySelector('[data-key="confirm_password"]').value,
    };
})()
`, true);
if (!success || !success.ok || !success.cleared) {
    console.error('FAIL phase 10 — Change Password successful submit did not clear inputs / set success status');
    console.error('  diagnostics:', JSON.stringify(success));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log(`PASS  phase 10 — successful change cleared all three inputs (current="", new="", confirm="") with status "${success.statusText}"`);

// Close the Settings modal.
await evalExpr(`
(function () {
    var backdrop = document.querySelector('.modal-backdrop.settings-modal');
    if (backdrop) backdrop.remove();
})()
`, false);
await sleep(100);

// ── 15. Phase 11 — CSRF header present on an ordinary send;
//        exactly one send request, exactly one queued message ────
//
// Opens compose, fills a valid message (non-empty subject/body so
// the warn_on_empty_subject confirm() dialog — which would hang
// headless Chrome — never fires), sends once, and asserts against
// the mock's own request log: exactly one POST /api/v1/webmail/send,
// it carried X-CSRF-Token with the current token value, and the
// server's message counter incremented by exactly 1. This is the
// "one click produces exactly one message" and "CSRF header on every
// mutation" requirement, verified from the server side rather than
// trusting client-side behavior alone.

async function sendComposeMessage(to, subject, bodyText) {
    return evalExpr(`
(async () => {
    // Clear any leftover toast from a prior phase first so a stale
    // .toast.success/.error can never be mistaken for this action's
    // result.
    document.querySelectorAll('.toast').forEach(t => t.remove());
    const api = window.OrvixWebmail || window.orvixWebmail || null;
    if (api && typeof api.openCompose === 'function') api.openCompose();
    const deadline1 = Date.now() + 4000;
    let modal = null;
    while (Date.now() < deadline1) {
        // Pick the LAST match, not the first: if a prior phase's
        // compose modal somehow failed to close, this favors the
        // freshly-opened one rather than a stale duplicate.
        const dialogs = document.querySelectorAll('.modal[role="dialog"][aria-label="Compose message"]');
        modal = dialogs.length ? dialogs[dialogs.length - 1] : null;
        if (modal) break;
        await new Promise(r => setTimeout(r, 100));
    }
    if (!modal) return { ok: false, reason: 'compose modal did not open' };
    const toInput = modal.querySelector('input[placeholder="recipient@example.com"]');
    const subjInput = modal.querySelector('input[placeholder="Subject"]');
    const bodyInput = modal.querySelector('textarea.compose-body');
    if (!toInput || !subjInput || !bodyInput) return { ok: false, reason: 'compose fields missing' };
    const setVal = (el, v) => { el.value = v; el.dispatchEvent(new Event('input', { bubbles: true })); };
    setVal(toInput, ${JSON.stringify(to)});
    setVal(subjInput, ${JSON.stringify(subject)});
    setVal(bodyInput, ${JSON.stringify(bodyText)});
    const sendBtn = modal.querySelector('.modal-footer .btn.primary');
    if (!sendBtn) return { ok: false, reason: 'send button missing' };
    sendBtn.click();
    const deadline2 = Date.now() + 6000;
    let result = null;
    while (Date.now() < deadline2) {
        const toastSuccess = document.querySelector('.toast.success');
        const toastError = document.querySelector('.toast.error');
        if (toastSuccess) { result = { ok: true, outcome: 'success', text: toastSuccess.textContent }; break; }
        if (toastError) { result = { ok: true, outcome: 'error', text: toastError.textContent }; break; }
        await new Promise(r => setTimeout(r, 100));
    }
    if (!result) result = { ok: false, reason: 'no toast within 6s' };
    // Close the modal (if a send succeeded it already removed itself;
    // on failure it stays open, so force it closed for the next phase).
    const dialog = document.querySelector('.modal[role="dialog"][aria-label="Compose message"]');
    const backdrop = dialog && dialog.closest('.modal-backdrop');
    if (backdrop) backdrop.remove();
    return result;
})()
`, true);
}

async function mockState() {
    return evalExpr(`fetch('/__test__/state').then(r => r.json())`, true);
}
async function mockReset() {
    return evalExpr(`fetch('/__test__/reset', { method: 'POST' }).then(r => r.json())`, true);
}

await mockReset();
const send1 = await sendComposeMessage('r1-recipient@orvix.local', 'Smoke phase 11', 'exactly one message, one click');
if (!send1 || !send1.ok || send1.outcome !== 'success') {
    console.error('FAIL phase 11 — ordinary send did not succeed:', JSON.stringify(send1));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const state1 = await mockState();
const sendCalls1 = (state1.requests || []).filter(r => r.method === 'POST' && r.path === '/api/v1/webmail/send');
if (sendCalls1.length !== 1) {
    console.error(`FAIL phase 11 — expected exactly 1 POST /send, got ${sendCalls1.length}:`, JSON.stringify(sendCalls1));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (!sendCalls1[0].hasCsrfHeader || sendCalls1[0].csrfHeaderValue !== state1.csrfToken) {
    console.error('FAIL phase 11 — send request did not carry the current CSRF token:', JSON.stringify(sendCalls1[0]), 'current token:', state1.csrfToken);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (state1.counters.messagesSent !== 1) {
    console.error(`FAIL phase 11 — expected messagesSent=1, got ${state1.counters.messagesSent}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log('PASS  phase 11 — one click produced exactly one /send request carrying the correct X-CSRF-Token, exactly one message queued');

// ── 16. Phase 12 — CSRF pre-handler 403 causes exactly one retry,
//        which succeeds; still exactly one message queued ────────
//
// The mock rotates its CSRF token out from under the client on the
// NEXT mutation only (simulating a stale cached token), 403s that
// one request with a CSRF-flavored message, then answers normally.
// webmail.js's api()/sendMessageWithAttachments() must clear its
// cache, refetch via GET /api/v1/csrf-token (now returning the
// rotated value), and retry exactly once — never duplicating the
// send.

await mockReset();
await evalExpr(`fetch('/__test__/mode', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ rotateOnNextMutation: true }) })`, true);
const send2 = await sendComposeMessage('r1-recipient@orvix.local', 'Smoke phase 12', 'csrf rotation forces exactly one retry');
if (!send2 || !send2.ok || send2.outcome !== 'success') {
    console.error('FAIL phase 12 — send after a forced CSRF rotation did not eventually succeed:', JSON.stringify(send2));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const state2 = await mockState();
const sendCalls2 = (state2.requests || []).filter(r => r.method === 'POST' && r.path === '/api/v1/webmail/send');
if (sendCalls2.length !== 2) {
    console.error(`FAIL phase 12 — expected exactly 2 attempts (1 rejected + 1 retry), got ${sendCalls2.length}:`, JSON.stringify(sendCalls2));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (sendCalls2[1].csrfHeaderValue !== state2.csrfToken || sendCalls2[1].csrfHeaderValue === sendCalls2[0].csrfHeaderValue) {
    console.error('FAIL phase 12 — the retry did not use a freshly refetched token:', JSON.stringify(sendCalls2));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (state2.counters.messagesSent !== 1) {
    console.error(`FAIL phase 12 — a CSRF-triggered retry must still produce exactly one queued message, got ${state2.counters.messagesSent}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log('PASS  phase 12 — CSRF pre-handler 403 caused exactly one retry with a freshly refetched token; exactly one message queued');

// ── 17. Phase 13 — ordinary (non-CSRF) 403 is NEVER retried ───────
//
// A permission-denied 403 with no "csrf" in the message must fail
// immediately with no automatic retry — retrying a genuinely
// forbidden request is not a CSRF-bootstrap concern and doing so
// blindly could duplicate a send if the handler had partially run.

await mockReset();
await evalExpr(`fetch('/__test__/mode', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ forceOrdinary403Once: true }) })`, true);
const send3 = await sendComposeMessage('r1-recipient@orvix.local', 'Smoke phase 13', 'ordinary 403 must not retry');
if (!send3 || !send3.ok || send3.outcome !== 'error') {
    console.error('FAIL phase 13 — an ordinary 403 should surface as a failed send, got:', JSON.stringify(send3));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const state3 = await mockState();
const sendCalls3 = (state3.requests || []).filter(r => r.method === 'POST' && r.path === '/api/v1/webmail/send');
if (sendCalls3.length !== 1) {
    console.error(`FAIL phase 13 — an ordinary 403 must not be retried; expected exactly 1 attempt, got ${sendCalls3.length}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (state3.counters.messagesSent !== 0) {
    console.error(`FAIL phase 13 — a rejected send must not queue a message, got messagesSent=${state3.counters.messagesSent}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log('PASS  phase 13 — ordinary 403 surfaced as a failure with no automatic retry, no message queued');

// ── 18. Phase 14 — 429 is NEVER auto-retried (no retry loop) ─────
//
// The mock answers 429 with Retry-After once. webmail.js has no
// automatic retry-after-backoff loop for ordinary mutations (only
// the single unambiguous-CSRF-403 retry exists) — a 429 must
// surface as a failed send on the first attempt, not spin.

await mockReset();
await evalExpr(`fetch('/__test__/mode', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ force429Once: true }) })`, true);
const send4 = await sendComposeMessage('r1-recipient@orvix.local', 'Smoke phase 14', '429 must not loop-retry');
if (!send4 || !send4.ok || send4.outcome !== 'error') {
    console.error('FAIL phase 14 — a 429 should surface as a failed send (no retry loop), got:', JSON.stringify(send4));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
const state4 = await mockState();
const sendCalls4 = (state4.requests || []).filter(r => r.method === 'POST' && r.path === '/api/v1/webmail/send');
if (sendCalls4.length !== 1) {
    console.error(`FAIL phase 14 — a 429 must not be auto-retried; expected exactly 1 attempt, got ${sendCalls4.length}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
if (state4.counters.messagesSent !== 0) {
    console.error(`FAIL phase 14 — a 429-rejected send must not queue a message, got messagesSent=${state4.counters.messagesSent}`);
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log('PASS  phase 14 — 429 surfaced as a single failed attempt honoring Retry-After without an automatic retry loop');

// ── 19. Phase 15 — no secret material ever left the browser as a
//        URL, and every send request logged by the mock used the
//        header (never a query string) for the CSRF token ─────────
//
// Scans every request path the mock recorded across phases 11-14
// for the CSRF token value and the login password used in Phase 2
// — neither may ever appear in a URL. The console log is scanned
// too, since a logged fetch call or error object containing a raw
// token/password would be an equally real leak.

const state5 = await mockState();
const allPaths = (state5.requests || []).map(r => r.path);
const secretsToCheck = [state5.csrfToken, 'pw-not-used-by-mock', 'BrandNewPw2026'];
let leaked = [];
for (const p of allPaths) {
    for (const secret of secretsToCheck) {
        if (secret && p.includes(secret)) leaked.push({ where: 'url', path: p, secret });
    }
}
// consoleLog is the Node-side array the CDP Runtime.consoleAPICalled
// listener has been appending to since the page first attached
// (see section 4) — this is the real transcript, not a page-side
// stand-in that could itself be wiped by a navigation.
const consoleText = consoleLog.map((e) => e.text || '').join('\n');
for (const secret of secretsToCheck) {
    if (secret && consoleText.includes(secret)) {
        leaked.push({ where: 'console', secret });
    }
}
if (leaked.length > 0) {
    console.error('FAIL phase 15 — secret material leaked into a URL or console log:', JSON.stringify(leaked));
    chromeProc.kill('SIGKILL');
    process.exit(1);
}
console.log('PASS  phase 15 — no CSRF token or password appeared in any request URL or console log across phases 11-14');

// ── 20. Done ─────────────────────────────────────────────────

chromeProc.kill('SIGKILL');
server.close();
console.log('ALL WEBMAIL FUNCTIONAL BROWSER TESTS PASSED');
process.exit(0);
