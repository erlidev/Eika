/**
 * Chat mode: sessions with no workspace, listed apart from the projects,
 * marked as chats in their header, and offering only the tools that need no
 * workspace, which the Tools panel turns on and off.
 */

import { expect, test } from "../fixtures.ts";

for (const theme of ["light", "dark"] as const) {
  test(`a chat, ${theme} theme`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "chat", theme });
    await expect(eika.page.getByRole("navigation", { name: "Chats" })).toBeVisible();
    await expectShot(eika, `chat-${theme}`);
  });
}

test("a new chat says what it cannot do", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "chat-empty" });
  await expectShot(eika, "chat-empty");
});

test("a chat has a Tools panel where a workspace session has files", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "chat" });
  const tabs = eika.page.getByRole("tablist", { name: "Context panels" });
  await expect(tabs.getByRole("tab", { name: "Files" })).toBeHidden();
  await expect(tabs.getByRole("tab", { name: "Terminal" })).toBeHidden();

  await eika.click("Tools");
  const fetch = eika.page.getByRole("switch", { name: "web_fetch" });
  await expect(fetch).toBeChecked();
  await eika.click("web_fetch");
  await expect(fetch).not.toBeChecked();
  await expectShot(eika, "chat-tools", 'role=tabpanel[name="Tools"]');

  // The choice is the harness's, so it holds across a reload.
  await eika.page.reload();
  await eika.settle();
  await eika.click("Tools");
  await expect(eika.page.getByRole("switch", { name: "web_fetch" })).not.toBeChecked();
});

test("a workspace session names its workspace and has no Tools panel", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  await expect(header.getByText("fix-retries", { exact: true })).toBeVisible();
  await expect(header.getByText("Chat")).toBeHidden();
  const tabs = eika.page.getByRole("tablist", { name: "Context panels" });
  await expect(tabs.getByRole("tab", { name: "Tools" })).toBeHidden();
});

test("new chat opens a chat from the sidebar", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("New chat");
  await expect(eika.page).toHaveURL(/\/sessions\/chat-/);
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  await expect(chats.locator('[aria-current="page"]')).toHaveText("New chat");
  await expectShot(eika, "chat-sidebar", 'role=navigation[name="Chats"]');
});
