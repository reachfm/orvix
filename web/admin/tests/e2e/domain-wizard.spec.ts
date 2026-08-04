import { expect, test, type Page } from "@playwright/test";
import { CAPACITY_FIXTURE, mockAPI, openDomains } from "./domains-fixtures";

/**
 * End-to-end coverage of the three-stage domain provisioning wizard, running
 * against the real built bundle with EVERY network call intercepted.
 *
 * Nothing here touches a live API, a real database, a VPS or a DNS resolver.
 * The fixtures in domains-fixtures.ts are the only data the app ever sees, and
 * none of them contain private key material — so the "no private key is
 * rendered" assertions cannot be satisfied by a permissive fixture.
 */

const UNLIMITED_CAPACITY = {
  capacity: {
    ...CAPACITY_FIXTURE.capacity,
    plan: "enterprise",
    max_domains: -1,
    max_domains_unlimited: true,
    remaining_domains: null,
    max_mailboxes: -1,
    max_mailboxes_unlimited: true,
    remaining_mailboxes: null,
  },
};

async function openWizard(page: Page) {
  await page.getByRole("button", { name: "Add Domain" }).click();
  await expect(page.getByTestId("domain-wizard")).toBeVisible();
}

async function fillStageOne(page: Page, name = "newdomain.com") {
  await page.getByLabel("Domain name").fill(name);
  await page.getByLabel("Description (optional)").fill("Provisioned by the e2e suite");
}

async function toCapacity(page: Page) {
  await page.getByRole("button", { name: /Continue/ }).click();
  await expect(page.getByTestId("plan-summary")).toBeVisible();
  await expect(page.getByTestId("plan-summary")).not.toContainText("Loading your plan");
}

async function toReview(page: Page) {
  await page.getByRole("button", { name: /Continue/ }).click();
  await expect(page.getByTestId("wizard-review")).toBeVisible();
}

test.describe("domain provisioning wizard", () => {
  test("opens a modal, not an inline form", async ({ page }) => {
    await mockAPI(page);
    await openDomains(page);

    // The removed inline one-field form must be gone before AND after opening.
    await expect(page.getByLabel("New domain name")).toHaveCount(0);
    await openWizard(page);
    await expect(page.getByLabel("New domain name")).toHaveCount(0);
    await expect(page.getByRole("dialog", { name: "Add a domain" })).toBeVisible();
  });

  test("completes all three stages and creates the domain", async ({ page }) => {
    const counters = await mockAPI(page);
    await openDomains(page);
    await openWizard(page);

    // Stage 1 — live normalization preview.
    await page.getByLabel("Domain name").fill("  NEWDOMAIN.Com.  ");
    await expect(page.getByTestId("normalization-preview")).toContainText("newdomain.com");
    await fillStageOne(page);
    await toCapacity(page);

    // Stage 2 — real plan values from the capacity endpoint.
    const plan = page.getByTestId("plan-summary");
    await expect(plan).toContainText("business");
    await expect(plan).toContainText("500");
    await expect(page.getByTestId("remaining-mailboxes")).toContainText("488 remaining");
    // No organization alias ceiling exists, so it must read Unlimited.
    await expect(page.getByTestId("remaining-aliases")).toContainText("Unlimited remaining");
    // Every control defaults to inherit.
    await expect(page.getByLabel("Maximum mailboxes")).toHaveValue("inherit");
    await expect(page.getByLabel("Maximum aliases")).toHaveValue("inherit");
    await toReview(page);

    // Stage 3 — DKIM on by default, JMAP read-only, review present.
    await expect(page.getByRole("checkbox", { name: /Generate DKIM during provisioning/ })).toBeChecked();
    await expect(page.getByLabel("Selector")).toHaveValue("mail");
    await expect(page.getByTestId("jmap-info")).toContainText("/.well-known/jmap");
    await expect(page.getByTestId("jmap-info").getByRole("checkbox")).toHaveCount(0);
    await expect(page.getByTestId("wizard-review")).toContainText("newdomain.com");
    await expect(page.getByText(/publish the DNS records/i)).toBeVisible();

    await page.getByTestId("wizard-submit").click();

    // The wizard closes, the list refreshes and shows the new row.
    await expect(page.getByTestId("domain-wizard")).toBeHidden();
    await expect(page.getByRole("heading", { name: /Domains \(3\)/ })).toBeVisible();
    // Scoped to the table's own row action: the DNS modal that opens next also
    // renders the domain name in several cells, so a bare text match would be
    // ambiguous and would not prove the TABLE gained a row.
    await expect(page.getByRole("button", { name: "Open DNS records for newdomain.com" })).toBeVisible();

    // The DNS comparison modal opens automatically for the NEW domain.
    await expect(page.getByRole("dialog")).toBeVisible();

    // The generated PUBLIC DKIM record is shown, with the no-DNS-changed note.
    const notice = page.getByTestId("new-dkim-notice");
    await expect(notice).toContainText("mail._domainkey.newdomain.com");
    await expect(notice).toContainText("v=DKIM1");
    await expect(notice).toContainText(/no public DNS was changed/i);

    expect(counters.createDomain).toBe(1);

    // No private key anywhere in the rendered page.
    const html = await page.content();
    for (const marker of ["BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", "private_key"]) {
      expect(html, `page must not contain ${marker}`).not.toContain(marker);
    }
  });

  test("blocks an invalid domain name before any request is made", async ({ page }) => {
    const counters = await mockAPI(page);
    await openDomains(page);
    await openWizard(page);

    await page.getByLabel("Domain name").fill("not a domain");
    await expect(page.getByTestId("normalization-preview")).toContainText(/not allowed|not valid|Wildcards/i);
    await page.getByRole("button", { name: /Continue/ }).click();

    await expect(page.getByTestId("wizard-error-summary")).toBeVisible();
    // Still on stage 1 and nothing was sent.
    await expect(page.getByTestId("plan-summary")).toHaveCount(0);
    expect(counters.createDomain).toBe(0);
  });

  test("enforces the plan limit client-side before submitting", async ({ page }) => {
    const counters = await mockAPI(page);
    await openDomains(page);
    await openWizard(page);
    await fillStageOne(page);
    await toCapacity(page);

    // 500 ceiling with 100 already allocated leaves 400; 450 must be rejected.
    await page.getByLabel("Maximum mailboxes").selectOption("custom");
    await page.getByLabel("Maximum mailboxes value").fill("450");
    await page.getByRole("button", { name: /Continue/ }).click();

    await expect(page.getByTestId("wizard-error-summary")).toContainText("400 mailboxes remain");
    await expect(page.getByTestId("wizard-review")).toHaveCount(0);
    expect(counters.createDomain).toBe(0);

    // A value inside the remaining allowance passes.
    await page.getByLabel("Maximum mailboxes value").fill("50");
    await toReview(page);
    await expect(page.getByTestId("wizard-review")).toContainText("50");
  });

  test("does not offer Unlimited under a finite plan, and does under an unlimited one", async ({ page }) => {
    await mockAPI(page);
    await openDomains(page);
    await openWizard(page);
    await fillStageOne(page);
    await toCapacity(page);
    await expect(page.getByLabel("Maximum mailboxes").getByRole("option", { name: "Unlimited" })).toHaveCount(0);

    await page.reload({ waitUntil: "domcontentloaded" });
    await mockAPI(page, { capacity: UNLIMITED_CAPACITY });
    await openDomains(page);
    await openWizard(page);
    await fillStageOne(page);
    await toCapacity(page);

    await expect(page.getByTestId("remaining-mailboxes")).toContainText("Unlimited remaining");
    await expect(page.getByTestId("plan-summary")).not.toContainText("0 remaining");
    await expect(page.getByLabel("Maximum mailboxes").getByRole("option", { name: "Unlimited" })).toHaveCount(1);
  });

  test("a double click issues exactly ONE create request", async ({ page }) => {
    // Hold the response open so both clicks land while the first is in flight,
    // which is precisely the double-submit the guard must absorb.
    let release!: () => void;
    const gate = new Promise<void>((r) => { release = r; });

    const counters = await mockAPI(page);
    await page.route("**/api/v1/enterprise/domains", async (route) => {
      if (route.request().method() !== "POST") return route.fallback();
      counters.createDomain += 1;
      await gate;
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify((await import("./domains-fixtures")).PROVISION_RESPONSE),
      });
    });

    await openDomains(page);
    await openWizard(page);
    await fillStageOne(page);
    await toCapacity(page);
    await toReview(page);

    const submit = page.getByTestId("wizard-submit");
    await submit.click();
    // The button disables immediately; force the second click so the test
    // exercises the in-flight guard rather than relying on the disabled attr.
    await submit.click({ force: true, timeout: 2000 }).catch(() => {});
    await expect(submit).toBeDisabled();

    release();
    await expect(page.getByTestId("domain-wizard")).toBeHidden();
    expect(counters.createDomain, "a double click must not create two domains").toBe(1);
  });

  test("keeps the wizard open and preserves input when the server rejects", async ({ page }) => {
    await mockAPI(page, {
      createDomain: {
        status: 409,
        body: { code: "DOMAIN_ALREADY_EXISTS", message: "Domain already exists." },
      },
    });
    await openDomains(page);
    await openWizard(page);
    await fillStageOne(page, "taken.com");
    await toCapacity(page);
    await toReview(page);
    await page.getByTestId("wizard-submit").click();

    await expect(page.getByTestId("domain-wizard")).toBeVisible();
    await expect(page.getByTestId("wizard-error-summary")).toContainText(/already configured/i);
    // Routed back to the stage that owns the name, with the value intact.
    await expect(page.getByLabel("Domain name")).toHaveValue("taken.com");
    await expect(page.getByLabel("Description (optional)")).toHaveValue("Provisioned by the e2e suite");
    // No success state and no phantom row.
    await expect(page.getByTestId("new-dkim-notice")).toHaveCount(0);
  });

  test("warns before discarding entered data, and closes silently when empty", async ({ page }) => {
    await mockAPI(page);
    await openDomains(page);

    // Nothing entered -> closes with no warning.
    await openWizard(page);
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("domain-wizard")).toBeHidden();

    // Something entered -> warns.
    await openWizard(page);
    await page.getByLabel("Domain name").fill("typed.com");
    await page.keyboard.press("Escape");
    await expect(page.getByText("Discard this domain?")).toBeVisible();
    await page.getByRole("button", { name: "Keep editing" }).click();
    await expect(page.getByTestId("domain-wizard")).toBeVisible();
    await expect(page.getByLabel("Domain name")).toHaveValue("typed.com");
  });
});
