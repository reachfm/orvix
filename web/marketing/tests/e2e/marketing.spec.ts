import { expect, test } from "@playwright/test";
import { PUBLIC_PATHS } from "../../src/lib/route-table";

for (const path of PUBLIC_PATHS) {
  test(`${path} renders its production deep link`, async ({ page }) => {
    const errors: string[] = [];
    page.on("console", (message) => message.type() === "error" && errors.push(message.text()));
    page.on("pageerror", (error) => errors.push(error.message));
    const response = await page.goto(path, { waitUntil: "domcontentloaded" });
    expect(response?.status()).toBe(200);
    await expect(page.locator("h1")).toHaveCount(1);
    await expect(page.locator("h1")).toBeVisible();
    await expect(page).toHaveTitle(/Orvix/);
    await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", new RegExp(`${path === "/" ? "/?$" : `${path}$`}`));
    expect(errors).toEqual([]);
  });
}

test("unknown production route returns the prerendered 404", async ({ page }) => {
  const response = await page.goto("/not-a-real-orvix-page");
  expect(response?.status()).toBe(404);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
});

test("portal and documentation links leave the marketing origin", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator('a[href="https://app.orvix.com/login"]')).not.toHaveCount(0);
  await expect(page.locator('a[href="https://app.orvix.com/signup"]')).not.toHaveCount(0);
  await page.goto("/docs");
  const docsLinks = page.locator('a[href^="https://docs.orvix.email"]');
  await expect(docsLinks.first()).toBeVisible();
});

test("mobile navigation is keyboard closable and restores focus", async ({ page }, testInfo) => {
  test.skip(!testInfo.project.name.includes("mobile"), "mobile-only interaction");
  await page.goto("/");
  const toggle = page.getByRole("button", { name: "Open menu" });
  await toggle.click();
  await expect(page.getByRole("dialog", { name: "Mobile navigation" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Mobile navigation" })).toBeHidden();
  await expect(toggle).toBeFocused();
});

test("pricing displays finite enterprise limits", async ({ page }) => {
  await page.goto("/pricing");
  const enterprise = page.getByRole("article", { name: "Enterprise plan" });
  await expect(enterprise.getByText("Mailboxes")).toBeVisible();
  await expect(enterprise.getByText("1000", { exact: true })).toBeVisible();
  await expect(page.getByText("Unlimited", { exact: true })).toHaveCount(0);
});

test("contact form provides a non-scripted mail fallback", async ({ page }) => {
  await page.goto("/contact");
  await expect(page.locator('a[href^="mailto:"]')).not.toHaveCount(0);
});
