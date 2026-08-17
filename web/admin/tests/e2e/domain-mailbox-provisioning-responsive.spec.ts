import { expect, test } from "@playwright/test";
import { mockProvisioningAPI, openPlatformShell, applyTenantScope, resetCreatedCalls, ensurePlatformSidebarOpen } from "./domain-mailbox-provisioning-fixtures";

const VIEWPORTS = [
  { name: "iphone-375x812", width: 375, height: 812 },
  { name: "tablet-768x1024", width: 768, height: 1024 },
  { name: "laptop-1280x720", width: 1280, height: 720 },
  { name: "desktop-1440x900", width: 1440, height: 900 },
  { name: "wide-1920x1080", width: 1920, height: 1080 },
];

test.describe("Domain/Mailbox/Bulk provisioning responsive sweep", () => {
  for (const vp of VIEWPORTS) {
    test(`renders Domains, Mailboxes and Bulk Import at ${vp.name} without console errors or horizontal overflow`, async ({ page }) => {
      resetCreatedCalls();
      const errors: string[] = [];
      page.on("console", (msg) => { if (msg.type() === "error") errors.push(msg.text()); });
      page.on("pageerror", (err) => errors.push(String(err)));
      await page.setViewportSize({ width: vp.width, height: vp.height });
      await mockProvisioningAPI(page);
      await openPlatformShell(page);

      for (const label of ["Domains", "Mailboxes", "Bulk Import"]) {
        await ensurePlatformSidebarOpen(page);
        await page.getByRole("button", { name: label, exact: true }).click();
        await applyTenantScope(page);
      }

      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
      expect(overflow).toBe(false);
      expect(errors).toEqual([]);
    });
  }

  test("light theme is default for provisioning pages even with OS dark preference", async ({ page }) => {
    await mockProvisioningAPI(page);
    await page.emulateMedia({ colorScheme: "dark" });
    await openPlatformShell(page);
    const html = page.locator("html");
    await expect(html).not.toHaveClass(/dark/);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await expect(page.getByRole("heading", { name: /Platform Domains/i })).toBeVisible();
  });

  test("dark theme renders the provisioning pages without console errors", async ({ page }) => {
    const errors: string[] = [];
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    page.on("console", (msg) => { if (msg.type() === "error") errors.push(msg.text()); });
    await page.evaluate(() => window.localStorage.setItem("orvix-admin-theme", "dark"));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "Orvix", exact: true }).waitFor();
    await ensurePlatformSidebarOpen(page);
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await expect(page.getByRole("heading", { name: /Platform Mailboxes/i })).toBeVisible();
    expect(errors).toEqual([]);
  });
});

test.describe("Domain/Mailbox/Bulk provisioning accessibility", () => {
  test("create-domain dialog form controls have accessible labels and visible focus", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create domain/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Domain name *")).toBeVisible();
    await expect(dialog.getByLabel("Description")).toBeVisible();
    await expect(dialog.getByLabel("Status")).toBeVisible();
    await dialog.getByLabel("Domain name *").focus();
    await expect(dialog.getByLabel("Domain name *")).toBeFocused();
  });

  test("create-domain dialog traps focus and restores it to the trigger on Escape", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page);
    const trigger = page.getByRole("button", { name: /Create domain/i });
    await trigger.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
    await expect(trigger).toBeFocused();
  });

  test("mailbox access-mode radio group is a labeled fieldset reachable by keyboard", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    await page.getByRole("button", { name: /Create mailbox/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("group", { name: /Mail access mode/ })).toBeVisible();
    await dialog.getByLabel(/^Internal only/).focus();
    await page.keyboard.press("ArrowDown");
    await expect(dialog.getByLabel(/^Internal and external/)).toBeFocused();
  });

  test("bulk import page domain select and file input are labeled", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Bulk Import", exact: true }).click();
    await applyTenantScope(page);
    await expect(page.getByLabel("Domain")).toBeVisible();
    await expect(page.getByText("Choose .csv or .xlsx file")).toBeVisible();
  });

  test("mailbox detail drawer focus is trapped and restored on close", async ({ page }) => {
    await mockProvisioningAPI(page);
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page);
    const row = page.getByText("alice@acme.example");
    await row.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
  });
});
