/**
 * The Chat page: Eika's one conversation, the home session. It opens at the
 * app's root, has no list of chats and no way to start another, and a message
 * sent on it is the home session's, recorded with the web channel. The tests
 * check what the page says and offers.
 */

import { expect, test } from "../fixtures.ts";

test("Chat opens on the home conversation", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await expect(eika.page).toHaveURL(/\/$/);
  const session = eika.page.getByRole("region", { name: "Session" });
  // One header: the shell's. The session view draws none of its own.
  await expect(eika.page.getByRole("heading", { level: 1, name: "Chat" })).toBeVisible();
  await expect(eika.page.getByRole("heading", { level: 2 })).toHaveCount(0);
  await expect(eika.page.getByRole("banner")).toHaveCount(1);
  await expect(session.getByText("The webhook client retries immediately")).toBeVisible();
  await expect(eika.page.getByRole("main").getByLabel("Message", { exact: true })).toBeVisible();
});

test("an empty conversation says what to do", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "home-empty" });
  await expectAria(eika, "home-empty", 'role=region[name="Session"]');
});

test("there is one conversation: no chat list and no way to start another", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await expect(eika.page.getByRole("navigation", { name: "Chats" })).toHaveCount(0);
  await expect(eika.page.getByRole("button", { name: /^New (home )?chat$/ })).toHaveCount(0);
  await eika.press("Control+K");
  await expect(eika.page.getByRole("dialog")).toHaveCount(0);
});

test("a message sent on Chat is the home session's, from the web", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  await eika.send("What is decorrelated jitter?");
  await expect(
    eika.page.getByRole("paragraph").filter({ hasText: "Decorrelated jitter grows" }),
  ).toBeVisible();
  const home = eika.mock.world.sessions.find((s) => s.kind === "home");
  const entries = eika.mock.world.entries[home?.id ?? ""] ?? [];
  const sent = entries.filter((e) => e.kind === "user").at(-1);
  expect(sent).toMatchObject({ channel: "web" });
  expect(entries.filter((e) => e.kind === "user")).toHaveLength(2);
});

test("a reload keeps the conversation", async ({ open }) => {
  const eika = await open({ scenario: "agent-tools" });
  await eika.send("Run the tests");
  await expect(eika.page.getByText("Tests pass.").first()).toBeVisible();
  await eika.page.reload();
  await eika.settle();
  const session = eika.page.getByRole("region", { name: "Session" });
  await expect(session.getByText("Run the tests", { exact: true })).toBeVisible();
  await expect(session.getByText(/Tests pass\./).first()).toBeVisible();
});

test("a link to a session of the old workbench lands on Chat", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.goto("/sessions/ses-backoff");
  await expect(eika.page).toHaveURL(/\/$/);
  await expect(eika.page.getByRole("region", { name: "Session" })).toBeVisible();
});

test("a home session that does not load says so and can be tried again", async ({ open }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "GET /api/home": { status: 500 } },
  });
  const alert = eika.page.getByRole("alert");
  await expect(alert).toContainText("Could not load the conversation");
  eika.mock.healRoute("GET /api/home");
  await eika.click("Retry");
  await expect(eika.page.getByRole("region", { name: "Session" })).toBeVisible();
  await expect(alert).toHaveCount(0);
});
