/* =====================================================================
   modules/state.js — Shared global state for the admin shell.

   This is a thin wrapper around a plain object. Page modules read
   profile, settings, runtime, etc. via the getters and set them
   with the setters; the wrapper makes invalidation easy when (for
   example) a settings PATCH succeeds and every page must re-read.
   ===================================================================== */

const _state = {
  // Profile from /api/v1/me.
  profile: null,
  // Admin settings (read from /api/v1/admin/settings). Cached for the
  // life of the session. Pages call reloadSettings() to force-refresh.
  settings: null,
  settingsLoadedAt: 0,
  // Runtime telemetry (read from /api/v1/admin/runtime).
  runtime: null,
  runtimeLoadedAt: 0,
  // License summary (read from /api/v1/license).
  license: null,
  licenseLoadedAt: 0,
  // Build metadata (read from /api/v1/health or summary).
  build: null,
  // Last seen non-2xx response for the global banner.
  lastBanner: null,
  // Current locale, set by i18n.
  locale: 'en',
  // Network status: a simple counter of in-flight fetches.
  inflight: 0,
};

// ---------- profile -------------------------------------------
export function getProfile()  { return _state.profile; }
export function setProfile(p) { _state.profile = p || {}; }

// ---------- settings -------------------------------------------
export function getSettings() { return _state.settings; }
export function setSettings(s) {
  _state.settings = s || null;
  _state.settingsLoadedAt = Date.now();
}
export function settingsLoadedAt() { return _state.settingsLoadedAt; }

// ---------- runtime --------------------------------------------
export function getRuntime() { return _state.runtime; }
export function setRuntime(r) {
  _state.runtime = r || null;
  _state.runtimeLoadedAt = Date.now();
}
export function runtimeLoadedAt() { return _state.runtimeLoadedAt; }

// ---------- license --------------------------------------------
export function getLicense() { return _state.license; }
export function setLicense(l) {
  _state.license = l || null;
  _state.licenseLoadedAt = Date.now();
}

// ---------- build ----------------------------------------------
export function getBuild()   { return _state.build; }
export function setBuild(b)  { _state.build = b || null; }

// ---------- locale ---------------------------------------------
export function getLocale()       { return _state.locale; }
export function setLocale(loc)    { _state.locale = loc || 'en'; }

// ---------- in-flight counter (for topbar status) -------------
export function inflightBegin()  { _state.inflight += 1; document.dispatchEvent(new CustomEvent('orvix:inflight', { detail: _state.inflight })); }
export function inflightEnd()    { _state.inflight = Math.max(0, _state.inflight - 1); document.dispatchEvent(new CustomEvent('orvix:inflight', { detail: _state.inflight })); }
export function getInflight()    { return _state.inflight; }

// ---------- banner ---------------------------------------------
export function setBanner(msg)    { _state.lastBanner = msg || null; document.dispatchEvent(new CustomEvent('orvix:banner', { detail: msg })); }
export function getBanner()      { return _state.lastBanner; }
