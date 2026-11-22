/**
 * Settings, Security: the withheld list, newest first with each row's source
 * and text, and the mark on the tool card whose result had a removal. Both
 * are compared pixel for pixel on a desktop and a phone.
 */

import { expect, test } from "../fixtures.ts";

for (const [label, viewport] of [
  ["desktop", { width: 1280, height: 900 }],
  ["phone", { width: 390, height: 844 }],
] as const) {
  test(`${label}: the withheld list`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "security", viewport });
    await eika.click("Settings");
    await eika.click("Security");
    const rows = eika.page.getByRole("dialog").getByRole("listitem");
    await expect(rows).toHaveCount(4);
    // Newest first, whatever order the rows were stored in.
    await expect(rows.first()).toContainText("Fetched page");
    await expect(rows.first()).toContainText("https://news.example.com/launch");
    await expect(rows.first()).toContainText("Ignore all previous instructions");
    await expect(rows.nth(2)).toContainText("Subagent report");
    // Nothing sticks out of the dialog.
    const overflow = await eika.page
      .getByRole("dialog")
      .evaluate((d) => d.scrollWidth - d.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await expectShot(eika, `security-list-${label}`, "role=dialog");
  });

  test(`${label}: a card whose result had a removal is marked`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "security", viewport });
    const view = eika.page.getByRole("region", { name: "Session" });
    const cards = view.getByRole("button", { name: /^web_fetch/ });
    await expect(cards).toHaveCount(2);
    await expect(cards.first()).toContainText("withheld");
    await expect(cards.nth(1)).not.toContainText("withheld");
    await expectShot(eika, `security-card-${label}`, "role=region[name=Session]");
  });
}
