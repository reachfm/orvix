import { expect, test, type Page } from "@playwright/test";
import { mockMailControlAPI, openPlatformShell, applyTenantScope, platformCalls, resetPlatformCalls } from "./mail-control-fixtures";

async function noConsoleErrors(page: Page) {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  page.on("pageerror", (err) => errors.push(String(err)));
  return errors;
}

test.describe("Platform Super Admin Mail Control", () => {
  test.beforeEach(() => resetPlatformCalls());

  test("PSA login and full Mail Control navigation", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    for (const label of ["Domains", "Mailboxes", "Aliases", "Groups", "Relays"]) {
      await expect(page.getByRole("button", { name: label, exact: true })).toBeVisible();
    }
    for (const label of ["Mail Queue", "Suppressions", "Deliverability", "Bulk Mailboxes"]) {
      await expect(page.getByRole("button", { name: label, exact: true })).toBeVisible();
    }
  });

  test("domain inventory loads via the platform route after explicit tenant scope", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Domains/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByRole("cell", { name: "acme.example", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "beta.example", exact: true })).toBeVisible();
  });

  test("domain detail shows DKIM and DMARC from the real contract, and does not present a domain-level mail-access-mode control", async ({ page }) => {
    // PRODUCT DECISION: mail_access_mode is a MAILBOX-level policy in
    // this frontend, set only from the mailbox create/detail views —
    // see DomainDetailDrawer.tsx's header comment. The domain detail
    // drawer (Overview tab, active by default) shows DKIM/DMARC state
    // from the real read contract instead.
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await expect(drawer.getByText(/Enabled · selector mail/)).toBeVisible();
    await expect(drawer.getByText("Enabled", { exact: true })).toBeVisible(); // DMARC

    await expect(drawer.getByText("Mail access policy")).not.toBeVisible();
    await expect(drawer.getByText("Internal + external")).not.toBeVisible();
    await expect(drawer.getByText(/local-to-local delivery remains permitted/i)).not.toBeVisible();

    // DKIM tab: configured state + selector come from the real
    // domain fields even without a creation-time DNS/DKIM cache (this
    // domain was opened from the list, not just created).
    await drawer.getByRole("tab", { name: "DKIM" }).click();
    await expect(drawer.getByText("Configured")).toBeVisible();
    await expect(drawer.getByText("Selector: mail")).toBeVisible();
  });

  test("mailbox inventory and detail load via the platform route", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Mailboxes/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("alice@acme.example")).toBeVisible();
    await expect(page.getByText("bob@acme.example")).toBeVisible();
    await page.getByText("alice@acme.example").click();
    await expect(page.getByRole("button", { name: "Reset password" })).toBeVisible();
  });

  test("aliases inventory loads via the platform route", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Aliases", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Aliases/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("sales@acme.example")).toBeVisible();
    await expect(page.getByText("alice@acme.example")).toBeVisible();
  });

  test("groups inventory and memberships load via the platform route", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Groups", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Groups/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("engineering@acme.example")).toBeVisible();
    await page.getByRole("button", { name: /View members of engineering@acme.example/ }).click();
    await expect(page.getByText("alice@acme.example")).toBeVisible();
    await expect(page.getByText("bob@acme.example")).toBeVisible();
  });

  test("relay inventory renders redacted credentials (never the username or a secret)", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Relays", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Relays/i })).toBeVisible();
    await expect(page.getByText("primary")).toBeVisible();
    await expect(page.getByText("smtp.provider.example:587")).toBeVisible();
    // Secret redaction: the auth signal is the boolean label only.
    await expect(page.getByText("configured")).toBeVisible();
    const body = await page.locator("body").textContent();
    expect(body).not.toContain("relay-user");
  });

  test("suppression lifecycle renders with impact copy and history", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Suppressions", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Suppression Management/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("bounce@example.net")).toBeVisible();
    await expect(page.getByText("spam@example.net")).toBeVisible();
    await page.getByText("bounce@example.net").click();
    await expect(page.getByText(/active: outbound delivery to this address is blocked/i)).toBeVisible();
    await expect(page.getByText("created", { exact: true })).toBeVisible();
    await expect(page.getByText("released", { exact: true })).toBeVisible();
  });

  test("deliverability metrics, breakdowns and events render from real data", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Deliverability", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Deliverability/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("100", { exact: true })).toBeVisible();
    await expect(page.getByText("By category")).toBeVisible();
    await expect(page.getByText("provider-a")).toBeVisible();
    await expect(page.getByText(/Time series \(hourly buckets\)/)).toBeVisible();
    const eventsTable = page.getByLabel("Deliverability events");
    await expect(eventsTable.getByRole("cell", { name: "Delivered", exact: true })).toBeVisible();
    await expect(eventsTable.getByRole("cell", { name: "Suppressed", exact: true })).toBeVisible();
  });

  test("queue shows attribution, failure category and state-aware actions", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mail Queue", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Mail Operations/i })).toBeVisible();
    // Attribution columns from the PR #65 projection.
    await expect(page.getByText("tenant 7 · #1").first()).toBeVisible();
    await expect(page.getByText("Suppressed", { exact: true })).toBeVisible();
    // Delivered (terminal) rows expose no actionable buttons.
    await expect(page.getByRole("button", { name: "Retry message 1003" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Bounce message 1003" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Cancel message 1003" })).toBeDisabled();
    // Pending rows keep retry enabled.
    await expect(page.getByRole("button", { name: "Retry message 1001" })).toBeEnabled();
  });

  test("bulk mailbox workflow applies via the production bulk endpoint", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Mailboxes", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Bulk Mailbox Operations/i })).toBeVisible();
    await applyTenantScope(page, "Acme");
    await page.getByLabel("Select mailbox 101").check();
    await page.getByLabel("Select mailbox 102").check();
    await page.getByLabel("Bulk action").selectOption("suspend");
    await page.getByRole("button", { name: /Apply Suspend/ }).click();
    await page.getByRole("button", { name: "Apply to selection" }).click();
    await expect(page.getByText(/Bulk result: 2\/2 succeeded/)).toBeVisible();
  });

  test("normal PSA platform calls never carry X-Support-Tenant-ID", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Suppressions", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Deliverability", exact: true }).click();
    await applyTenantScope(page, "Acme");

    expect(platformCalls.length).toBeGreaterThan(0);
    for (const call of platformCalls) {
      expect(call.url).toMatch(/\/api\/v1\/platform\//);
      expect(call.headers["x-support-tenant-id"]).toBeUndefined();
    }
  });

  test("PSA never calls tenant-owned mail routes", async ({ page }) => {
    const tenantCalls: string[] = [];
    await mockMailControlAPI(page, { portal: "platform" });
    // Recording interceptors registered LAST so they see every request
    // to tenant-family routes; they answer 403 so any accidental call
    // would visibly fail.
    await page.route("**/api/v1/domains*", (r) => { tenantCalls.push(r.request().url()); return r.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "forbidden", code: "FORBIDDEN" }) }); });
    await page.route("**/api/v1/mailboxes*", (r) => { tenantCalls.push(r.request().url()); return r.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "forbidden", code: "FORBIDDEN" }) }); });
    await page.route("**/api/v1/enterprise/**", (r) => { tenantCalls.push(r.request().url()); return r.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "forbidden", code: "FORBIDDEN" }) }); });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Aliases", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("button", { name: "Groups", exact: true }).click();
    await applyTenantScope(page, "Acme");
    expect(tenantCalls).toEqual([]);
  });

  test("no browser console errors across the Mail Control flow", async ({ page }) => {
    const errors = await noConsoleErrors(page);
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    for (const label of ["Domains", "Mailboxes", "Aliases", "Groups", "Relays", "Mail Queue", "Suppressions", "Deliverability", "Bulk Mailboxes"]) {
      await page.getByRole("button", { name: label, exact: true }).click();
      if (["Domains", "Mailboxes", "Aliases", "Groups", "Suppressions", "Deliverability", "Bulk Mailboxes"].includes(label)) {
        await applyTenantScope(page, "Acme");
      }
    }
    expect(errors).toEqual([]);
  });

  test("light theme is default even with OS dark preference", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await page.emulateMedia({ colorScheme: "dark" });
    await openPlatformShell(page);
    const html = page.locator("html");
    // Light is the mandatory default — never inferred from prefers-color-scheme.
    await expect(html).not.toHaveClass(/dark/);
    await expect(page.getByRole("button", { name: "Domains", exact: true })).toBeVisible();
  });

  test("dark theme persists after reload", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.evaluate(() => window.localStorage.setItem("orvix-admin-theme", "dark"));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: /Orvix Admin/i }).waitFor();
    const html = page.locator("html");
    await expect(html).toHaveClass(/dark/);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await expect(page.getByRole("cell", { name: "acme.example", exact: true })).toBeVisible();
  });

  test("mail control pages render in both light and dark modes", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Deliverability", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await expect(page.getByRole("heading", { name: /Deliverability/i })).toBeVisible();
    // Switch to dark via the toggle and confirm pages still render.
    await page.evaluate(() => window.localStorage.setItem("orvix-admin-theme", "dark"));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: /Orvix Admin/i }).waitFor();
    await page.getByRole("button", { name: "Suppressions", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await expect(page.getByText("bounce@example.net")).toBeVisible();
  });
});

test.describe("Tenant Admin regression — Mail Control isolation", () => {
  test("Customer Portal remains visible and Platform Mail Control navigation is absent", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "organization" });
    await page.goto("/admin", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: /Dashboard/i })).toBeVisible();
    // Platform-only Mail Control navigation must NOT be present. (The
    // tenant portal legitimately has its own tenant-owned Domains/
    // Mailboxes/Aliases/Groups pages, so only the platform-only items
    // are asserted absent.)
    for (const label of ["Relays", "Mail Queue", "Suppressions", "Deliverability", "Bulk Mailboxes"]) {
      await expect(page.getByRole("button", { name: label, exact: true })).toHaveCount(0);
    }
    // Tenant-owned pages remain functional.
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Domains/i })).toBeVisible();
  });

  test("tenant admin identity is never platform", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "organization" });
    await page.goto("/admin", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Orvix Admin", { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: /Dashboard/i })).toBeVisible();
  });
});
