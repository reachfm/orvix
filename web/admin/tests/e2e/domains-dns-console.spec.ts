import { expect, test } from "@playwright/test";
import {
  DNS_HEALTH_FIXTURE, mockAPI, openDNSModal, openDomains,
} from "./domains-fixtures";

test.beforeEach(async ({ page }) => {
  await mockAPI(page);
});

test("Domains page renders the dense table with real aggregate columns", async ({ page }) => {
  await openDomains(page);

  for (const header of [
    "Domain", "Aliases", "Mailboxes", "Storage", "Messages",
    "DKIM", "DMARC", "DNS health", "Status", "Actions",
  ]) {
    await expect(page.getByRole("columnheader", { name: header })).toBeVisible();
  }

  const row = page.getByRole("row").filter({ hasText: "example.com" }).first();
  await expect(row).toContainText("18,432");   // message_count
  await expect(row).toContainText("5.0 GB / 10.0 GB"); // storage used / limit
  await expect(row).toContainText("72%");      // dns_score
  await expect(row).toContainText("warning");  // dns_health

  // A domain with no completed check shows an explicit textual state — never
  // a fabricated "0%", which would read as "failed every check".
  const unchecked = page.getByRole("row").filter({ hasText: "never-checked.com" }).first();
  await expect(unchecked).toContainText("Not checked");
  await expect(unchecked).not.toContainText("0%");

  // The inert bulk-selection UI is gone.
  await expect(page.locator('input[type="checkbox"]')).toHaveCount(0);
  await expect(page.getByText(/\d+ selected/)).toHaveCount(0);
});

test("the dedicated DNS button — not a row click — opens the modal for the right domain", async ({ page }) => {
  await openDomains(page);

  // Clicking the row body must NOT open the dialog.
  await page.getByRole("cell", { name: "example.com", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await openDNSModal(page);
  await expect(page.getByRole("dialog")).toContainText("DNS Records");
  await expect(page.locator("#dns-records-title")).toContainText("example.com");
});

test("every record row renders name, type, required, current, status, reason and guidance", async ({ page }) => {
  await openDomains(page);
  await openDNSModal(page);

  const table = page.getByTestId("dns-records-table");
  for (const header of ["Name", "Type", "Required Data", "Current DNS", "Status", "Action"]) {
    await expect(table.getByRole("columnheader", { name: header })).toBeVisible();
  }

  // The full expanded inventory is present.
  for (const key of [
    "mx", "mx-host-0", "mail-host-a", "mail-host-aaaa", "ptr",
    "spf", "dkim", "dmarc", "mtasts", "mtasts-policy", "tlsrpt",
    "autodiscover", "autoconfig", "tlsa",
  ]) {
    await expect(page.getByTestId(`dns-row-${key}`)).toBeVisible();
  }

  // Required values are always real, never a bare dash.
  await expect(page.getByTestId("required-spf")).toHaveText("v=spf1 mx -all");
  await expect(page.getByTestId("required-dmarc")).toHaveText(
    "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"
  );

  // Current observed values.
  await expect(page.getByTestId("observed-mail-host-a")).toHaveText("192.0.2.25");
  await expect(page.getByTestId("observed-dmarc")).toHaveText("Not present");

  // Status + exact reason + repair guidance.
  await expect(page.getByTestId("dns-row-ptr")).toContainText("Fail");
  await expect(page.getByTestId("reason-ptr")).toContainText("no PTR record published for 192.0.2.25");
  await expect(page.getByTestId("guidance-ptr")).toContainText("reverse DNS (PTR) record to mail.example.com");
  await expect(page.getByTestId("guidance-mx")).toContainText("priority 10");

  // Optional / not-applicable rows are labelled, not hidden and not failures.
  await expect(page.getByTestId("dns-row-mail-host-aaaa")).toContainText("Optional");
  await expect(page.getByTestId("dns-row-autodiscover")).toContainText("Optional");
  await expect(page.getByTestId("dns-row-autoconfig")).toContainText("Optional");
  await expect(page.getByTestId("dns-row-tlsa")).toContainText("Not applicable");
  await expect(page.getByTestId("dns-row-tlsa")).not.toContainText("Fail");

  // Each row exposes a copy action for its required value.
  await expect(page.getByRole("button", { name: /^Copy / })).not.toHaveCount(0);
});

test("a high score with complete:false never renders as a full pass", async ({ page }) => {
  await openDomains(page);
  await openDNSModal(page);

  const summary = page.getByTestId("health-summary");
  await expect(summary).toContainText("96%");
  // complete:false ⇒ the incomplete indicator replaces the pass pill.
  await expect(page.getByTestId("incomplete-indicator")).toBeVisible();
  await expect(summary).toContainText("Incomplete");
  await expect(summary).not.toContainText("100%");
  await expect(summary.getByText("Pass", { exact: true })).toHaveCount(0);

  // And the MTA-STS HTTPS policy row explains why a full pass is impossible.
  await expect(page.getByTestId("dns-row-mtasts-policy")).toContainText("Fail");
});

test("cooldown disables the check button and a second click issues no second POST", async ({ page }) => {
  const counters = await mockAPI(page);
  await openDomains(page);
  await openDNSModal(page);

  const checkButton = page.getByRole("button", { name: /Check DNS now/i });
  await checkButton.click();

  // The verify response carries a cooldown window, so the button becomes a
  // disabled "Retry in …" countdown.
  const retryButton = page.getByRole("button", { name: /Retry in/i });
  await expect(retryButton).toBeVisible();
  await expect(retryButton).toBeDisabled();
  await expect(page.getByTestId("cooldown-notice")).toBeVisible();

  expect(counters.verify).toBe(1);

  // A second click inside the window must not reach the network.
  await retryButton.click({ force: true }).catch(() => { /* disabled */ });
  await page.waitForTimeout(500);
  expect(counters.verify).toBe(1);
});

test("the modal closes via the close button and via Escape, returning focus to the trigger each time", async ({ page }) => {
  await openDomains(page);
  const trigger = page.getByRole("button", { name: "Open DNS records for example.com" });

  // ── Close button ──
  await trigger.click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByRole("button", { name: "Close DNS records dialog" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(trigger).toBeFocused();

  // ── Escape ──
  await trigger.click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test("DKIM rotation asks for confirmation and shows only the new public record", async ({ page }) => {
  const counters = await mockAPI(page);
  await openDomains(page);
  await openDNSModal(page);

  await page.getByRole("button", { name: /Rotate DKIM key/i }).click();

  // A confirmation step is mandatory, and it warns about propagation.
  const confirm = page.getByRole("dialog").filter({ hasText: "Rotate DKIM key for example.com?" });
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText("up to 48 hours");
  expect(counters.rotate).toBe(0);

  // Cancelling issues no request at all.
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(confirm).toHaveCount(0);
  expect(counters.rotate).toBe(0);

  // Confirming does.
  await page.getByRole("button", { name: /Rotate DKIM key/i }).click();
  await page.getByRole("button", { name: "Rotate key", exact: true }).click();

  const pending = page.getByTestId("dkim-pending");
  await expect(pending).toBeVisible();
  await expect(pending).toContainText("Pending propagation");
  await expect(pending).toContainText("v=DKIM1; k=rsa; p=ROTATEDPUBLICKEY");
  // The private key is never part of the response and must never appear.
  await expect(pending).not.toContainText("PRIVATE KEY");
  expect(counters.rotate).toBe(1);
});

test("the download button produces the expected records file", async ({ page }) => {
  await openDomains(page);
  await openDNSModal(page);

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: /Download DNS Records/i }).click(),
  ]);

  expect(download.suggestedFilename()).toBe("example.com-dns-records.txt");

  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(Buffer.from(c));
  const contents = chunks.join("");

  expect(contents).toContain("DNS records for example.com");
  expect(contents).toContain("NAME | TYPE | PRIORITY | STATUS | REQUIRED VALUE");
  expect(contents).toContain("example.com | MX | 10 | pass | mail.example.com");
  expect(contents).toContain("v=spf1 mx -all");
  expect(contents).toContain("v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com");
  expect(contents).toContain("_25._tcp.mail.example.com | TLSA | - | not_applicable");
  // The incomplete state is carried into the exported file too.
  expect(contents).toContain("complete: false");
  expect(contents).toContain("WARNING: the last check was incomplete");
  // No secrets, ever.
  expect(contents).not.toContain("PRIVATE KEY");
  expect(contents).not.toContain("csrf");
});

test("DKIM generation is offered when no key is configured and shows only the public record", async ({ page }) => {
  const counters = await mockAPI(page);
  // Re-mock the DNS payload with DKIM unconfigured (registered after the
  // earlier route, so it wins).
  await page.route("**/api/v1/enterprise/domains/*/dns", (r) =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ...DNS_HEALTH_FIXTURE,
        dkim: { selector: "", status: "fail", configured: false, reason: "DKIM record not found" },
      }),
    })
  );

  await openDomains(page);
  await openDNSModal(page);

  // Rotate must NOT be offered for a domain with no key.
  await expect(page.getByRole("button", { name: /Rotate DKIM key/i })).toHaveCount(0);

  await page.getByRole("button", { name: /Generate DKIM key/i }).click();

  const pending = page.getByTestId("dkim-pending");
  await expect(pending).toBeVisible();
  await expect(pending).toContainText("Pending propagation");
  await expect(pending).toContainText("v=DKIM1; k=rsa; p=GENERATEDPUBLICKEY");
  await expect(pending).toContainText("mail._domainkey.example.com");
  await expect(pending).not.toContainText("PRIVATE KEY");
  expect(counters.generate).toBe(1);
  // The DKIM row reflects the pending state rather than claiming a pass.
  await expect(page.getByTestId("dns-row-dkim")).toContainText("Pending propagation");
});
