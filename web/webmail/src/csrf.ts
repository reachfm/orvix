// CSRF support for the webmail SPA.
//
// H-1: every state-changing /api/v1/webmail/* route now requires the
// double-submit CSRF token (canonical middleware, same as the admin console).
// This mirrors web/admin/src/api.ts's approach: fetch a token once from the
// public bootstrap endpoint, cache it, send it in X-CSRF-Token on every
// mutation, and refresh once if the server reports the token stale.
//
// The token is never placed in a URL, never logged, and never persisted to
// localStorage/sessionStorage — it lives only in this module's closure for the
// lifetime of the page, alongside the cookie the server sets.

let csrfTokenValue = "";
let csrfTokenPromise: Promise<string> | null = null;

const CSRF_URL = "/api/v1/csrf-token";

export async function initCSRF(): Promise<string> {
  if (csrfTokenValue) return csrfTokenValue;
  if (csrfTokenPromise) return csrfTokenPromise;

  csrfTokenPromise = (async () => {
    const res = await fetch(CSRF_URL, { credentials: "include" });
    if (!res.ok) {
      csrfTokenPromise = null;
      throw new Error(`CSRF token fetch failed: ${res.status}`);
    }
    const data = await res.json();
    csrfTokenValue = data.csrf_token || "";
    csrfTokenPromise = null;
    return csrfTokenValue;
  })();

  return csrfTokenPromise;
}

export function resetCSRFToken(): void {
  csrfTokenValue = "";
  csrfTokenPromise = null;
}

function isMutation(method: string): boolean {
  const m = method.toUpperCase();
  return m !== "GET" && m !== "HEAD" && m !== "OPTIONS";
}

/**
 * apiFetch is the single entry point for webmail API calls. It always sends
 * credentials, and for mutations it attaches the CSRF header and retries once
 * with a fresh token if the server rejects a stale one.
 *
 * Mutations always declare application/json (unless the caller supplies a body
 * that must be multipart, e.g. attachment upload) because the server rejects
 * form-ish content types with 415 — that is what stops a cross-site <form>
 * from reaching a handler.
 */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method || "GET").toUpperCase();
  const headers = new Headers(init.headers || {});

  const bodyIsFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
  if (isMutation(method) && !bodyIsFormData && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  if (isMutation(method)) {
    const token = await initCSRF();
    if (token) headers.set("X-CSRF-Token", token);
  }

  const res = await fetch(path, { ...init, headers, credentials: "include" });

  // A 403 on a mutation may mean the cached token expired or was rotated.
  // Refresh once and replay; never loop.
  if (res.status === 403 && isMutation(method) && !(init as { _csrfRetried?: boolean })._csrfRetried) {
    resetCSRFToken();
    const token = await initCSRF();
    if (token) headers.set("X-CSRF-Token", token);
    return fetch(path, {
      ...init,
      headers,
      credentials: "include",
      ...({ _csrfRetried: true } as Record<string, unknown>),
    });
  }

  return res;
}
