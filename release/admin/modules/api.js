/* =====================================================================
   modules/api.js — HTTP client with CSRF + auth + error normalization.

   This is the only file in the admin frontend that talks to fetch().
   Every page module imports apiGet / apiPost / apiPatch / apiDelete
   from here. Two safety guarantees:

     1. Every mutating call adds X-CSRF-Token. The header is fetched
        once per page load (CSRF tokens are long-lived in this build)
        and refreshed automatically on a CSRF-specific 403.

     2. Non-2xx responses are normalized into a thrown ApiError that
        carries .status, .code, .message and (for some endpoints)
        .fields. This means page modules can stay small: they catch
        one error shape and render it once.

   All exported functions return the parsed JSON body. Non-JSON
   responses throw an ApiError(status, 'non_json', body).
   ===================================================================== */

const TOKEN_KEY = 'orvix_admin_token';
const CSRF_KEY  = 'orvix_admin_csrf';

export class ApiError extends Error {
  constructor(status, code, message, fields) {
    super(message || code || 'request failed');
    this.name    = 'ApiError';
    this.status  = status || 0;
    this.code    = code   || '';
    this.fields  = fields || null;
  }
}

// ---------- token + csrf storage ----------------------------------
function getToken() {
  try { return sessionStorage.getItem(TOKEN_KEY) || ''; } catch (_) { return ''; }
}
export function setToken(t) {
  try {
    if (t) sessionStorage.setItem(TOKEN_KEY, t);
    else   sessionStorage.removeItem(TOKEN_KEY);
  } catch (_) { /* sessionStorage may be disabled */ }
}
export function clearToken() {
  try { sessionStorage.removeItem(TOKEN_KEY); } catch (_) {}
}
function getCachedCsrf() {
  try { return sessionStorage.getItem(CSRF_KEY) || ''; } catch (_) { return ''; }
}
function setCachedCsrf(t) {
  try {
    if (t) sessionStorage.setItem(CSRF_KEY, t);
    else   sessionStorage.removeItem(CSRF_KEY);
  } catch (_) {}
}

function authHeaders() {
  const t = getToken();
  const h = { 'Accept': 'application/json' };
  if (t) h['Authorization'] = 'Bearer ' + t;
  return h;
}

// ---------- csrf refresh -----------------------------------------
async function refreshCsrf() {
  const resp = await fetch('/api/v1/csrf-token', {
    method: 'GET',
    headers: authHeaders(),
    credentials: 'same-origin',
  });
  if (!resp.ok) {
    setCachedCsrf('');
    return '';
  }
  let body = null;
  try { body = await resp.json(); } catch (_) {}
  const tok = (body && body.csrf_token) || '';
  setCachedCsrf(tok);
  return tok;
}

async function ensureCsrf() {
  let tok = getCachedCsrf();
  if (tok) return tok;
  return refreshCsrf();
}

// ---------- core fetch with retry on 403 -------------------------
async function rawFetch(url, init) {
  const resp = await fetch(url, init);
  return resp;
}

function wantsCsrf(method) {
  return method === 'POST' || method === 'PUT' || method === 'PATCH' || method === 'DELETE';
}

async function parseError(resp) {
  let body = null;
  try { body = await resp.json(); } catch (_) {}
  const code = body && (body.code || body.error_code) || '';
  const msg  = body && (body.message || body.error) || resp.statusText || 'request failed';
  const fields = body && body.fields && body.fields || null;
  return new ApiError(resp.status, code, msg, fields);
}

/**
 * csrfFetch is the canonical caller. It:
 *   - sends Auth + CSRF headers automatically
 *   - retries exactly once on a 403 with code 'csrf_invalid'
 *   - throws a normalized ApiError on every non-2xx
 */
export async function csrfFetch(url, init = {}, opts = {}) {
  const method = (init.method || 'GET').toUpperCase();
  const headers = Object.assign({}, authHeaders(), init.headers || {});
  if (!('Content-Type' in headers) && init.body && typeof init.body === 'string') {
    headers['Content-Type'] = 'application/json';
  }
  if (wantsCsrf(method)) {
    const tok = await ensureCsrf();
    if (tok) headers['X-CSRF-Token'] = tok;
  }
  const resp = await rawFetch(url, Object.assign({}, init, {
    method,
    headers,
    credentials: 'same-origin',
  }));

  // CSRF retry path.
  if (resp.status === 403 && wantsCsrf(method)) {
    let body = null;
    try { body = await resp.clone().json(); } catch (_) {}
    const code = body && body.code;
    if (code === 'csrf_invalid' || code === 'csrf_required') {
      const fresh = await refreshCsrf();
      const retryHeaders = Object.assign({}, headers);
      if (fresh) retryHeaders['X-CSRF-Token'] = fresh;
      const retry = await rawFetch(url, Object.assign({}, init, {
        method,
        headers: retryHeaders,
        credentials: 'same-origin',
      }));
      return consume(retry, opts);
    }
  }
  return consume(resp, opts);
}

async function consume(resp, opts) {
  if (resp.status === 204) return null;
  if (resp.status >= 200 && resp.status < 300) {
    const ct = resp.headers.get('content-type') || '';
    if (ct.indexOf('application/json') >= 0) {
      try { return await resp.json(); } catch (_) { return null; }
    }
    return await resp.text();
  }
  // Body may already be consumed by the CSRF-retry path; clone-aware fallback.
  throw await parseError(resp);
}

// ---------- shorthand methods ------------------------------------
export function apiGet(url, opts) { return csrfFetch(url, { method: 'GET' }, opts); }
export function apiPost(url, body, opts) {
  return csrfFetch(url, { method: 'POST', body: typeof body === 'string' ? body : JSON.stringify(body || {}) }, opts);
}
export function apiPut(url, body, opts) {
  return csrfFetch(url, { method: 'PUT', body: typeof body === 'string' ? body : JSON.stringify(body || {}) }, opts);
}
export function apiPatch(url, body, opts) {
  return csrfFetch(url, { method: 'PATCH', body: typeof body === 'string' ? body : JSON.stringify(body || {}) }, opts);
}
export function apiDelete(url, opts) { return csrfFetch(url, { method: 'DELETE' }, opts); }

// ---------- login / logout (auth boundaries) ---------------------
export async function login(email, password, mfaCode) {
  const resp = await fetch('/api/v1/auth/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
    body: JSON.stringify({ email, password, mfa_code: mfaCode || '' }),
  });
  const data = await resp.json().catch(() => null);
  if (!resp.ok) {
    throw new ApiError(resp.status, data && data.code || '', data && (data.message || data.error) || 'login failed');
  }
  if (!data || !data.access_token) {
    throw new ApiError(resp.status, '', 'login response missing access_token');
  }
  setToken(data.access_token);
  if (data.refresh_token) {
    try { sessionStorage.setItem('orvix_admin_refresh', data.refresh_token); } catch (_) {}
  }
  // Pre-fetch a CSRF so the first mutating call doesn't have to.
  refreshCsrf().catch(() => {});
  return data;
}

export async function logout() {
  try { await csrfFetch('/api/v1/auth/logout', { method: 'POST' }); } catch (_) {}
  clearToken();
  setCachedCsrf('');
  try { sessionStorage.removeItem('orvix_admin_refresh'); } catch (_) {}
}

export function isAuthenticated() { return !!getToken(); }
