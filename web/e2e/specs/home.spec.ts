/**
 * The home computer: the one computer the harness makes for itself, and the
 * only place a chat can have files and commands until phase 2 replaces chats.
 * The sidebar offers a chat on it beside the plain "New chat", lists the
 * sessions that run there with the chats, and marks them.
 */

import { defaultSandbox } from "../harness/world.ts";
import { expect, test } from "../fixtures.ts";

test("the sidebar lists a session on the home computer with the chats", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "home" });
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  await expect(
    chats.getByRole("button", { name: "What is in my home directory?", exact: true }),
  ).toBeVisible();
  await expect(
    chats.getByRole("button", { name: "What is full jitter?", exact: true }),
  ).toBeVisible();
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  await expect(header.getByText("Home computer")).toBeVisible();
  await expectShot(eika, "home-light");
});

test("a chat on the home computer is opened from the sidebar", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "home" });
  await eika.click("New home chat");
  await expect(eika.page).toHaveURL(/\/sessions\/ses-/);
  const session = eika.page.getByRole("region", { name: "Session" });
  await expect(session.locator("header").getByText("Home computer")).toBeVisible();
  await expectAria(eika, "home-empty", 'role=region[name="Session"]');

  // It runs like any session, and the sidebar keeps it among the chats.
  await eika.send("What is in my home directory?");
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  await expect(chats.locator('[aria-current="page"]')).toBeVisible();
  expect(
    eika.mock.world.sessions.find((s) => s.computer_id === "cx-home" && s.id !== "ses-home"),
  ).toBeDefined();
});

test("a plain chat is no home chat", async ({ open }) => {
  const eika = await open({ scenario: "home" });
  await eika.click("New chat");
  await expect(eika.page).toHaveURL(/\/sessions\/chat-/);
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  await expect(header.getByText("no computer")).toBeVisible();
  await expect(header.getByText("Home computer")).toBeHidden();
});

test("there is no home chat to start until the harness has made the computer", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  await expect(eika.page.getByRole("button", { name: "New chat" })).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "New home chat" })).toBeHidden();

  // The harness records the computer and announces it on the stream.
  eika.mock.world.computers.push({
    id: "cx-home",
    name: "home",
    image: "eika-sandbox:latest",
    state: "creating",
    sandbox: defaultSandbox(),
    home: true,
    created_at: "2026-03-14T14:59:00.000Z",
    updated_at: "2026-03-14T14:59:00.000Z",
  });
  eika.mock.emit(
    eika.mock.event("computer.state", "global", { computer_id: "cx-home", state: "creating" }),
  );
  await expect(eika.page.getByRole("button", { name: "New home chat" })).toBeVisible();
});
