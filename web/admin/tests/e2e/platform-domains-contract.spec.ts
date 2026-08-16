import { test, expect } from "@playwright/test";
import {
  mockPlatformDomainsAPI,
  openDomainsPage,
  domainCalls,
  resetDomainCalls,
} from "./platform-domains-contract-fixtures";

/**
 * Browser-level regression barrier for the Platform Domains
 * contract-closure remediation.
 *
 * This exists specifically because the previous regression was able to
 * survive a green unit/component suite: the built UI stayed stale while
 * the backend already exposed .../dns, .../dkim/generate,
 * .../dkim/rotate and .../deactivate. These specs run against the real
 * production bundle served from web/admin/dist, so a rebuild that drops
 * these flows fails here even if jsdom tests still pass.
 */

test.describe("Platform Domains — existing-domain DNS", () => {
  test.beforeEach(() => resetDomainCalls());

  test("fetches the live existing-domain DNS snapshot and renders the returned records", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    // The live endpoint is actually called — not the creation cache.
    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/1\/dns$/.test(c.url) && c.method === "GET").length)
      .toBeGreaterThan(0);

    // Server-returned records render, values verbatim.
    await expect(drawer.getByText("inbound mail routing")).toBeVisible();
    await expect(drawer.getByText("mail.orvix.email")).toBeVisible();
    await expect(drawer.getByText("v=spf1 include:orvix.email ~all")).toBeVisible();
    await expect(drawer.getByText("publish_and_verify_dns")).toBeVisible();

    // The obsolete "unavailable for an existing domain" claim is gone.
    await expect(drawer.getByText(/only returns DNS records at the moment a domain is created/i)).toHaveCount(0);
    await expect(drawer.getByText(/no route to re-fetch them for an existing domain/i)).toHaveCount(0);
    await expect(drawer.getByText("DNS records unavailable")).toHaveCount(0);
  });
});

test.describe("Platform Domains — DKIM not configured", () => {
  test.beforeEach(() => resetDomainCalls());

  test("offers Generate DKIM, sends the exact request, and shows the new public record without recreating the domain", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "beta.example", exact: true }).click();
    const drawer = page.getByLabel("beta.example");
    await drawer.getByRole("tab", { name: "DKIM" }).click();

    await expect(drawer.getByText("DKIM is not configured")).toBeVisible();
    await expect(drawer.getByRole("button", { name: "Generate DKIM" })).toBeVisible();
    // Rotate must not be presented as the current action while unconfigured.
    await expect(drawer.getByRole("button", { name: "Rotate DKIM" })).toHaveCount(0);
    await expect(drawer.getByText(/there is no generate or rotate route for an existing domain/i)).toHaveCount(0);

    await drawer.getByRole("button", { name: "Generate DKIM" }).click();

    // Exact route, method, body and idempotency header.
    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/2\/dkim\/generate$/.test(c.url)).length)
      .toBe(1);
    const gen = domainCalls.find((c) => /\/dkim\/generate$/.test(c.url))!;
    expect(gen.method).toBe("POST");
    expect(JSON.parse(gen.body)).toEqual({ expected_version: 1 });
    expect(gen.headers["idempotency-key"]).toBeTruthy();

    // Server state refreshed: configured, with the real new public value,
    // and the not-configured state genuinely gone.
    await expect(drawer.getByText("orvix._domainkey.beta.example")).toBeVisible();
    await expect(drawer.getByText("v=DKIM1; k=rsa; p=NEWLYGENERATEDPUBLICKEY")).toBeVisible();
    await expect(drawer.getByText("DKIM is not configured")).toHaveCount(0);
    await expect(drawer.getByRole("button", { name: "Rotate DKIM" })).toBeVisible();
  });
});

test.describe("Platform Domains — DKIM configured", () => {
  test.beforeEach(() => resetDomainCalls());

  test("shows selector/hostname/TXT, requires rotation confirmation, and renders the rotated value", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DKIM" }).click();

    await expect(drawer.getByText("Configured")).toBeVisible();
    await expect(drawer.getByText("Selector: mail")).toBeVisible();
    await expect(drawer.getByText("mail._domainkey.acme.example")).toBeVisible();
    await expect(drawer.getByText("v=DKIM1; k=rsa; p=CURRENTPUBLICKEYDATA")).toBeVisible();

    // Rotation requires an explicit confirmation step — the click alone
    // must not fire the destructive mutation.
    await drawer.getByRole("button", { name: "Rotate DKIM" }).click();
    const confirmDialog = page.getByRole("dialog", { name: "Rotate DKIM key" });
    await expect(confirmDialog).toBeVisible();
    expect(domainCalls.filter((c) => /\/dkim\/rotate$/.test(c.url))).toHaveLength(0);

    await confirmDialog.getByRole("button", { name: "Rotate DKIM" }).click();

    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/1\/dkim\/rotate$/.test(c.url)).length)
      .toBe(1);
    const rot = domainCalls.find((c) => /\/dkim\/rotate$/.test(c.url))!;
    expect(rot.method).toBe("POST");
    // The real currently-loaded server version, never a client-side bump.
    expect(JSON.parse(rot.body)).toEqual({ confirm_rotation: "rotate-dkim-key", expected_version: 3 });
    expect(rot.headers["idempotency-key"]).toBeTruthy();

    // The NEW value is rendered; the superseded one is gone.
    await expect(drawer.getByText("v=DKIM1; k=rsa; p=ROTATEDPUBLICKEYDATA")).toBeVisible();
    await expect(drawer.getByText("v=DKIM1; k=rsa; p=CURRENTPUBLICKEYDATA")).toHaveCount(0);
  });

  test("never exposes DKIM private key material anywhere in the drawer", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DKIM" }).click();
    await expect(drawer.getByText("mail._domainkey.acme.example")).toBeVisible();

    await expect(drawer.getByText(/BEGIN (RSA |EC )?PRIVATE KEY/i)).toHaveCount(0);
    await expect(drawer.getByText(/private_key/i)).toHaveCount(0);
    await expect(page.getByLabel(/private key/i)).toHaveCount(0);
  });
});

test.describe("Platform Domains — lifecycle and danger zone", () => {
  test.beforeEach(() => resetDomainCalls());

  test("keeps Active/Disabled/Suspended and exposes deactivate as a separate confirmed danger-zone action", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "Lifecycle" }).click();

    // Normal status controls survive, unchanged.
    await expect(drawer.getByText("Lifecycle status")).toBeVisible();
    const statusSelect = drawer.getByLabel("New domain status");
    await expect(statusSelect).toBeVisible();
    await expect(statusSelect.getByRole("option", { name: "Disabled" })).toHaveCount(1);
    await expect(statusSelect.getByRole("option", { name: "Suspended" })).toHaveCount(1);

    // Deactivate is a DISTINCT danger-zone action, not a status value.
    const dangerZone = drawer.getByRole("region", { name: "Danger zone" });
    await expect(dangerZone).toBeVisible();
    await expect(dangerZone.getByText(/soft-delete/i)).toBeVisible();

    const deactivateButton = dangerZone.getByRole("button", { name: "Deactivate domain" });
    await expect(deactivateButton).toBeDisabled();
    await dangerZone.getByLabel("Reason").fill("Customer offboarding");
    await expect(deactivateButton).toBeEnabled();
    await deactivateButton.click();

    // Typed confirmation is required before the mutation fires.
    const confirmDialog = page.getByRole("dialog", { name: "Deactivate domain" });
    await expect(confirmDialog).toBeVisible();
    const confirmButton = confirmDialog.getByRole("button", { name: "Deactivate domain" });
    await expect(confirmButton).toBeDisabled();
    expect(domainCalls.filter((c) => /\/deactivate$/.test(c.url))).toHaveLength(0);

    await confirmDialog.getByRole("textbox").fill("DEACTIVATE-DOMAIN-1");
    await expect(confirmButton).toBeEnabled();
    await confirmButton.click();

    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/1\/deactivate$/.test(c.url)).length)
      .toBe(1);
    const deact = domainCalls.find((c) => /\/deactivate$/.test(c.url))!;
    expect(deact.method).toBe("POST");
    expect(JSON.parse(deact.body)).toEqual({
      confirm: "DEACTIVATE-DOMAIN-1",
      reason: "Customer offboarding",
      expected_version: 3,
    });
    expect(deact.headers["idempotency-key"]).toBeTruthy();

    // Post-success the drawer closes and the list reflects server state —
    // the row must not stay visually Active.
    await expect(drawer).toHaveCount(0);
    await expect(page.getByRole("row", { name: /acme\.example/ }).getByText("Active")).toHaveCount(0);
  });
});

test.describe("Platform Domains — stale-version conflict", () => {
  test.beforeEach(() => resetDomainCalls());

  test("a backend 409 on a destructive mutation shows no pretend success and offers a state reload", async ({ page }) => {
    await mockPlatformDomainsAPI(page, { conflictOnFirstMutation: true });
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "beta.example", exact: true }).click();
    const drawer = page.getByLabel("beta.example");
    await drawer.getByRole("tab", { name: "DKIM" }).click();
    await expect(drawer.getByRole("button", { name: "Generate DKIM" })).toBeVisible();

    await drawer.getByRole("button", { name: "Generate DKIM" }).click();

    // The conflict surfaces honestly.
    await expect(drawer.getByRole("alert")).toBeVisible();
    await expect(drawer.getByRole("alert").getByText("Conflict", { exact: true })).toBeVisible();

    // No fabricated success: still reported as not configured, and no
    // invented public record appeared.
    await expect(drawer.getByText("DKIM is not configured")).toBeVisible();
    await expect(drawer.getByText(/p=NEWLYGENERATEDPUBLICKEY/)).toHaveCount(0);

    // The operator is given an explicit way to resync server state.
    await expect(drawer.getByRole("button", { name: "Reload current state" })).toBeVisible();
  });
});
