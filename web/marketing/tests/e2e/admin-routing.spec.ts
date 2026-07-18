import { expect, test } from "@playwright/test";

test("/admin/login renders Sign In", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
  await expect(page.locator("h2")).not.toContainText("Professional email");
});

test("/admin/signup renders Create Account", async ({ page }) => {
  await page.goto("/admin/signup", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Create Account");
  await expect(page.locator("h2")).not.toContainText("Professional email");
});

test("/admin unauthenticated renders Sign In", async ({ page }) => {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("/admin/ unauthenticated renders Sign In", async ({ page }) => {
  await page.goto("/admin/", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("admin pages must not contain marketing homepage title", async ({ page }) => {
  for (const p of ["/admin", "/admin/login", "/admin/signup"]) {
    await page.goto(p, { waitUntil: "domcontentloaded" });
    await expect(page).not.toHaveTitle(/Email hosting for teams/);
  }
});

test("login page links to signup", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await expect(page.locator("a[href=\"/admin/signup\"]")).toBeVisible();
});

test("signup page links to login", async ({ page }) => {
  await page.goto("/admin/signup", { waitUntil: "domcontentloaded" });
  await expect(page.locator("a[href=\"/admin/login\"]")).toBeVisible();
});
