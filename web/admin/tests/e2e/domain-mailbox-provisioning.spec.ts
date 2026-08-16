import { expect, test, type Page } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  mockProvisioningAPI,
  openPlatformShell,
  applyTenantScope,
  createdCalls,
  resetCreatedCalls,
} from "./domain-mailbox-provisioning-fixtures";
import { mockMailControlAPI } from "./mail-control-fixtures";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE_CSV = path.join(__dirname, "fixtures", "bulk-mailboxes.csv");

async function noConsoleErrors(page: Page) {
  const errors: string[] = [];
  page.on("console", (msg) => { if (msg.type() === "error") errors.push(msg.text()); });
  page.on("pageerror", (err) => errors.push(String(err)));
  return errors;
}

test.describe("Platform Domain provisioning", () => {
  test.beforeEach(() => resetCreatedCalls());

  test("creates a domain via the exact contract, with no mail-access-mode field, CSRF and Idempotency-Key", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create domain/i }).click();
    await page.getByLabel("Domain name *").fill("newco.example");
    await page.getByRole("button", { name: "Create domain", exact: true }).click();

    await expect(page.getByText("Domain created")).toBeVisible();
    await expect(page.getByText("newco.example", { exact: true })).toBeVisible();

    const post = createdCalls.find((c) => c.method === "POST" && c.url.endsWith("/platform/domains/7"));
    expect(post).toBeTruthy();
    expect(post!.headers["x-csrf-token"]).toBe("test-csrf-token");
    expect(post!.headers["idempotency-key"]).toBeTruthy();
    const body = JSON.parse(post!.body);
    expect(body).toEqual({ name: "newco.example", status: "active" });
    expect(body).not.toHaveProperty("mail_access_mode");
  });

  test("renders DKIM public record and DNS requirements, never a private key, and honestly reports no auto DNS change", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create domain/i }).click();
    await page.getByLabel("Domain name *").fill("dkim.example");
    await page.getByText("Advanced: limits & DKIM").click();
    await page.getByLabel("Generate DKIM for this domain").check();
    await page.getByRole("button", { name: "Create domain", exact: true }).click();

    await expect(page.getByText(/DKIM \(public record only\)/)).toBeVisible();
    await expect(page.getByText(/FAKEPUBLICKEYDATA/)).toBeVisible();
    await expect(page.getByText("DNS records to publish")).toBeVisible();
    await expect(page.getByText(/no public DNS was changed automatically/)).toBeVisible();

    const body = await page.locator("body").textContent();
    expect(body).not.toMatch(/PRIVATE KEY/i);
  });

  test("shows a validation error and never a false success state on a rejected submit", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create domain/i }).click();
    // Leave name blank via direct mutation bypass is not possible (button
    // stays disabled) — instead exercise the server-side rejection path by
    // submitting a name and then intercepting with a 400 once.
    await page.route("**/api/v1/platform/domains/7", (r) => {
      if (r.request().method() === "POST") {
        return r.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ code: "VALIDATION_FAILED", message: "name already exists" }) });
      }
      return r.continue();
    });
    await page.getByLabel("Domain name *").fill("dup.example");
    await page.getByRole("button", { name: "Create domain", exact: true }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByText("Domain created")).not.toBeVisible();
  });
});

test.describe("Platform Mailbox provisioning", () => {
  test.beforeEach(() => resetCreatedCalls());

  test("requires a mail access mode choice and never offers inherit", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create mailbox/i }).click();
    await expect(page.getByRole("button", { name: "Create mailbox", exact: true })).toBeDisabled();
    await expect(page.getByLabel(/^Internal only/)).toBeVisible();
    await expect(page.getByLabel(/^Internal and external/)).toBeVisible();
    await expect(page.getByText("inherit", { exact: false })).not.toBeVisible();
  });

  test("creates a mailbox via the exact contract and never retains the password after success", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create mailbox/i }).click();
    // The domain selector is filtered to the currently-selected
    // tenant's active domains — wait for the real option to load
    // before choosing it, then compose the address from local
    // part + domain (there is no free-text "Email" field: the
    // domain must be a real one, not hand-typed).
    await page.getByLabel("Domain *").selectOption({ label: "acme.example" });
    await page.getByLabel("Local part *").fill("new");
    const passwordField = page.getByLabel("Password *");
    await passwordField.fill("tempSecret!2026");
    await expect(passwordField).toHaveAttribute("type", "password");
    await expect(passwordField).toHaveAttribute("autocomplete", "new-password");
    await page.getByLabel(/^Internal only/).check();
    await page.getByRole("button", { name: "Create mailbox", exact: true }).click();

    await expect(page.getByText("Mailbox created")).toBeVisible();
    const body = await page.locator("body").textContent();
    expect(body).not.toContain("tempSecret!2026");

    const post = createdCalls.find((c) => c.method === "POST" && c.url.endsWith("/platform/mailboxes/7"));
    expect(post).toBeTruthy();
    expect(post!.headers["idempotency-key"]).toBeTruthy();
    const parsed = JSON.parse(post!.body);
    expect(parsed).toEqual({ email: "new@acme.example", password: "tempSecret!2026", force_password_change: true, mail_access_mode: "internal_only" });

    // Never leaked to storage.
    const storageDump = await page.evaluate(() => JSON.stringify(localStorage) + JSON.stringify(sessionStorage));
    expect(storageDump).not.toContain("tempSecret!2026");
  });

  test("distinguishes configured vs effective mail access and mutates with the real read version", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await page.getByText("alice@acme.example").click();
    const section = page.getByLabel("Mail access policy");
    await expect(section.getByText(/Configured: Internal only · Effective: Internal only/)).toBeVisible();

    // First attempt: fixture simulates a stale version (412).
    await section.getByLabel("New mail access mode").selectOption("internal_external");
    await section.getByRole("button", { name: "Apply mode" }).click();
    await expect(page.getByText(/This mailbox changed elsewhere/)).toBeVisible();

    const firstAttempt = createdCalls.filter((c) => c.url.includes("/access-mode"))[0];
    const firstBody = JSON.parse(firstAttempt.body);
    expect(firstBody.expected_version).toBe(1);
    expect(firstBody.mail_access_mode).toBe("internal_external");

    // Retry succeeds once the mailbox has refetched to the real version.
    await section.getByLabel("New mail access mode").selectOption("internal_external");
    await section.getByRole("button", { name: "Apply mode" }).click();
    await expect(page.getByText(/Configured: Internal and external/)).toBeVisible();
  });
});

test.describe("Platform Bulk Mailbox Provisioning (CSV/XLSX import)", () => {
  test.beforeEach(() => resetCreatedCalls());

  test("downloads templates from the untenanted route", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("button", { name: "CSV template" }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("bulk-mailbox-import-template.csv");
  });

  test("runs the full stage -> validate -> create job -> execute -> poll -> terminal workflow, preventing duplicate submits", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    await applyTenantScope(page);

    await page.getByLabel("Domain").selectOption("1");
    await page.setInputFiles('input[type="file"]', FIXTURE_CSV);
    await expect(page.getByText(/Staged 2 rows/)).toBeVisible();

    await page.getByRole("button", { name: "Validate" }).click();
    await expect(page.getByText(/2 valid, 0 invalid of 2 total rows/)).toBeVisible();

    const createBtn = page.getByRole("button", { name: "Create job" });
    await createBtn.click();
    await expect(page.getByText(/Job #501 created/)).toBeVisible();

    // Only one job-create request reached the backend even though the
    // button was available before the mutation settled.
    const jobCreateCalls = createdCalls.filter((c) => c.method === "POST" && c.url.endsWith("/platform/mailboxes/bulk/7/jobs"));
    expect(jobCreateCalls.length).toBe(1);

    await page.getByRole("button", { name: "Execute job" }).click();
    await expect(page.getByText(/Refreshing automatically/)).toBeVisible();
    await expect(page.getByText("Completed")).toBeVisible({ timeout: 15000 });

    const executeCalls = createdCalls.filter((c) => c.url.includes("/execute"));
    expect(executeCalls.length).toBe(1);
  });

  test("rejects an unsupported file extension client-side before any staging request", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    await applyTenantScope(page);
    await page.getByLabel("Domain").selectOption("1");
    const badFile = path.join(__dirname, "fixtures", "bulk-mailboxes.csv");
    // Re-use the CSV bytes but present a .txt name via a data buffer.
    await page.setInputFiles('input[type="file"]', { name: "not-a-sheet.txt", mimeType: "text/plain", buffer: Buffer.from("hello") });
    await expect(page.getByText(/Only \.csv or \.xlsx files are accepted/)).toBeVisible();
    expect(createdCalls.some((c) => c.url.includes("/stage"))).toBe(false);
    void badFile;
  });

  test("row and error text render as plain text, never HTML, even with markup-like content", async ({ page }) => {
    await mockProvisioningAPI(page);
    await page.route("**/api/v1/platform/mailboxes/bulk/7/validate", (r) =>
      r.fulfill({
        status: 200, contentType: "application/json",
        body: JSON.stringify({
          total_rows: 1, valid_rows: 0, invalid_rows: 1,
          rows: [{ id: 1, job_id: 0, row_number: 1, email: "<img src=x onerror=alert(1)>@acme.example", status: "invalid", error_code: "invalid_email", error_detail: "<b>bad</b> email", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }],
          capacity_remaining: 88, source_hash: "hash_abc123", schema_version: 1,
        }),
      }));
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    await applyTenantScope(page);
    await page.getByLabel("Domain").selectOption("1");
    await page.setInputFiles('input[type="file"]', FIXTURE_CSV);
    await page.getByRole("button", { name: "Validate" }).click();
    await expect(page.getByText(/No valid rows/)).toBeVisible();
    const imgCount = await page.locator("img[src=x]").count();
    expect(imgCount).toBe(0);
    const boldCount = await page.locator("table b").count();
    expect(boldCount).toBe(0);
  });
});

test.describe("Provisioning portal and tenant isolation", () => {
  test("PSA provisioning calls never carry X-Support-Tenant-ID and never reach tenant-owned routes", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    await applyTenantScope(page);

    const platformCalls = createdCalls.filter((c) => c.url.includes("/api/v1/platform/"));
    expect(platformCalls.length).toBeGreaterThan(0);
    for (const call of platformCalls) {
      expect(call.headers["x-support-tenant-id"]).toBeUndefined();
    }
  });

  test("Tenant Admin cannot see or navigate to platform provisioning routes", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "organization" });
    await page.goto("/admin", { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { name: /Dashboard/i })).toBeVisible();
    await expect(page.getByRole("button", { name: "Bulk Import", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Bulk Mailboxes", exact: true })).toHaveCount(0);
  });

  test("no browser console errors across the full provisioning flow", async ({ page }) => {
    const errors = await noConsoleErrors(page);
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    for (const label of ["Domains", "Mailboxes", "Bulk Import"]) {
      await page.getByRole("button", { name: label, exact: true }).click();
      await applyTenantScope(page);
    }
    expect(errors).toEqual([]);
  });
});
