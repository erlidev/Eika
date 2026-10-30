/**
 * Keeping the chat list in order: renaming a chat in place, pinning it to
 * the top of the list, and archiving it under a folded heading. Every row
 * offers the same actions from its menu button and from a right-click, and
 * the session header renames the chat that is open.
 */

import { expect, test } from "../fixtures.ts";

test("a chat is renamed in place from its menu, and F2 does the same", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  const chats = eika.page.getByRole("navigation", { name: "Chats" });

  await eika.click("Actions for What is full jitter?");
  await eika.click("Rename");
  const field = chats.getByLabel("Rename What is full jitter?");
  await expect(field).toBeFocused();
  await field.fill("Jitter explained");
  await field.press("Enter");

  // The row and the open chat's header both carry the new title, and the
  // focus is back on the row it started from.
  const row = chats.getByRole("button", { name: "Jitter explained", exact: true });
  await expect(row).toBeFocused();
  await expect(eika.page.getByRole("heading", { name: "Jitter explained" })).toBeVisible();

  // F2 on the focused row renames it too; Escape keeps the name it had.
  await row.press("F2");
  await chats.getByLabel("Rename Jitter explained").fill("Something else");
  await eika.press("Escape");
  await expect(row).toBeFocused();
  await expect(chats.getByText("Something else")).toBeHidden();
});

test("the session header renames the chat that is open", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  await eika.click("role=heading[name='What is full jitter?'] >> role=button");
  await eika.fill("Session title", "Jitter, briefly");
  await eika.press("Enter");
  await expect(eika.page.getByRole("heading", { name: "Jitter, briefly" })).toBeVisible();
  await expect(
    eika.page
      .getByRole("navigation", { name: "Chats" })
      .getByRole("button", { name: "Jitter, briefly", exact: true }),
  ).toBeVisible();
});

test("pinning puts a chat first and archiving folds it away", async ({
  open,
  expectAria,
  expectShot,
}) => {
  const eika = await open({ scenario: "chat" });
  const chats = eika.page.getByRole("navigation", { name: "Chats" });

  await chats
    .getByRole("button", { name: "Postgres advisory locks", exact: true })
    .click({ button: "right" });
  await expectShot(eika, "row-menu", 'role=menu[name="Actions for Postgres advisory locks"]');
  await eika.click("Pin");
  await expect(chats.getByRole("list").first().getByRole("button").first()).toHaveAccessibleName(
    "Postgres advisory locks (pinned)",
  );

  await eika.click("Actions for What is full jitter?");
  await eika.click("Archive");
  await expect(chats.getByRole("button", { name: /^What is full jitter/ })).toBeHidden();
  await eika.click("Archived chats (1)");
  await expect(chats.getByRole("button", { name: /^What is full jitter/ })).toBeVisible();
  await expectAria(eika, "organized-chats", 'role=navigation[name="Chats"]');

  // Unarchiving puts it back among the others.
  await eika.click("Actions for What is full jitter?");
  await eika.click("Unarchive");
  await expect(chats.getByRole("button", { name: /Archived chats/ })).toBeHidden();
});

test("an archived chat says so in its header, with the way back", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  const title = (await chats.locator('[aria-current="page"]').textContent()) ?? "";

  await eika.click(`Actions for ${title}`);
  await eika.click("Archive");
  await expect(header.getByText("Archived")).toBeVisible();
  await expect(chats.locator('[aria-current="page"]')).toBeHidden();

  await header.getByRole("button", { name: "Unarchive" }).click();
  await expect(header.getByText("Archived")).toBeHidden();
  await expect(chats.locator('[aria-current="page"]')).toBeVisible();
});

test("a change the harness refuses says why beside the row", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  eika.mock.failRoute("PATCH /api/sessions/{id}", { status: 500 });
  await eika.click("Actions for Postgres advisory locks");
  await eika.click("Pin");
  await expect(eika.page.getByRole("navigation", { name: "Chats" }).getByRole("alert")).toHaveText(
    /^Could not change the chat: /,
  );
});
