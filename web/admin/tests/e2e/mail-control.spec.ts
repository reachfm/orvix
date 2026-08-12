import { expect, test } from "@playwright/test";
import { mockMailControlAPI, openPlatformShell, PSA_ME, TENANT_ADMIN_ME } from "./mail-control-fixtures";

test.describe("Platform Super Admin Mail Control", () => {
  test("PSA login and Mail Control navigation", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Domains/i })).toBeVisible();
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Platform Mailboxes/i })).toBeVisible();
    await page.getByRole("button", { name: "Mail Operations", exact: true }).click();
    await expect(page.getByRole("heading", { name: /Mail Operations/i })).toBeVisible();
  });

  test("domain inventory renders tenant-context-required state without a tenant", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await expect(page.getByText(/tenant context required/i)).toBeVisible();
  });

  test("domain inventory loads after selecting a tenant with an active grant", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await page.getByLabel("Select tenant").selectOption("7");
    await page.getByRole("button", { name: "Open tenant context" }).click();
    await expect(page.getByRole("cell", { name: "acme.example", exact: true })).toBeVisible();
    await expect(page.getByText("grant #11 · domain_view")).toBeVisible();
  });

  test("mailbox inventory loads within the selected tenant context", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await page.getByLabel("Select tenant").selectOption("7");
    await page.getByRole("button", { name: "Open tenant context" }).click();
    await expect(page.getByText("alice@acme.example")).toBeVisible();
    await expect(page.getByText("bob@acme.example")).toBeVisible();
  });

  test("queue summary and message list render", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mail Operations", exact: true }).click();
    await expect(page.getByText("138", { exact: true })).toBeVisible();
    await expect(page.getByText("alice@acme.example")).toBeVisible();
  });

  test("bulk queue action panel applies retry to a selected message", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mail Operations", exact: true }).click();
    const checkbox = page.getByLabel("Select message 1001");
    await checkbox.check();
    await page.getByRole("button", { name: /Apply retry to 1/ }).click();
    await page.getByRole("button", { name: "Confirm retry" }).click();
    await expect(page.getByText(/Succeeded: 1/)).toBeVisible();
  });

  test("no browser console errors on the Mail Control smoke flow", async ({ page }) => {
    const errors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") errors.push(msg.text());
    });
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await page.getByRole("button", { name: "Mail Operations", exact: true }).click();
    expect(errors).toEqual([]);
  });

  test("light theme is default", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await page.emulateMedia({ colorScheme: "dark" });
    await openPlatformShell(page);
    const html = page.locator("html");
    const theme = await html.getAttribute("data-theme");
    expect(theme).not.toBe("dark");
  });
});

test.describe("Tenant Admin regression — Mail Control isolation", () => {
  test("Customer Portal remains visible and Platform Mail Control is absent", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "organization" });
    await page.goto("/admin", { waitUntil: "domcontentloaded" });
    // Tenant admin lands on the Customer Portal dashboard.
    await expect(page.getByRole("heading", { name: /Dashboard/i })).toBeVisible();
    // Platform Mail Control navigation must NOT be present.
    await expect(page.getByRole("button", { name: "Platform Domains", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Mail Operations", exact: true })).toHaveCount(0);
  });

  test("tenant admin identity is never platform", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "organization" });
    await page.goto("/admin", { waitUntil: "domcontentloaded" });
    await expect(page.getByText("Orvix Admin", { exact: true })).toBeVisible();
    // /me returned the tenant identity; the shell must render the
    // customer portal, proving no platform shell leak.
    await expect(page.getByRole("heading", { name: /Dashboard/i })).toBeVisible();
  });
});
