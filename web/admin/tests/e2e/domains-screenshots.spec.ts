import { test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { openDNSModal, openDomains, mockAPI } from "./domains-fixtures";

/**
 * Captures reviewer-facing screenshots of the rebuilt Domains console against
 * the real built bundle and the same fully-mocked fixtures used by the
 * functional e2e spec. No live API, no live DNS, no real domain.
 *
 * Artifacts land in <repo>/.artifacts/ and are NOT committed — the repository
 * has no convention for storing binary UI snapshots in-tree.
 */

const ARTIFACTS = join(process.cwd(), "..", "..", ".artifacts");

test.use({ viewport: { width: 1600, height: 1000 } });

test.describe("screenshots", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "chromium only");

  test("captures the domains table, the DNS modal and the cooldown state", async ({ page }) => {
    mkdirSync(ARTIFACTS, { recursive: true });
    await mockAPI(page);

    // (a) Full desktop Domains table.
    await openDomains(page);
    await page.screenshot({ path: join(ARTIFACTS, "domains-table-desktop.png"), fullPage: true });

    // (b) DNS modal with a mix of pass / fail / optional / not_applicable rows.
    await openDNSModal(page);
    await page.screenshot({ path: join(ARTIFACTS, "dns-modal-mixed-statuses.png"), fullPage: true });

    // The modal body scrolls internally, so the optional/not_applicable rows
    // at the bottom need their own capture.
    await page.getByTestId("dns-row-tlsa").scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(ARTIFACTS, "dns-modal-optional-rows.png"), fullPage: true });

    // (c) Cooldown + incomplete state after a verify call.
    await page.getByRole("button", { name: /Check DNS now/i }).click();
    await page.getByRole("button", { name: /Retry in/i }).waitFor();
    await page.screenshot({ path: join(ARTIFACTS, "dns-modal-cooldown-incomplete.png"), fullPage: true });
  });
});
