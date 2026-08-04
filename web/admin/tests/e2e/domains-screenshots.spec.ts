import { test, type Page } from "@playwright/test";
import { copyFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { DNS_HEALTH_FIXTURE, openDNSModal, openDomains, mockAPI } from "./domains-fixtures";

/**
 * Captures reviewer-facing screenshots of the rebuilt Domains console against
 * the real built bundle and the same fully-mocked fixtures used by the
 * functional e2e spec. No live API, no live DNS, no real domain.
 *
 * Every shot is written TWICE:
 *
 *   - <repo>/.artifacts/                  — local, gitignored scratch copy;
 *   - <repo>/docs/review-artifacts/pr52/  — committed, so a reviewer can
 *                                           actually open the images from the
 *                                           PR rather than read a description
 *                                           of them.
 */

const ARTIFACTS = join(process.cwd(), "..", "..", ".artifacts");
const REVIEW = join(process.cwd(), "..", "..", "docs", "review-artifacts", "pr52");

/** Captures one screenshot into both output directories. */
async function shot(page: Page, name: string) {
  await page.screenshot({ path: join(ARTIFACTS, name), fullPage: true });
  copyFileSync(join(ARTIFACTS, name), join(REVIEW, name));
}

test.use({ viewport: { width: 1600, height: 1000 } });

test.describe("screenshots", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "chromium only");

  test.beforeAll(() => {
    mkdirSync(ARTIFACTS, { recursive: true });
    mkdirSync(REVIEW, { recursive: true });
  });

  test("captures the domains table, the DNS modal and the cooldown state", async ({ page }) => {
    await mockAPI(page);

    // (a) Full desktop Domains table.
    await openDomains(page);
    await shot(page, "domains-table-desktop.png");

    // (b) DNS modal with a mix of pass / fail / warning / optional /
    //     not_applicable rows — including the autodiscover SRV row in its
    //     wrong-target warning state.
    await openDNSModal(page);
    await shot(page, "dns-modal-mixed-statuses.png");

    // The modal body scrolls internally, so the optional/not_applicable rows
    // at the bottom need their own capture.
    await page.getByTestId("dns-row-tlsa").scrollIntoViewIfNeeded();
    await shot(page, "dns-modal-optional-rows.png");

    // (c) Cooldown + incomplete state after a verify call.
    await page.getByRole("button", { name: /Check DNS now/i }).click();
    await page.getByRole("button", { name: /Retry in/i }).waitFor();
    await shot(page, "dns-modal-cooldown-incomplete.png");
  });

  // (d) The new autodiscover SRV row in a NON-PASSING state, scrolled into
  //     view so the required value, the observed answer, the mismatch reason
  //     and the repair guidance are all legible.
  test("captures the autodiscover SRV row in a failing state", async ({ page }) => {
    await mockAPI(page, {
      dnsHealth: {
        ...DNS_HEALTH_FIXTURE,
        autodiscover_srv: {
          name: "_autodiscover._tcp.example.com",
          type: "SRV",
          status: "warning",
          optional: true,
          expected: "0 0 443 mail.example.com.",
          observed: ["0 0 80 legacy.elsewhere.example."],
          reason:
            "published autodiscover SRV does not match the expected endpoint: target legacy.elsewhere.example (want mail.example.com)",
          guidance:
            'Optional. Publish an SRV record at _autodiscover._tcp.example.com with the value "0 0 443 mail.example.com." (priority 0, weight 0, port 443, target mail.example.com).',
          checked_at: "2026-01-01T00:00:00Z",
        },
      },
    });

    await openDomains(page);
    await openDNSModal(page);
    await page.getByTestId("dns-row-autodiscover-srv").scrollIntoViewIfNeeded();
    await shot(page, "dns-modal-autodiscover-srv-mismatch.png");
  });
});
