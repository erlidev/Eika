/** Touch density uses a coarse pointer, including on a phone. */
import type { Locator } from "@playwright/test";

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };
test.use({ hasTouch: true, isMobile: true });

async function targetSize(target: Locator) {
  await expect(target).toBeVisible();
  const size = await target.boundingBox();
  expect(size?.width).toBeGreaterThanOrEqual(44);
  expect(size?.height).toBeGreaterThanOrEqual(44);
}

test("Chat exposes rewind and uses touch targets", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench", viewport: phone });
  expect(await eika.page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
  expect(eika.page.viewportSize()).toEqual(phone);
  // The transcript can finish laying out after its initial scroll. Pin the
  // capture to the latest entry after fonts and mock requests have settled.
  await eika.settle();
  const scroller = eika.page.locator('[aria-label="Session"] .overflow-y-auto').first();
  await scroller.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  await expect
    .poll(() =>
      scroller.evaluate(
        (element) => element.scrollHeight - element.clientHeight - element.scrollTop,
      ),
    )
    .toBeLessThanOrEqual(1);
  const rewind = eika.page.getByRole("button", { name: /Rewind the conversation/ }).first();
  await targetSize(rewind);
  await expect(rewind).toHaveCSS("opacity", "1");
  await expectShot(eika, "chat-touch");
  await rewind.tap();
  await expect(eika.page.getByLabel("Message", { exact: true })).not.toHaveValue("");
});

test("Activity on touch", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "activity", viewport: phone });
  await expectShot(eika, "activity-touch");
});

test("Projects on touch", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "projects", viewport: phone });
  await expectShot(eika, "projects-touch");
  await eika.page.getByRole("link", { name: /Kitchen/ }).tap();
  await targetSize(eika.page.getByRole("link", { name: /← Projects/ }));
});

test("Settings on touch", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "providers", viewport: phone });
  await eika.click("Settings");
  await expectShot(eika, "settings-touch");
});

test("model checkbox targets do not cross row boundaries", async ({ open }) => {
  const eika = await open({ scenario: "providers", viewport: phone });
  await eika.click("Settings");
  await eika.click('role=button[name="Models"] >> nth=1');
  const first = eika.page.getByRole("checkbox", { name: "gpt-5", exact: true });
  const second = eika.page.getByRole("checkbox", { name: "gpt-5-mini", exact: true });
  const tapRow = async (checkbox: Locator, fraction: number, checked: boolean) => {
    const row = checkbox.locator("..");
    await row.scrollIntoViewIfNeeded();
    const geometry = await checkbox.evaluate((element) => {
      const row = element.parentElement;
      if (!row) throw new Error("model checkbox has no row");
      const box = row.getBoundingClientRect();
      const after = getComputedStyle(element, "::after");
      return {
        x: box.x,
        y: box.y,
        width: box.width,
        height: box.height,
        afterWidth: parseFloat(after.width),
        afterHeight: parseFloat(after.height),
      };
    });
    expect(geometry.height).toBeGreaterThanOrEqual(44);
    expect(geometry.afterWidth).toBeGreaterThanOrEqual(44);
    expect(geometry.afterHeight).toBeGreaterThanOrEqual(44);
    const x = geometry.x + geometry.width / 2;
    const y = geometry.y + 2 + (geometry.height - 4) * fraction;
    const hit = await eika.page.evaluate(
      ({ x, y }) => {
        const element = document.elementFromPoint(x, y);
        return element instanceof HTMLLabelElement ? element.htmlFor : null;
      },
      { x, y },
    );
    expect(hit).toBe(await checkbox.getAttribute("id"));
    await eika.page.touchscreen.tap(x, y);
    if (checked) await expect(checkbox).toBeChecked();
    else await expect(checkbox).not.toBeChecked();
    await eika.settle();
  };
  for (const fraction of [0, 0.5, 1]) {
    await tapRow(first, fraction, true);
    await expect(second).not.toBeChecked();
    await tapRow(first, fraction, false);
    await tapRow(second, fraction, true);
    await expect(first).not.toBeChecked();
    await tapRow(second, fraction, false);
  }
});

test("URL actions have touch targets", async ({ open }) => {
  const eika = await open({ scenario: "agent-mcp-url", viewport: phone });
  await eika.send("Connect Linear");
  await targetSize(eika.page.getByRole("link", { name: "Open linear.app" }));
});

test("developer disclosures have touch targets", async ({ open }) => {
  const eika = await open({ scenario: "recorded-calls", viewport: phone });
  await eika.click("Settings");
  await eika.click("Developer");
  await eika.select("Session", "Add exponential backoff to retries");
  await targetSize(eika.page.locator("summary").first());
});
