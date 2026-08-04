import { test, type Page } from "@playwright/test";
import { copyFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { mockAPI, openDomains } from "./domains-fixtures";

/**
 * Reviewer-facing screenshots of the domain provisioning wizard, captured
 * against the real built bundle and the same fully-mocked fixtures the
 * functional spec uses. No live API, no live DNS, no VPS, no real domain.
 *
 * Following the PR#52 convention, every shot is written twice:
 *
 *   - <repo>/.artifacts/                            local, gitignored scratch;
 *   - <repo>/docs/review-artifacts/domain-wizard/   committed, so a reviewer
 *                                                   can open the images from
 *                                                   the PR itself.
 */

const ARTIFACTS = join(process.cwd(), "..", "..", ".artifacts");
const REVIEW = join(process.cwd(), "..", "..", "docs", "review-artifacts", "domain-wizard");

async function shot(page: Page, name: string) {
  await page.screenshot({ path: join(ARTIFACTS, name), fullPage: true });
  copyFileSync(join(ARTIFACTS, name), join(REVIEW, name));
}

/** Drives the wizard to the requested stage, capturing each one on the way. */
async function walkStages(page: Page, prefix: string) {
  await openDomains(page);
  await page.getByRole("button", { name: "Add Domain" }).click();
  await page.getByTestId("domain-wizard").waitFor();

  // Stage 1 — domain identity, with the live normalization preview populated.
  await page.getByLabel("Domain name").fill("  NEWDOMAIN.Com  ");
  await page.getByLabel("Description (optional)").fill("Corporate mail domain for the EU region.");
  await page.getByTestId("normalization-preview").waitFor();
  await shot(page, `${prefix}-stage1-domain.png`);

  // Stage 2 — capacity, with the plan card resolved and a custom limit set so
  // both the inherit and the explicit paths are visible in one frame.
  await page.getByRole("button", { name: /Continue/ }).click();
  await page.getByTestId("plan-summary").waitFor();
  await page.getByTestId("remaining-mailboxes").waitFor();
  await page.getByLabel("Maximum mailboxes").selectOption("custom");
  await page.getByLabel("Maximum mailboxes value").fill("50");
  await shot(page, `${prefix}-stage2-capacity.png`);

  // Stage 3 — DKIM, the read-only JMAP card and the review summary.
  await page.getByRole("button", { name: /Continue/ }).click();
  await page.getByTestId("wizard-review").waitFor();
  await shot(page, `${prefix}-stage3-security-review.png`);

  // Success — the DNS modal opened automatically with the public DKIM record.
  await page.getByTestId("wizard-submit").click();
  await page.getByTestId("new-dkim-notice").waitFor();
  await shot(page, `${prefix}-success-dns-modal.png`);
}

test.describe("domain wizard screenshots", () => {
  test.beforeAll(() => {
    mkdirSync(ARTIFACTS, { recursive: true });
    mkdirSync(REVIEW, { recursive: true });
  });

  test("captures all three stages and the success state on desktop", async ({ page }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "desktop project only");
    await page.setViewportSize({ width: 1600, height: 1000 });
    await mockAPI(page);
    await walkStages(page, "wizard-desktop");
  });

  test("captures all three stages and the success state on mobile", async ({ page }) => {
    test.skip(test.info().project.name !== "mobile-chromium", "mobile project only");
    await mockAPI(page);
    await walkStages(page, "wizard-mobile");
  });

  test("captures the plan-limit validation error", async ({ page }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "desktop project only");
    await page.setViewportSize({ width: 1600, height: 1000 });
    await mockAPI(page);
    await openDomains(page);
    await page.getByRole("button", { name: "Add Domain" }).click();
    await page.getByLabel("Domain name").fill("overflow.com");
    await page.getByRole("button", { name: /Continue/ }).click();
    await page.getByTestId("remaining-mailboxes").waitFor();

    // 500 ceiling with 100 already allocated leaves 400 — 450 is rejected.
    await page.getByLabel("Maximum mailboxes").selectOption("custom");
    await page.getByLabel("Maximum mailboxes value").fill("450");
    await page.getByRole("button", { name: /Continue/ }).click();
    await page.getByTestId("wizard-error-summary").waitFor();
    await shot(page, "wizard-desktop-plan-limit-error.png");
  });

  test("captures a typed backend error preserving the entered values", async ({ page }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "desktop project only");
    await page.setViewportSize({ width: 1600, height: 1000 });
    await mockAPI(page, {
      createDomain: {
        status: 409,
        body: { code: "DOMAIN_ALREADY_EXISTS", message: "Domain already exists." },
      },
    });
    await openDomains(page);
    await page.getByRole("button", { name: "Add Domain" }).click();
    await page.getByLabel("Domain name").fill("taken.com");
    await page.getByRole("button", { name: /Continue/ }).click();
    await page.getByTestId("remaining-mailboxes").waitFor();
    await page.getByRole("button", { name: /Continue/ }).click();
    await page.getByTestId("wizard-review").waitFor();
    await page.getByTestId("wizard-submit").click();
    await page.getByTestId("wizard-error-summary").waitFor();
    await shot(page, "wizard-desktop-backend-error.png");
  });
});
