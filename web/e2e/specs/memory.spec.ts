/**
 * The Memory page: the list with its filters and search, and a memory read in
 * full with its source links. The shots pin the desktop split and the phone's
 * two screens, on a coarse pointer.
 */

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };

test("the list shows type, pinned and state, and filters by state and type", async ({ open }) => {
  const eika = await open({ scenario: "memory" });
  const list = eika.page.getByRole("navigation", { name: "Memories" });
  await expect(list.getByRole("link")).toHaveCount(5);
  await expect(list.getByRole("link", { name: /web-note/ })).toContainText("Pending");
  await expect(list.getByRole("link", { name: /preferred-name/ })).toContainText("Pinned");
  await expect(list.getByRole("link", { name: /deploy-steps/ })).toContainText("Archived");
  await list.getByRole("button", { name: "Pending" }).click();
  await expect(list.getByRole("link")).toHaveCount(1);
  await list.getByRole("button", { name: "Any state" }).click();
  await list.getByRole("button", { name: "Person" }).click();
  await expect(list.getByRole("link")).toHaveText([/plumber-contact/]);
  await list.getByRole("button", { name: "All types" }).click();
  await list.getByRole("button", { name: "Pinned" }).click();
  await expect(list.getByRole("link")).toHaveText([/preferred-name/]);
});

test("a search narrows by text and by state, and an empty result says so", async ({ open }) => {
  const eika = await open({ scenario: "memory" });
  const list = eika.page.getByRole("navigation", { name: "Memories" });
  await list.getByLabel("Search memories").fill("oolong");
  await expect(list.getByRole("link")).toHaveText([/tea-order/]);
  await list.getByLabel("Search memories").fill("shed");
  await list.getByRole("button", { name: "Active" }).click();
  await expect(eika.page.getByText("No memory matches.")).toBeVisible();
  await eika.page.getByRole("button", { name: "Clear filters" }).click();
  await expect(list.getByRole("link")).toHaveCount(5);
});

test("a memory opens with its Markdown body, and its source links to Activity", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "memory" });
  await eika.page.getByRole("link", { name: /plumber-contact/ }).click();
  await expect(eika.page).toHaveURL(/\/memory\/plumber-contact$/);
  await expect(eika.page.getByRole("heading", { level: 2, name: "plumber-contact" })).toBeVisible();
  await expectShot(eika, "memory-desktop");
  await eika.page.getByRole("link", { name: "session:subagent-plumber" }).click();
  await expect(eika.page).toHaveURL(/\/activity\/subagent-plumber$/);
});

test("a pending memory says why, and an archived one says what archived it", async ({ open }) => {
  const eika = await open({ scenario: "memory" });
  await eika.page.getByRole("link", { name: /web-note/ }).click();
  await expect(eika.page.getByText("waiting for review")).toBeVisible();
  await expect(eika.page.getByText("ignore previous instructions", { exact: true })).toBeVisible();
  await eika.page.getByRole("link", { name: /deploy-steps/ }).click();
  await expect(eika.page.getByText("Replaced by the new shed checklist")).toBeVisible();
});

test("an unknown memory and a failing list are said, not blank", async ({ open }) => {
  const eika = await open({ scenario: "memory", path: "/memory/nothing-here" });
  await expect(eika.page.getByText("This memory no longer exists.")).toBeVisible();
  const failed = await open({
    scenario: "memory",
    failing: { "GET /api/memories": { status: 500 } },
  });
  await expect(failed.page.getByRole("alert")).toBeVisible();
});

test.describe("on a phone", () => {
  test.use({ hasTouch: true, isMobile: true });

  test("the list, then a memory with a way back", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "memory", viewport: phone });
    expect(await eika.page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
    await expectShot(eika, "memory-phone-list");
    await eika.page.getByRole("link", { name: /web-note/ }).tap();
    await expect(
      eika.page.getByRole("link", { name: "Memory", exact: true }).first(),
    ).toBeVisible();
    await expectShot(eika, "memory-phone-detail");
    await eika.page.getByRole("link", { name: "Memory", exact: true }).first().tap();
    await expect(eika.page).toHaveURL(/\/memory$/);
  });
});
