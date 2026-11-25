/**
 * Halt: the button every page has, the banner while halted with Resume, live
 * updates from `halt.state`, and a refused message. Screenshots pin the button
 * and the halted banner on a desktop and a phone, in both themes.
 */

import type { Locator } from "@playwright/test";

import { expect, test } from "../fixtures.ts";

const desktop = { width: 1280, height: 800 };
const phone = { width: 390, height: 844 };

for (const [label, viewport] of [
  ["desktop", desktop],
  ["phone", phone],
] as const) {
  for (const theme of ["light", "dark"] as const) {
    test(`${label}, ${theme}: the halt button and the halted banner`, async ({
      open,
      expectShot,
    }) => {
      const eika = await open({ scenario: "halted", viewport, theme });
      const banner = eika.page.getByRole("status").filter({ hasText: "Eika is halted" });
      await expect(banner).toContainText("A canary value was seen in a command.");
      await expect(banner.getByRole("button", { name: "Resume" })).toBeVisible();
      await expect(eika.page.getByRole("button", { name: "Halt Eika" })).toBeVisible();
      const overflow = await eika.page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect(overflow).toBeLessThanOrEqual(0);
      await expectShot(eika, `halted-${label}-${theme}`);
    });
  }

  test(`${label}: one press halts, Resume lifts it, on any page`, async ({ open }) => {
    const eika = await open({ scenario: "workbench", viewport });
    const banner = eika.page.getByRole("status").filter({ hasText: "Eika is halted" });
    await expect(banner).toHaveCount(0);
    await eika.click("Halt Eika");
    await expect(banner).toBeVisible();
    expect(eika.mock.world.halt.halted).toBe(true);
    // The banner follows to every page.
    await eika.goto("/activity");
    await expect(banner).toBeVisible();
    await eika.click("Resume");
    await expect(banner).toHaveCount(0);
    expect(eika.mock.world.halt.halted).toBe(false);
  });
}

test("a halt from elsewhere shows live and lifts live", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  const banner = eika.page.getByRole("status").filter({ hasText: "Eika is halted" });
  eika.mock.world.halt = { halted: true, at: "2026-03-14T15:00:00.000Z", reason: "From a script." };
  eika.mock.emit(eika.mock.event("halt.state", "global", { ...eika.mock.world.halt }));
  await expect(banner).toContainText("From a script.");
  eika.mock.world.halt = { halted: false };
  eika.mock.emit(eika.mock.event("halt.state", "global", { halted: false }));
  await expect(banner).toHaveCount(0);
});

test("a message while halted is refused and its text is kept", async ({ open }) => {
  const eika = await open({ scenario: "halted" });
  await eika.send("Run the tests");
  const alert = eika.page
    .getByRole("alert")
    .filter({ hasText: "Eika is halted, so nothing was sent" });
  await expect(alert).toBeVisible();
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveValue("Run the tests");
});

test.describe("touch", () => {
  test.use({ hasTouch: true, isMobile: true });

  test("the button and Resume are at least 44px", async ({ open }) => {
    const eika = await open({ scenario: "halted", viewport: phone });
    const large = async (target: Locator) => {
      await expect(target).toBeVisible();
      const box = await target.boundingBox();
      expect(box?.width).toBeGreaterThanOrEqual(44);
      expect(box?.height).toBeGreaterThanOrEqual(44);
    };
    await large(eika.page.getByRole("button", { name: "Halt Eika" }));
    await large(eika.page.getByRole("button", { name: "Resume" }));
  });
});
