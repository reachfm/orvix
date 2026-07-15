import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

for (const path of ["/", "/pricing", "/features", "/security", "/contact", "/legal/privacy"]) {
  test(`${path} has no serious or critical axe violations`, async ({ page }) => {
    await page.goto(path, { waitUntil: "networkidle" });
    const results = await new AxeBuilder({ page }).analyze();
    const violations = results.violations.filter((item) => item.impact === "serious" || item.impact === "critical");
    expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
  });
}
