import { test, expect } from "@playwright/test";
import { spawn, execFileSync, ChildProcess } from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import * as crypto from "crypto";
import * as http from "http";

const ADMIN_EMAIL = "admin@e2e-test.local";
const ADMIN_PASSWORD = "E2eTestPass123!";
const TENANT_ADMIN_EMAIL = "tenant-admin@portal-e2e-tenant.test";
const TENANT_ADMIN_PASSWORD = "PortalE2eTenantAdmin123!";

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

let serverProcess: ChildProcess | null = null;
let adminPort: number;
let tempDir: string;
let configPath: string;

function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = require("net").createServer();
    srv.listen(0, "127.0.0.1", () => {
      const port = (srv.address() as any).port;
      srv.close(() => resolve(port));
    });
    srv.on("error", reject);
  });
}

function waitForHealth(url: string, timeoutMs: number): Promise<void> {
  const start = Date.now();
  return new Promise((resolve, reject) => {
    const check = () => {
      if (Date.now() - start > timeoutMs) {
        return reject(new Error(`Health check timed out after ${timeoutMs}ms`));
      }
      const req = http.get(url, (res) => {
        res.resume();
        if (res.statusCode === 200) resolve();
        else setTimeout(check, 500);
      });
      req.on("error", () => setTimeout(check, 500));
      req.setTimeout(2000, () => { req.destroy(); setTimeout(check, 500); });
    };
    check();
  });
}

test.beforeAll(async () => {
  adminPort = await findFreePort();
  tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "orvix-e2e-"));

  const adminUiDir = path.resolve(__dirname, "../../web/admin/dist");
  if (!fs.existsSync(adminUiDir)) {
    throw new Error(`Admin UI dist not found at ${adminUiDir}`);
  }
  const ext = process.platform === "win32" ? ".exe" : "";
  const binaryPath = path.resolve(__dirname, `../../orvix${ext}`);
  if (!fs.existsSync(binaryPath)) {
    throw new Error(`orvix binary not found at ${binaryPath}`);
  }

  const sqliteFile = path.join(tempDir, "orvix.db");
  const dsn = sqliteFile + "?_loc=auto&_busy_timeout=5000&_txlock=immediate";
  const jwtKeyPath = path.join(tempDir, "jwt_key.pem");

  // Write a standalone YAML config so the project's orvix.yaml is never read
  const yaml = `server:
  host: "127.0.0.1"
  admin_port: ${adminPort}
  admin_ui_dir: "${adminUiDir.replace(/\\/g, "/")}"
  allowed_origins: ["*"]
database:
  driver: sqlite
  dsn: "${dsn.replace(/\\/g, "/")}"
auth:
  jwt_key_path: "${jwtKeyPath.replace(/\\/g, "/")}"
logging:
  level: error
  format: console
coremail:
  enabled: false
redis:
  host: ""
`;
  configPath = path.join(tempDir, "orvix.yaml");
  fs.writeFileSync(configPath, yaml, "utf-8");

  const env: Record<string, string> = {
    ORVIX_CONFIG: configPath,
    ORVIX_ADMIN_EMAIL: ADMIN_EMAIL,
    ORVIX_ADMIN_PASSWORD_B64: Buffer.from(ADMIN_PASSWORD).toString("base64"),
  };

  serverProcess = spawn(binaryPath, [], { env, stdio: ["ignore", "pipe", "pipe"] });
  serverProcess.stdout?.on("data", (d: Buffer) => process.stdout.write(`[orvix] ${d}`));
  serverProcess.stderr?.on("data", (d: Buffer) => process.stderr.write(`[orvix:err] ${d}`));

  await waitForHealth(`http://127.0.0.1:${adminPort}/api/v1/health`, 45000);

  // Seed a genuine tenant_admin fixture for the Organization-portal test.
  // There is currently no supported non-SQL production API to bootstrap
  // the FIRST tenant_admin of a brand-new tenant (POST /api/v1/auth/signup
  // always creates plain RoleUser — see seed-fixture/main.go's doc comment
  // for the full rationale). This seeds directly into this test run's own
  // disposable temp-file SQLite database, the same hermetic-fixture
  // pattern internal/api/router_test.go and
  // cmd/orvix/admin_recovery_test.go already use for their own ephemeral
  // databases — never a live, production, or VPS database.
  execFileSync(
    "go",
    ["run", "./seed-fixture", dsn, TENANT_ADMIN_EMAIL, TENANT_ADMIN_PASSWORD, "Portal E2E Tenant"],
    { cwd: __dirname, stdio: "inherit" },
  );
});

test.afterAll(async () => {
  if (serverProcess && !serverProcess.killed) {
    serverProcess.kill();
    await new Promise((r) => setTimeout(r, 2000));
    try { serverProcess.kill(); } catch { /* ignore */ }
  }
  try {
    if (tempDir) fs.rmSync(tempDir, { recursive: true, force: true });
  } catch { /* ignore */ }
});

test.describe("Orvix admin portal E2E", () => {
  // beforeAll may wait up to 45s for health; allow 60s total.
  test.describe.configure({ timeout: 60000 });

  test("login and navigate dashboard and customer portal sections", async ({ browser, request }) => {
    // PORTAL-SEPARATION-PHASE1 / PLATFORM-SHELL: this exercises the
    // Organization portal (portal="organization") as a genuine
    // tenant_admin, seeded in beforeAll (see seed-fixture/main.go — there
    // is currently no supported non-SQL production API to provision the
    // first tenant_admin of a brand-new tenant). A PLAIN signed-up
    // RoleUser (POST /api/v1/auth/signup) is intentionally NOT used here
    // any more: /api/v1/me correctly returns portal="" for that role
    // (handlers.go's Me handler has no case for RoleUser), and the
    // frontend now correctly fails closed for portal="" instead of
    // showing the Customer Portal shell to an unauthorized role — see
    // the "signed-up plain user fails closed" test below for that
    // contract.
    const loginRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/login`, {
      data: { email: TENANT_ADMIN_EMAIL, password: TENANT_ADMIN_PASSWORD },
    });
    expect(loginRes.ok()).toBeTruthy();
    const loginBody = await loginRes.json();
    expect(loginBody.access_token).toBeTruthy();
    const accessToken: string = loginBody.access_token;

    const context = await browser.newContext({ bypassCSP: true });
    const page = await context.newPage();

    // Intercept every /api/v1/* call to inject the Bearer token
    // so the SPA's credentials: include pattern works over HTTP
    // where Secure cookies are not sent by the browser.
    await page.route("**/api/v1/**", async (route) => {
      const headers = {
        ...route.request().headers(),
        Authorization: `Bearer ${accessToken}`,
      };
      await route.continue({ headers });
    });

    // Navigate to admin UI
    await page.goto(`http://127.0.0.1:${adminPort}/admin`);
    await page.waitForLoadState("networkidle");

    // Verify dashboard loads with the correct heading in the main content area.
    const mainContent = page.locator("main");
    await expect(mainContent.locator("h2").filter({ hasText: "Dashboard" })).toBeVisible();

    // Navigate to each Customer Portal section and verify page renders.
    // The sidebar has ambiguous button labels (e.g. "Organizations" + "Organization",
    // admin "Mailboxes" + customer "Mailboxes"), so we use exact text matching
    // where possible and .last() for labels that appear twice under the same name.
    const portalSections: { text: string; heading: string | RegExp }[] = [
      { text: "Organization", heading: "Organization" },
      { text: "Mailboxes", heading: "Mailboxes" },
      { text: "Aliases", heading: "Email Aliases" },
      { text: "Groups", heading: "Groups" },
      { text: "Usage", heading: "Usage & Quotas" },
      // The "Domain Setup" / "Domain Onboarding" section was REMOVED
      // deliberately: it was a second, inferior copy of the DNS record UI
      // that read response fields the API never returned, so every record
      // rendered as "pending". DNS record management now lives solely in the
      // admin Domains console, whose per-domain "DNS" action opens the
      // canonical DNS Records modal (covered by
      // web/admin/tests/e2e/domains-dns-console.spec.ts). Asserting the
      // deleted section here would only pin a UI that no longer exists.
      { text: "Invitations", heading: "Invitations" },
      { text: "Members", heading: "Members & Roles" },
      { text: "Ownership", heading: "Ownership Transfer" },
      { text: "Status", heading: "Organization Status" },
      { text: "Billing", heading: "Billing & Subscription" },
      { text: "API Keys", heading: "API Keys" },
      { text: "Invoices", heading: "Invoices" },
      { text: "Security", heading: "Security" },
      { text: "Support", heading: "Support" },
      { text: "Preferences", heading: "Preferences" },
    ];

    for (const section of portalSections) {
      // Pick the button whose trimmed text exactly matches
      const btn = page.locator("aside button").filter({ hasText: new RegExp(`^\\s*${escapeRegex(section.text)}\\s*$`) });
      // If there are multiple exact matches (e.g. two "Mailboxes"), take the last one
      // which is the customer portal one (appears after the admin one in DOM order).
      const count = await btn.count();
      const target = count > 1 ? btn.last() : btn.first();
      await target.scrollIntoViewIfNeeded();
      await target.click();
      await page.waitForTimeout(500);
      await page.waitForLoadState("networkidle");

      const heading = page.locator("h2").filter({ hasText: section.heading });
      await expect(heading.first()).toBeVisible();
    }

    // Verify sidebar still shows "Orvix Admin" after all navigation
    await expect(page.getByText("Orvix Admin")).toBeVisible();

    // Tenant Admin in Dark mode — reuses this test's already-
    // authenticated session (no new /auth/login call: the no-Redis
    // fallback login limiter is a flat 5-per-15min counter across the
    // whole file, internal/api/router.go's `limiter.New(Max:5,...)`,
    // and does not reset on success the way the Redis path's
    // ResetLoginLimit does — every additional standalone login test
    // in this file consumes irreplaceable budget from that same
    // per-IP counter).
    // A passive listener, not a new page.route() — a second route
    // handler for the same pattern would shadow the Authorization-
    // header-injecting handler registered above and break auth on
    // every subsequent request.
    const requestedPathsDark: string[] = [];
    page.on("request", (req) => {
      const u = new URL(req.url());
      if (u.pathname.startsWith("/api/v1/")) requestedPathsDark.push(u.pathname);
    });
    const darkConsoleErrors: string[] = [];
    page.on("console", (msg) => { if (msg.type() === "error") darkConsoleErrors.push(msg.text()); });

    await page.evaluate(() => window.localStorage.setItem("orvix-admin-theme", "dark"));
    await page.reload();
    await page.waitForLoadState("networkidle");

    const hasDarkClass = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(hasDarkClass).toBe(true);
    await expect(page.locator("main").locator("h2").filter({ hasText: "Dashboard" })).toBeVisible();
    // Tenant Admin never sees the Platform Administration shell, in
    // either theme.
    await expect(page.getByText("Platform Administration")).toHaveCount(0);
    if (darkConsoleErrors.length) throw new Error(`console errors for Tenant Admin in dark theme: ${darkConsoleErrors.join(" | ")}`);
    const platformOnlySuffixes = [
      "/platform/dashboard", "/platform/organizations", "/admin/backups", "/admin/queue/summary",
      "/admin/security/antivirus", "/guardian/logs", "/heal/history", "/admin/log-rules",
      "/admin/settings", "/feature-flags", "/monitoring/alerts", "/admin/storage/volumes", "/admin/cluster/status",
    ];
    for (const suffix of platformOnlySuffixes) {
      expect(requestedPathsDark.some((p) => p.endsWith(suffix))).toBe(false);
    }
  });

  test("platform super admin gets the Platform Administration shell, never the Customer Portal", async ({ browser, request }) => {
    // PLATFORM-SHELL: ADMIN_EMAIL is the env-var bootstrap identity and is
    // canonically platform_super_admin with tenant_id=NULL (see the
    // comment on the test above). Before this fix its /me.portal="platform"
    // response was ignored by the frontend, which still rendered the
    // Customer Portal shell and called the tenant-scoped dashboard
    // endpoint — a NULL-tenant identity the backend correctly rejects,
    // producing "Failed to load dashboard". This test proves the fixed
    // contract end-to-end against the real server/build.
    const loginRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/login`, {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(loginRes.ok()).toBeTruthy();
    const loginBody = await loginRes.json();
    expect(loginBody.access_token).toBeTruthy();
    const accessToken: string = loginBody.access_token;

    const context = await browser.newContext({ bypassCSP: true });
    const page = await context.newPage();

    const requestedPaths: string[] = [];
    await page.route("**/api/v1/**", async (route) => {
      requestedPaths.push(new URL(route.request().url()).pathname);
      const headers = {
        ...route.request().headers(),
        Authorization: `Bearer ${accessToken}`,
      };
      await route.continue({ headers });
    });

    await page.goto(`http://127.0.0.1:${adminPort}/admin`);
    await page.waitForLoadState("networkidle");

    // Platform Administration shell renders.
    const mainContent = page.locator("main");
    await expect(mainContent.locator("h2").filter({ hasText: "Platform Administration" })).toBeVisible();

    // Customer Portal navigation must never appear for this identity.
    await expect(page.locator("aside").getByText("Customer Portal")).toHaveCount(0);

    // No "Failed to load dashboard" error anywhere on the landing page.
    await expect(page.getByText("Failed to load dashboard")).toHaveCount(0);

    // Zero requests to the tenant-owned dashboard/domain/mailbox endpoints
    // during bootstrap and landing render.
    for (const suffix of ["/enterprise/dashboard", "/enterprise/domains", "/enterprise/mailboxes", "/users"]) {
      expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
    }

    // PLATFORM-SHELL-2: the complete restored platform navigation set.
    // Click every visible item sequentially, assert a stable heading, and
    // reject console errors / failed API responses / tenant-owned calls.
    // The E2E harness runs with coremail.enabled=false, so the Mail
    // Operations page's queue calls correctly receive a 503
    // {"code":"COREMAIL_DISABLED"} contract (see internal/api/handlers
    // admin_queue.go's coreMailUnavailableResponse) and the page renders
    // a graceful "CoreMail is disabled" state for it. Chromium still logs
    // that fetch as "Failed to load resource" to both the console and the
    // response stream — that is expected, correct fail-closed behavior,
    // not a bug, so it is the one 503 excluded from the error assertions
    // below. Any other 4xx/5xx, or a 503 without that exact code, still
    // fails the test.
    // Chromium's built-in "Failed to load resource: ... 503" console message
    // is excluded from the console-error check unconditionally here, but the
    // failedResponses handler below independently re-verifies every such 503
    // actually carries the COREMAIL_DISABLED contract on a /queue path — if
    // it doesn't, it is pushed to failedResponses and still fails the test.
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() !== "error") return;
      if (/status of 503 \(Service Unavailable\)/.test(msg.text())) return;
      consoleErrors.push(msg.text());
    });
    const failedResponses: string[] = [];
    page.on("response", async (res) => {
      const pathname = new URL(res.url()).pathname;
      if (res.status() < 400 || !pathname.startsWith("/api/")) return;
      if (res.status() === 503 && pathname.includes("/queue")) {
        try {
          const body = await res.json();
          if (body?.code === "COREMAIL_DISABLED") return;
        } catch { /* fall through to treat as a genuine failure */ }
      }
      failedResponses.push(`${res.status()} ${pathname}`);
    });

    const platformNav: { label: string; heading: string | RegExp }[] = [
      { label: "Organizations", heading: /organizations/i },
      { label: "Summary", heading: "Platform Summary" },
      { label: "Mail Operations", heading: /mail operations/i },
      { label: "Reliability", heading: /reliability/i },
      { label: "Health", heading: /health|runtime|system/i },
      { label: "Security", heading: /security/i },
      { label: "Modules", heading: /modules/i },
      { label: "Configuration", heading: /configuration/i },
    ];
    for (const item of platformNav) {
      const btn = page.locator("aside button").filter({ hasText: new RegExp(`^\\s*${escapeRegex(item.label)}\\s*$`) });
      await btn.first().scrollIntoViewIfNeeded();
      await btn.first().click();
      await page.waitForLoadState("networkidle");
      const heading = typeof item.heading === "string"
        ? page.getByText(item.heading)
        : page.locator("main").getByText(item.heading);
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText("Failed to load dashboard")).toHaveCount(0);
    }

    // No console errors, no failed API responses, and still zero requests
    // to any tenant-owned endpoint across the entire navigation sweep.
    if (consoleErrors.length) throw new Error(`console errors during platform nav sweep: ${consoleErrors.join(" | ")}`);
    if (failedResponses.length) throw new Error(`failed API responses during platform nav sweep: ${failedResponses.join(" | ")}`);
    for (const suffix of ["/enterprise/dashboard", "/enterprise/domains", "/enterprise/mailboxes", "/users"]) {
      expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
    }

    // Direct deep-link refresh (full page reload) preserves the Platform
    // shell — no flash of the wrong portal, no Customer Portal nav.
    await page.reload({ waitUntil: "networkidle" });
    await expect(page.locator("aside").getByText("Customer Portal")).toHaveCount(0);
    await expect(page.getByText("Failed to load dashboard")).toHaveCount(0);

    // PSA in Dark mode — reuses this test's already-authenticated
    // session rather than a fresh /auth/login (see the Tenant Admin
    // Dark-mode comment above on the no-Redis login limiter's flat,
    // non-resetting 5-per-15min-per-IP budget across this whole file).
    // A representative subset, not the full nav — the full sweep
    // already ran above in Light mode.
    const darkConsoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() !== "error") return;
      if (/status of 503 \(Service Unavailable\)/.test(msg.text())) return;
      darkConsoleErrors.push(msg.text());
    });
    await page.evaluate(() => window.localStorage.setItem("orvix-admin-theme", "dark"));
    await page.reload();
    await page.waitForLoadState("networkidle");
    const hasDarkClass = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(hasDarkClass).toBe(true);
    await expect(page.locator("main").locator("h2").filter({ hasText: "Platform Administration" })).toBeVisible();
    await expect(page.locator("aside").getByText("Customer Portal")).toHaveCount(0);

    const darkNavSubset: { label: string; heading: string | RegExp }[] = [
      { label: "Organizations", heading: /organizations/i },
      { label: "Security", heading: /security/i },
    ];
    for (const item of darkNavSubset) {
      const btn = page.locator("aside button").filter({ hasText: new RegExp(`^\\s*${escapeRegex(item.label)}\\s*$`) });
      await btn.first().scrollIntoViewIfNeeded();
      await btn.first().click();
      await page.waitForLoadState("networkidle");
      const heading = typeof item.heading === "string" ? page.getByText(item.heading) : page.locator("main").getByText(item.heading);
      await expect(heading.first()).toBeVisible();
    }
    // Security's Firewall tab (folded in, no longer a top-level item)
    // is reachable and renders in Dark mode.
    await page.locator("main button").filter({ hasText: /^\s*Firewall\s*$/ }).first().click();
    await expect(page.getByText(/Recent Log Entries|Active Rules/i).first()).toBeVisible();
    if (darkConsoleErrors.length) throw new Error(`console errors during dark PSA sweep: ${darkConsoleErrors.join(" | ")}`);

    // Logout clears the shell.
    await page.locator("aside button").filter({ hasText: /logout/i }).first().click();
    await page.waitForLoadState("networkidle");
    await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
  });

  test("a plain signed-up user (portal=\"\") fails closed to neither shell", async ({ browser, request }) => {
    // Documents the real, current authorization contract: signup grants
    // only RoleUser, which /api/v1/me maps to portal="" (no case in the
    // Me handler's switch). The frontend must show neither the Platform
    // Administration shell nor the Customer Portal — never infer a shell
    // from role.
    const email = `portal-e2e-plain-${Date.now()}@portal-e2e-plain.local`;
    const password = "PortalE2ePlainPass123!";
    const signupRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/signup`, {
      data: { email, password, name: "Portal E2E Plain User" },
    });
    expect(signupRes.ok()).toBeTruthy();

    const loginRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/login`, {
      data: { email, password },
    });
    expect(loginRes.ok()).toBeTruthy();
    const accessToken: string = (await loginRes.json()).access_token;

    const context = await browser.newContext({ bypassCSP: true });
    const page = await context.newPage();
    await page.route("**/api/v1/**", async (route) => {
      const headers = { ...route.request().headers(), Authorization: `Bearer ${accessToken}` };
      await route.continue({ headers });
    });

    await page.goto(`http://127.0.0.1:${adminPort}/admin`);
    await page.waitForLoadState("networkidle");

    await expect(page.getByText("Access Unavailable")).toBeVisible();
    await expect(page.getByText("Platform Administration")).toHaveCount(0);
    await expect(page.getByText("Customer Portal")).toHaveCount(0);
  });

  test("theme: defaults to Light even with OS dark preference, toggles to Dark, persists across reload, and applies pre-paint", async ({ browser, request }) => {
    const loginRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/login`, {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(loginRes.ok()).toBeTruthy();
    const accessToken: string = (await loginRes.json()).access_token;

    // colorScheme: "dark" simulates an OS that prefers dark — the app
    // must still default to Light (never inferred from the OS).
    const context = await browser.newContext({ bypassCSP: true, colorScheme: "dark" });
    const page = await context.newPage();
    await page.route("**/api/v1/**", async (route) => {
      const headers = { ...route.request().headers(), Authorization: `Bearer ${accessToken}` };
      await route.continue({ headers });
    });

    await page.goto(`http://127.0.0.1:${adminPort}/admin`);
    await page.waitForLoadState("networkidle");

    let hasDarkClass = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(hasDarkClass).toBe(false);
    let stored = await page.evaluate(() => window.localStorage.getItem("orvix-admin-theme"));
    expect(stored).toBeNull();

    const toggle = page.getByRole("switch", { name: /switch to dark theme/i });
    await expect(toggle).toBeVisible();
    await toggle.click();

    hasDarkClass = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(hasDarkClass).toBe(true);
    stored = await page.evaluate(() => window.localStorage.getItem("orvix-admin-theme"));
    expect(stored).toBe("dark");

    // Reload: the pre-paint init script in index.html must read the
    // stored choice and apply .dark before React ever mounts, with no
    // flash of the light theme in between.
    await page.reload();
    await page.waitForLoadState("networkidle");
    hasDarkClass = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(hasDarkClass).toBe(true);

    // No console errors and the shell still renders correctly in Dark.
    const consoleErrors: string[] = [];
    page.on("console", (msg) => { if (msg.type() === "error") consoleErrors.push(msg.text()); });
    await expect(page.locator("main").locator("h2").first()).toBeVisible();
    if (consoleErrors.length) throw new Error(`console errors in dark theme: ${consoleErrors.join(" | ")}`);
  });

});
