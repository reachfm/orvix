import { test, expect } from "@playwright/test";
import { spawn, ChildProcess } from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import * as crypto from "crypto";
import * as http from "http";

const ADMIN_EMAIL = "admin@e2e-test.local";
const ADMIN_PASSWORD = "E2eTestPass123!";

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

  const dsn = path.join(tempDir, "orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate");
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

  test("login and navigate dashboard and customer portal sections", async ({ browser, request }) => {
    // Login via API to get access token
    const loginRes = await request.post(`http://127.0.0.1:${adminPort}/api/v1/auth/login`, {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
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

    // Verify dashboard loads with key elements.
    // Use within() scoped to the <main> content area to avoid
    // matching sidebar navigation buttons that share the same text.
    const mainContent = page.locator("main");
    await expect(mainContent.locator("h2").filter({ hasText: "Dashboard" })).toBeVisible();
    await expect(mainContent.getByText("Domains")).toBeVisible();
    await expect(mainContent.getByText("Mailboxes")).toBeVisible();
    await expect(mainContent.getByText("Storage")).toBeVisible();

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
      { text: "Domain Setup", heading: "Domain Onboarding" },
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
  });

});
