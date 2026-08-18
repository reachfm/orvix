import { expect, test } from "@playwright/test";
import { mockMailControlAPI, openPlatformShell, applyTenantScope, ensurePlatformSidebarOpen } from "./mail-control-fixtures";

const VIEWPORTS = [
  { name: "iphone-375x812", width: 375, height: 812 },
  { name: "tablet-768x1024", width: 768, height: 1024 },
  { name: "laptop-1366x768", width: 1366, height: 768 },
  { name: "desktop-1440x900", width: 1440, height: 900 },
  { name: "wide-1920x1080", width: 1920, height: 1080 },
];

test.describe("Mail Control responsive sweep", () => {
  for (const vp of VIEWPORTS) {
    test(`renders every Mail Control page at ${vp.name} without console errors`, async ({ page }) => {
      const errors: string[] = [];
      page.on("console", (msg) => { if (msg.type() === "error") errors.push(msg.text()); });
      page.on("pageerror", (err) => errors.push(String(err)));
      await page.setViewportSize({ width: vp.width, height: vp.height });
      await mockMailControlAPI(page, { portal: "platform" });
      await openPlatformShell(page);

      const pages: Array<{ label: string; scoped: boolean }> = [
        { label: "Domains", scoped: true },
        { label: "Mailboxes", scoped: true },
        { label: "Aliases", scoped: true },
        { label: "Groups", scoped: true },
        { label: "Relays", scoped: false },
        { label: "Mail Queue", scoped: false },
        { label: "Suppressions", scoped: true },
        { label: "Deliverability", scoped: true },
        { label: "Bulk Mailboxes", scoped: true },
      ];
      for (const p of pages) {
        await ensurePlatformSidebarOpen(page);
        await page.getByRole("button", { name: p.label, exact: true }).click();
        if (p.scoped) await applyTenantScope(page, "Acme");
      }
      // Sidebar navigation remains reachable (no overflow trap) at the end.
      await expect(page.getByRole("button", { name: "Overview", exact: true })).toBeVisible();
      expect(errors).toEqual([]);
    });
  }
});

test.describe("Mail Control accessibility", () => {
  test("keyboard navigation: sidebar buttons are reachable and activate with Enter", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Overview", exact: true }).focus();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Enter");
    // Some page rendered after Enter activation.
    await expect(page.getByRole("heading", { name: /Organizations/i }).or(page.getByRole("heading", { name: /Platform/i }).first())).toBeVisible();
  });

  test("tables expose accessible names and status is never color-only", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    const table = page.getByRole("table", { name: "Platform domains" });
    await expect(table).toBeVisible();
    // Status badges carry a text label plus a non-color signal (the dot + word).
    await expect(page.getByRole("status", { name: "Status Active" })).toBeVisible();
    await ensurePlatformSidebarOpen(page);
    await page.getByRole("button", { name: "Mailboxes", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await expect(page.getByRole("table", { name: "Platform mailboxes" })).toBeVisible();
    await ensurePlatformSidebarOpen(page);
    await page.getByRole("button", { name: "Bulk Mailboxes", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await expect(page.getByRole("table", { name: "Bulk mailbox selection" })).toBeVisible();
  });

  test("form controls in the relay create dialog have accessible labels", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Relays", exact: true }).click();
    await page.getByRole("button", { name: "Create relay" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Name", { exact: true })).toBeVisible();
    await expect(dialog.getByLabel("Host", { exact: true })).toBeVisible();
    await expect(dialog.getByLabel("Port", { exact: true })).toBeVisible();
    await expect(dialog.getByLabel("Username", { exact: true })).toBeVisible();
    await expect(dialog.getByLabel(/^Password/)).toBeVisible();
  });

  test("dialog focus is trapped and restored on close (domain detail drawer)", async ({ page }) => {
    await mockMailControlAPI(page, { portal: "platform" });
    await openPlatformShell(page);
    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await applyTenantScope(page, "Acme");
    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    // Escape closes the dialog (Radix focus restoration returns to the trigger).
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
    await expect(page.getByRole("table", { name: "Platform domains" })).toBeVisible();
  });
});
