import { expect, test } from "@playwright/test";

test("GET /admin renders Admin Sign In", async ({ page }) => {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("GET /admin/ renders Admin Sign In", async ({ page }) => {
  await page.goto("/admin/", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("GET /admin/login renders Admin Sign In", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("GET /admin/signup renders Create Account", async ({ page }) => {
  await page.goto("/admin/signup", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Create Account");
});

test("GET /admin/forgot-password renders forgot-password page", async ({ page }) => {
  await page.goto("/admin/forgot-password", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Forgot Password");
});

test("GET /admin/reset-password renders reset-password page", async ({ page }) => {
  await page.goto("/admin/reset-password", { waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Reset Password");
});

test("refresh on /admin/login preserves Sign In", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
});

test("refresh on /admin/signup preserves Create Account", async ({ page }) => {
  await page.goto("/admin/signup", { waitUntil: "domcontentloaded" });
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Create Account");
});

test("login page links to /admin/signup", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await expect(page.locator('a[href="/admin/signup"]')).toBeVisible();
});

test("signup page links to /admin/login", async ({ page }) => {
  await page.goto("/admin/signup", { waitUntil: "domcontentloaded" });
  await expect(page.locator('a[href="/admin/login"]')).toBeVisible();
});

test("browser back/forward switches between login and signup", async ({ page }) => {
  await page.goto("/admin/login", { waitUntil: "domcontentloaded" });
  await page.click('a[href="/admin/signup"]');
  await page.waitForURL("**/admin/signup");
  await expect(page.locator("h2")).toContainText("Create Account");
  await page.goBack({ waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Sign In");
  await page.goForward({ waitUntil: "domcontentloaded" });
  await expect(page.locator("h2")).toContainText("Create Account");
});

test("admin pages do not contain Marketing title", async ({ page }) => {
  for (const p of ["/admin", "/admin/login", "/admin/signup"]) {
    await page.goto(p, { waitUntil: "domcontentloaded" });
    await expect(page).not.toHaveTitle(/Email hosting for teams/);
  }
});

test("admin pages do not contain Marketing description", async ({ page }) => {
  for (const p of ["/admin", "/admin/login", "/admin/signup"]) {
    await page.goto(p, { waitUntil: "domcontentloaded" });
    const desc = page.locator('meta[name="description"]');
    if (await desc.count() > 0) {
      await expect(desc).not.toHaveAttribute("content", /Professional email hosting/);
    }
  }
});

test("admin asset requests return JS/CSS MIME types, not HTML", async ({ page, request }) => {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  const jsLink = page.locator('script[src*=".js"]').first();
  if (await jsLink.count() > 0) {
    const src = await jsLink.getAttribute("src");
    const resp = await request.get(src!, { headers: { accept: "*/*" } });
    const ct = resp.headers()["content-type"] || "";
    expect(ct).toContain("javascript");
  }
  const cssLink = page.locator('link[rel="stylesheet"]').first();
  if (await cssLink.count() > 0) {
    const href = await cssLink.getAttribute("href");
    const resp = await request.get(href!, { headers: { accept: "*/*" } });
    const ct = resp.headers()["content-type"] || "";
    expect(ct).toContain("css");
  }
});

test("unknown non-Admin path returns 404", async ({ request }) => {
  const resp = await request.get("/not-a-real-path", { headers: { accept: "text/html" } });
  expect(resp.status()).toBe(404);
});

test("GET /api/v1/me returns unauthorized JSON", async ({ request }) => {
  const resp = await request.get("/api/v1/me", { headers: { accept: "application/json" } });
  expect(resp.status()).toBe(401);
  const body = await resp.json();
  expect(body.error).toBe("unauthenticated");
});
