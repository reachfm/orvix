/* =====================================================================
   modules/api.js — HTTP client with cookie auth + CSRF + error normalization.

   Browser authentication is cookie-based. The admin UI does not store or
   read bearer tokens from localStorage or sessionStorage. The server sets
   HttpOnly cookies, including the preferred __Host-orvix_session session
   cookie, and JavaScript only fetches a CSRF value for write requests.
   ===================================================================== */

let cachedCsrf = '';

export class ApiError extends Error {
  constructor(status, code, message, fields) {
    super(message || code || 'request failed');
    this.name    = 'ApiError';
    this.status  = status || 0;
    this.code    = code   || '';
    this.fields  = fields || null;
  }
}

function authHeaders() {
  return { 'Accept': 'application/json' };
}

function getCachedCsrf() { return cachedCsrf || ''; }
function setCachedCsrf(t) { cachedCsrf = t || ''; }

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
  const tok = (body && (body.csrf_token || body.token)) || '';
  setCachedCsrf(tok);
  return tok;
}

async function ensureCsrf() {
  const tok = getCachedCsrf();
  if (tok) return tok;
  return refreshCsrf();
}

async function rawFetch(url, init) {
  try {
    return await fetch(url, init);
  } catch (err) {
    throw new ApiError(0, 'network_error', (err && err.message) || 'network error');
  }
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
 *   - sends same-origin cookies automatically
 *   - sends X-CSRF-Token for POST/PUT/PATCH/DELETE
 *   - retries exactly once on a CSRF-specific 403
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
  throw await parseError(resp);
}

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
export function apiDelete(url, opts) {
  const init = { method: 'DELETE' };
  if (opts) {
    if (opts.headers) init.headers = opts.headers;
    if (opts.body) init.body = opts.body;
  }
  return csrfFetch(url, init, opts);
}

async function postAuth(url, body) {
  const resp = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
    body: JSON.stringify(body || {}),
  });
  const data = await resp.json().catch(() => null);
  if (!resp.ok) {
    throw new ApiError(resp.status, data && data.code || '', data && (data.message || data.error) || 'login failed');
  }
  return data || {};
}

export async function login(email, password, mfaCode, mfaChallenge) {
  let data;
  if (mfaChallenge) {
    data = await postAuth('/api/v1/auth/mfa/verify', { mfa_challenge: mfaChallenge, code: mfaCode || '' });
  } else {
    data = await postAuth('/api/v1/auth/login', { email, password });
  }
  if (data && data.mfa_required) {
    const err = new ApiError(401, 'mfa_required', 'mfa required');
    err.challenge = data.mfa_challenge || '';
    err.expiresIn = data.mfa_expires_in || 0;
    throw err;
  }
  refreshCsrf().catch(() => {});
  return data;
}

export async function logout() {
  try { await csrfFetch('/api/v1/auth/logout', { method: 'POST' }); } catch (_) {}
  setCachedCsrf('');
}

export function isAuthenticated() { return true; }
