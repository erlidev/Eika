/**
 * The app shell around every page: a rail from 640px and a bottom bar under
 * it, the header, the room it leaves for the device's keyboard, and the one
 * dialog it opens. The tests check what each layout says and offers; the
 * screenshots pin how the two layouts look.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

const phone = { width: 390, height: 844 };

// The screenshots show a short conversation, which fits the screen: the
// transcript's scroll position after a long one is not what they are for.

/**
 * coverKeyboard makes the visual viewport `height` tall, as the page sees it
 * while an on-screen keyboard that does not resize the layout viewport is up
 * (Safari), and tells the page it changed.
 */
async function coverKeyboard(eika: EikaDriver, height: number): Promise<void> {
  await eika.page.evaluate((h) => {
    const viewport = window.visualViewport;
    if (viewport === null) throw new Error("no visual viewport");
    Object.defineProperty(viewport, "height", { configurable: true, get: () => h });
    viewport.dispatchEvent(new Event("resize"));
  }, height);
  await eika.settle();
}

test("desktop: a rail with the pages, the halt slot and Settings", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  const pages = eika.page.getByRole("navigation", { name: "Pages" });
  await expect(pages.getByRole("link", { name: "Chat" })).toHaveAttribute("aria-current", "page");
  await expect(eika.page.getByRole("button", { name: "Settings" })).toBeVisible();
  await expect(eika.page.getByRole("heading", { level: 1, name: "Chat" })).toBeVisible();
  // The slot is reserved for phase 4's halt button; it holds nothing yet.
  await expect(eika.page.locator('[data-slot="halt"]')).toHaveCount(1);
  await expectShot(eika, "chat-desktop-light");
});

test("desktop, dark", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench", theme: "dark" });
  await expect(eika.page.getByRole("navigation", { name: "Pages" })).toBeVisible();
  await expectShot(eika, "chat-desktop-dark");
});

test("phone: a bottom bar with the pages and Settings, and no rail", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "home-world", viewport: phone });
  const bar = eika.page.getByRole("navigation", { name: "Pages" });
  await expect(bar.getByRole("link", { name: "Chat" })).toHaveAttribute("aria-current", "page");
  await expect(bar.getByRole("button", { name: "Settings" })).toBeVisible();
  await expect(eika.page.getByRole("navigation", { name: "Pages" })).toHaveCount(1);
  // The bar is the page's bottom edge, under the session's status bar.
  const box = await bar.boundingBox();
  expect(box?.y).toBeGreaterThan(phone.height - 80);
  expect((box?.y ?? 0) + (box?.height ?? 0)).toBe(phone.height);
  // The phone's header carries the halt slot, and the page never scrolls sideways.
  await expect(eika.page.locator('[data-slot="halt"]')).toHaveCount(1);
  const overflow = await eika.page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBe(0);
  await expectShot(eika, "chat-phone-light");
});

test("phone, dark", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "home-world", theme: "dark", viewport: phone });
  await expect(eika.page.getByRole("navigation", { name: "Pages" })).toBeVisible();
  await expectShot(eika, "chat-phone-dark");
});

test("the page's address is Chat, and a path that is no page lands on it", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.goto("/nowhere/at/all");
  await expect(eika.page).toHaveURL(/\/$/);
  await eika.page.reload();
  await eika.settle();
  await expect(eika.page.getByRole("region", { name: "Session" })).toBeVisible();
  await expect(
    eika.page.getByRole("navigation", { name: "Pages" }).getByRole("link", { name: "Chat" }),
  ).toHaveAttribute("aria-current", "page");
});

test("the shell fills the viewport and the session scrolls inside it", async ({ open }) => {
  const eika = await open({ scenario: "workbench", viewport: phone });
  const scroll = await eika.page.evaluate(() => ({
    page: document.documentElement.scrollHeight,
    window: window.innerHeight,
  }));
  expect(scroll.page).toBe(scroll.window);
});

test("the keyboard never covers the composer, and the bar gives way to it", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "home-world", viewport: phone });
  const composer = eika.page.getByLabel("Message", { exact: true });
  const bar = eika.page.getByRole("navigation", { name: "Pages" });
  await expect(bar).toBeVisible();

  // A keyboard of 344px: the visual viewport is 500px tall.
  await coverKeyboard(eika, 500);
  await expect(bar).toHaveCount(0);
  const inset = await eika.page.evaluate(() =>
    document.documentElement.style.getPropertyValue("--keyboard-inset"),
  );
  expect(inset).toBe("344px");
  const box = await composer.boundingBox();
  expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(500);
  await expectShot(eika, "chat-phone-keyboard");

  // The keyboard goes away and the bar comes back.
  await coverKeyboard(eika, phone.height);
  await expect(bar).toBeVisible();
});
