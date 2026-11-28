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
    await expectShot(eika, `security-card-${label}`, "xpath=(//*[@data-transcript-row])[1]/..");
  });
}

for (const [label, viewport] of [
  ["desktop", { width: 1280, height: 900 }],
  ["phone", { width: 390, height: 844 }],
] as const) {
  test(`${label}: guard down and trusted sources`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "security-guard-down", viewport });
    await expect(
      eika.page.getByRole("status").filter({ hasText: "guard is not responding" }),
    ).toContainText("blocked");
    await expectShot(eika, `guard-down-${label}`);
    await eika.click("Settings");
    await eika.click("Security");
    const dialog = eika.page.getByRole("dialog");
    await expect(dialog.getByRole("switch", { name: "Require screening" })).toBeChecked();
    await expect(dialog.getByText("example.com", { exact: true })).toBeVisible();
    await expect(dialog.getByText("blocked.example", { exact: true })).toBeVisible();
    await expectShot(eika, `security-trusted-${label}`, "role=dialog");
  });
}

for (const [label, viewport] of [
  ["desktop", { width: 1280, height: 900 }],
  ["phone", { width: 390, height: 844 }],
] as const) {
  test(`${label}: tripwires show their counts and save a switch and the numbers`, async ({
    open,
    expectShot,
  }) => {
    const eika = await open({ scenario: "security-tripwires", viewport });
    await eika.click("Settings");
    await eika.click("Security");
    const dialog = eika.page.getByRole("dialog");
    const followed = dialog.getByRole("switch", {
      name: "A suspect page's destination was followed",
    });
    await expect(followed).toBeChecked();
    await expect(dialog.getByText("Fired 2, this was fine 1")).toBeVisible();
    await expect(dialog.getByText("Fired 1, this was fine 0")).toBeVisible();
    await dialog.getByRole("switch", { name: "A large amount of data was sent out" }).click();
    await expect
      .poll(() => eika.mock.world.settings.settings.tripwires)
      .toMatchObject({ followed: true, drained: true });
    await dialog.getByLabel("Blocked limit").fill("25");
    await dialog.getByLabel("Upload bytes", { exact: true }).fill("2097152");
    await dialog.getByRole("button", { name: "Save numbers" }).click();
    await expect
      .poll(() => eika.mock.world.settings.settings.sink_limits)
      .toMatchObject({ upload_bytes: 2097152, fan_out_urls: 16 });
    expect(eika.mock.world.settings.settings.tripwires).toMatchObject({ blocked_limit: 25 });
    await dialog.getByRole("heading", { name: "Tripwires" }).scrollIntoViewIfNeeded();
    await expectShot(eika, `security-tripwires-${label}`, "role=dialog");
  });
}
