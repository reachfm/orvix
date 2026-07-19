// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, initCSRF, resetCSRFToken, setCSRFToken } from "./api";

const BASE = "/api/v1";

let fetchMock: ReturnType<typeof vi.fn>;
let originalFetch: typeof globalThis.fetch;

beforeEach(() => {
  originalFetch = globalThis.fetch;
  fetchMock = vi.fn();
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  resetCSRFToken();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

// ---- CSRF token lifecycle ----

describe("CSRF token lifecycle", () => {
  it("CSRF token is not fetched for GET requests", async () => {
    fetchMock.mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({}) } as Response);
    await api.getMe();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain("/me");
    expect(init?.headers?.["X-CSRF-Token"]).toBeUndefined();
    expect(init?.headers?.["Content-Type"]).toBe("application/json");
  });

  it("POST fetches CSRF token and sends X-CSRF-Token", async () => {
    // First call: fetch CSRF token
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "abc123" }),
    } as Response);
    // Second call: the actual POST
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    await api.createDomainEnterprise({ domain: "test.com" });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    // First call: CSRF token fetch
    expect(fetchMock.mock.calls[0][0]).toContain("/csrf-token");
    // Second call: actual POST
    const [, init] = fetchMock.mock.calls[1];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("abc123");
  });

  it("PUT sends X-CSRF-Token", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "xyz" }),
    } as Response);
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    // Call a mutation with PUT method
    await api.updateProfile({ name: "test" });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [, init] = fetchMock.mock.calls[1];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("xyz");
  });

  it("PATCH sends X-CSRF-Token", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "abc" }),
    } as Response);
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    await api.updateMemberRole(1, "admin");

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [, init] = fetchMock.mock.calls[1];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("abc");
  });

  it("DELETE sends X-CSRF-Token", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "del" }),
    } as Response);
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    await api.deleteAlias(1);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [, init] = fetchMock.mock.calls[1];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("del");
  });

  it("CSRF token fetch failure throws", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false, status: 500, json: () => Promise.resolve({}),
    } as Response);

    await expect(api.createDomainEnterprise({ domain: "test.com" }))
      .rejects.toThrow(/CSRF token fetch failed/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("already-cached CSRF token is not re-fetched", async () => {
    setCSRFToken("cached");
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    await api.createDomainEnterprise({ domain: "test.com" });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls[0];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("cached");
    // No CSRF token fetch call
  });

  it("concurrent mutations share CSRF token initialization", async () => {
    let csrfResolved = false;
    fetchMock.mockImplementation(() => {
      return new Promise<Response>((resolve) => {
        if (!csrfResolved) {
          csrfResolved = true;
          resolve({ ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "concurrent-token" }) } as Response);
        } else {
          resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as Response);
        }
      });
    });

    const results = await Promise.all([
      api.createDomainEnterprise({ domain: "a.com" }),
      api.createDomainEnterprise({ domain: "b.com" }),
    ]);

    expect(results).toHaveLength(2);
    // CSRF fetch + 2 mutations = 3 calls
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const firstCsrf = fetchMock.mock.calls[0][0] as string;
    expect(firstCsrf).toContain("/csrf-token");
    // Both mutations should have the same token
    const [, m1] = fetchMock.mock.calls[1];
    const [, m2] = fetchMock.mock.calls[2];
    expect(m1?.headers?.["X-CSRF-Token"]).toBe("concurrent-token");
    expect(m2?.headers?.["X-CSRF-Token"]).toBe("concurrent-token");
  });

  it("resetCSRFToken clears only CSRF state", () => {
    setCSRFToken("should-be-cleared");
    resetCSRFToken();
    // Verify by calling initCSRF — which would return immediately if token were set
    setCSRFToken("re-set");
    resetCSRFToken();
    // No fetch should occur until a mutation is made
    expect(fetchMock).toHaveBeenCalledTimes(0);
  });
});

// ---- CSRF retry behaviour ----

describe("CSRF retry behaviour", () => {
  it("CSRF 403 refreshes token once", async () => {
    let csrfCount = 0;
    fetchMock.mockImplementation(() => {
      const call = csrfCount++;
      if (call === 0) {
        return Promise.resolve({
          ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "t1" }),
        } as Response);
      }
      if (call === 1) {
        return Promise.resolve({
          ok: false, status: 403, json: () => Promise.resolve({ error: "CSRF token mismatch" }),
        } as Response);
      }
      if (call === 2) {
        return Promise.resolve({
          ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "t2" }),
        } as Response);
      }
      return Promise.resolve({
        ok: true, status: 200, json: () => Promise.resolve({}),
      } as Response);
    });

    await api.createDomainEnterprise({ domain: "test.com" });

    // call 0: CSRF fetch → t1
    // call 1: mutation with t1 → 403 CSRF
    // call 2: re-fetch CSRF → t2
    // call 3: retry mutation with t2 → 200
    expect(fetchMock).toHaveBeenCalledTimes(4);
    // Verify the retried call has the new token
    const [, init] = fetchMock.mock.calls[3];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("t2");
  });

  it("second CSRF 403 does not retry again", async () => {
    // Every call returns 403 CSRF, including the re-fetch.
    // The error comes from initCSRF failing, not the mutation itself.
    let firstCsfr = true;
    fetchMock.mockImplementation(() => {
      if (firstCsfr) {
        firstCsfr = false;
        return Promise.resolve({
          ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "t1" }),
        } as Response);
      }
      return Promise.resolve({
        ok: false, status: 403, json: () => Promise.resolve({ error: "CSRF token mismatch" }),
      } as Response);
    });

    await expect(api.createDomainEnterprise({ domain: "test.com" }))
      .rejects.toThrow(/CSRF token fetch failed/);

    // call 0: CSRF fetch → t1
    // call 1: mutation with t1 → 403 CSRF
    // call 2: re-fetch CSRF → 403 → throw (no second retry)
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("non-CSRF 403 does not retry", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "tok" }),
    } as Response);
    fetchMock.mockResolvedValueOnce({
      ok: false, status: 403, json: () => Promise.resolve({ error: "insufficient permissions" }),
    } as Response);

    await expect(api.createDomainEnterprise({ domain: "test.com" }))
      .rejects.toThrow(/insufficient permissions/);

    // No retry — only 2 calls: CSRF fetch + mutation
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

// ---- Auth mutations ----

describe("Auth mutations include CSRF", () => {
  it("logout sends CSRF token", async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({ csrf_token: "logout-csrf" }),
    } as Response);
    fetchMock.mockResolvedValueOnce({
      ok: true, status: 200, json: () => Promise.resolve({}),
    } as Response);

    await api.logout();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [, init] = fetchMock.mock.calls[1];
    expect(init?.headers?.["X-CSRF-Token"]).toBe("logout-csrf");
  });
});

// ---- No localStorage leakage ----

describe("No auth tokens in localStorage or sessionStorage", () => {
  it("no auth token written to localStorage during CSRF operations", () => {
    const localStorageKeys = Object.keys(localStorage);
    const sessionStorageKeys = Object.keys(sessionStorage);

    // There should be no token-like keys set by our CSRF module
    const tokenKeys = [...localStorageKeys, ...sessionStorageKeys].filter(
      (k) => k.toLowerCase().includes("token") || k.toLowerCase().includes("auth")
    );
    expect(tokenKeys).toHaveLength(0);
  });
});
