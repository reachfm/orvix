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
    await dangerZone.getByLabel("Deactivate reason").fill("Customer offboarding");
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

  test("blocks permanent delete until the domain is deactivated, then deletes it as a distinct, separately confirmed action", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "Lifecycle" }).click();

    const dangerZone = drawer.getByRole("region", { name: "Danger zone" });
    const deleteButton = dangerZone.getByRole("button", { name: "Delete domain" });
    await expect(deleteButton).toBeVisible();
    await expect(deleteButton).toBeDisabled();

    // Attempting delete before deactivation must be blocked with the
    // backend's structured guidance, not a fake client-side removal.
    await dangerZone.getByLabel("Delete reason").fill("Customer offboarding, fully wound down");
    await expect(deleteButton).toBeEnabled();
    await deleteButton.click();
    const deleteConfirmDialog = page.getByRole("dialog", { name: "Delete domain" });
    await expect(deleteConfirmDialog).toBeVisible();
    await deleteConfirmDialog.getByRole("textbox").fill("DELETE-DOMAIN-1");
    await deleteConfirmDialog.getByRole("button", { name: "Delete domain" }).click();

    await expect(page.getByText(/Deactivate the domain before deleting it permanently/i)).toBeVisible();
    // The drawer must remain open — a blocked deletion is not a fake success.
    await expect(drawer).toBeVisible();

    // Close the still-open delete confirmation dialog before continuing.
    await page.keyboard.press("Escape");
    await expect(deleteConfirmDialog).toHaveCount(0);

    // Now deactivate first, per the documented policy.
    await dangerZone.getByLabel("Deactivate reason").fill("Customer offboarding");
    await dangerZone.getByRole("button", { name: "Deactivate domain" }).click();
    const deactivateConfirmDialog = page.getByRole("dialog", { name: "Deactivate domain" });
    await deactivateConfirmDialog.getByRole("textbox").fill("DEACTIVATE-DOMAIN-1");
    await deactivateConfirmDialog.getByRole("button", { name: "Deactivate domain" }).click();
    await expect(drawer).toHaveCount(0);

    // Re-open and delete — now permitted.
    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer2 = page.getByLabel("acme.example");
    await drawer2.getByRole("tab", { name: "Lifecycle" }).click();
    const dangerZone2 = drawer2.getByRole("region", { name: "Danger zone" });
    await dangerZone2.getByLabel("Delete reason").fill("Customer offboarding, fully wound down");
    await dangerZone2.getByRole("button", { name: "Delete domain" }).click();
    const deleteConfirmDialog2 = page.getByRole("dialog", { name: "Delete domain" });
    await deleteConfirmDialog2.getByRole("textbox").fill("DELETE-DOMAIN-1");
    await deleteConfirmDialog2.getByRole("button", { name: "Delete domain" }).click();

    // Two delete calls total: the earlier blocked attempt (409
    // DOMAIN_NOT_DEACTIVATED) plus this successful one — assert on the
    // LAST one, which is the successful, post-deactivation request.
    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/1\/delete$/.test(c.url)).length)
      .toBe(2);
    const deleteCalls = domainCalls.filter((c) => /\/delete$/.test(c.url));
    const del = deleteCalls[deleteCalls.length - 1];
    expect(del.method).toBe("POST");
    const delBody = JSON.parse(del.body);
    expect(delBody.confirm).toBe("DELETE-DOMAIN-1");
    expect(delBody.reason).toBe("Customer offboarding, fully wound down");
    expect(del.headers["idempotency-key"]).toBeTruthy();

    // Successful delete closes the drawer — no fake client-only removal.
    await expect(drawer2).toHaveCount(0);
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

test.describe("Platform Domains — live DNS verification", () => {
  test.beforeEach(() => resetDomainCalls());

  test("matching records render a green Matched state with visible text, not color alone", async ({ page }) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("All DNS records match")).toBeVisible();
    await expect(drawer.getByText(/3 of 3 matched/)).toBeVisible();
    await expect(drawer.getByText("Matched").first()).toBeVisible();

    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/1\/dns\/verify$/.test(c.url) && c.method === "POST").length)
      .toBeGreaterThan(0);
  });

  test("mismatching records render a red Mismatch state with the actual published value visible", async ({ page }) => {
    await mockPlatformDomainsAPI(page, {
      verifyResponses: {
        1: [{
          tenant_id: 7, domain_id: 1, domain: "acme.example", checked_at: "2026-01-05T08:55:00Z",
          records: [
            { name: "acme.example", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing", status: "verified", verified: true, observed: "10 mail.orvix.email" },
            { name: "acme.example", type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF", status: "mismatch", verified: false, observed: "v=spf1 include:wrongvendor.example ~all", reason: "SPF exists but differs from generated plan" },
          ],
          total_count: 2, matched_count: 1, issue_count: 1, all_verified: false,
        }],
      },
    });
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("DNS configuration needs attention")).toBeVisible();
    await expect(drawer.getByText("Mismatch")).toBeVisible();
    await expect(drawer.getByText("v=spf1 include:orvix.email ~all")).toBeVisible();
    await expect(drawer.getByText("v=spf1 include:wrongvendor.example ~all")).toBeVisible();
  });

  test("missing records render a red Missing state with Actual: Not found", async ({ page }) => {
    await mockPlatformDomainsAPI(page, {
      verifyResponses: {
        2: [{
          tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:55:00Z",
          records: [
            { name: "beta.example", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing", status: "missing", verified: false, reason: "no MX records found" },
            { name: "beta.example", type: "TXT", value: "v=spf1 include:orvix.email ~all", ttl: 3600, required: true, purpose: "SPF", status: "missing", verified: false, reason: "no SPF TXT record found" },
          ],
          total_count: 2, matched_count: 0, issue_count: 2, all_verified: false,
        }],
      },
    });
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "beta.example", exact: true }).click();
    const drawer = page.getByLabel("beta.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("Missing").first()).toBeVisible();
    await expect(drawer.getByText("Not found").first()).toBeVisible();
  });

  test("DKIM is never falsely green for a wrong/old public key — the DKIM row in DNS Setup shows Mismatch", async ({ page }) => {
    await mockPlatformDomainsAPI(page, {
      verifyResponses: {
        1: [{
          tenant_id: 7, domain_id: 1, domain: "acme.example", checked_at: "2026-01-05T08:55:00Z",
          records: [
            { name: "mail._domainkey.acme.example", type: "TXT", value: "v=DKIM1; k=rsa; p=CURRENTPUBLICKEYDATA", ttl: 3600, required: true, purpose: "dkim", status: "mismatch", verified: false, observed: "v=DKIM1; k=rsa; p=OLDROTATEDOUTKEY", reason: "DKIM record exists but public key differs" },
          ],
          total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
        }],
      },
    });
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("Mismatch")).toBeVisible();
    await expect(drawer.getByText(/OLDROTATEDOUTKEY/)).toBeVisible();
    // The DKIM row specifically must never read as Matched.
    const dkimRow = drawer.locator("li", { has: page.getByText("dkim") });
    await expect(dkimRow.getByText("Matched")).toHaveCount(0);
  });

  test("Re-check DNS re-issues verification and updates the rendered status from Missing to Matched", async ({ page }) => {
    await mockPlatformDomainsAPI(page, {
      verifyResponses: {
        2: [
          {
            tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:00:00Z",
            records: [{ name: "beta.example", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing", status: "missing", verified: false, reason: "no MX records found" }],
            total_count: 1, matched_count: 0, issue_count: 1, all_verified: false,
          },
          {
            tenant_id: 7, domain_id: 2, domain: "beta.example", checked_at: "2026-01-05T08:10:00Z",
            records: [{ name: "beta.example", type: "MX", value: "mail.orvix.email", ttl: 3600, priority: 10, required: true, purpose: "inbound mail routing", status: "verified", verified: true, observed: "10 mail.orvix.email" }],
            total_count: 1, matched_count: 1, issue_count: 0, all_verified: true,
          },
        ],
      },
    });
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "beta.example", exact: true }).click();
    const drawer = page.getByLabel("beta.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("Missing").first()).toBeVisible();

    await drawer.getByRole("button", { name: "Re-check DNS" }).click();

    await expect(drawer.getByText("All DNS records match")).toBeVisible();
    await expect(drawer.getByText("Matched").first()).toBeVisible();
    await expect
      .poll(() => domainCalls.filter((c) => /\/platform\/domains\/7\/2\/dns\/verify$/.test(c.url)).length)
      .toBe(2);
  });

  test("desktop and mobile viewports both render the verification summary and per-record status usably", async ({ page }, testInfo) => {
    await mockPlatformDomainsAPI(page);
    await openDomainsPage(page);

    await page.getByRole("cell", { name: "acme.example", exact: true }).click();
    const drawer = page.getByLabel("acme.example");
    await drawer.getByRole("tab", { name: "DNS Setup" }).click();

    await expect(drawer.getByText("All DNS records match")).toBeVisible();
    await expect(drawer.getByRole("button", { name: "Re-check DNS" })).toBeVisible();
    // Sanity: the project actually varies viewport (desktop-chrome vs mobile-chromium).
    expect(testInfo.project.name).toMatch(/desktop-chrome|mobile-chromium/);
  });
});
