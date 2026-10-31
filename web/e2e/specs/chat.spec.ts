/**
 * Chat mode: sessions with no computer, listed in the sidebar, marked as
 * chats in their header, and offering only the tools that need no
 * computer, which the Tools panel turns on and off. A chat is compared pixel
 * for pixel once; the rest is checked by what the page says and offers.
 */

import { expect, test } from "../fixtures.ts";

test("a chat", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "chat" });
  await expect(eika.page.getByRole("navigation", { name: "Chats" })).toBeVisible();
  await expectShot(eika, "chat-light");
});

test("a new chat says what it cannot do", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "chat-empty" });
  await expectAria(eika, "chat-empty", 'role=region[name="Session"]');
});

test("a chat has a Tools panel", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "chat" });
  await eika.click("Tools");
  const fetch = eika.page.getByRole("switch", { name: "web_fetch" });
  await expect(fetch).toBeChecked();
  await eika.click("web_fetch");
  await expect(fetch).not.toBeChecked();
  await expectAria(eika, "chat-tools", 'role=tabpanel[name="Tools"]');

  // The choice is the harness's, so it holds across a reload.
  await eika.page.reload();
  await eika.settle();
  await eika.click("Tools");
  await expect(eika.page.getByRole("switch", { name: "web_fetch" })).not.toBeChecked();

  // Reset gives the choice back to the chat's profile, which offers every tool.
  await eika.click("role=button[name=/^Reset the chat.s tools/]");
  await expect(eika.page.getByRole("switch", { name: "web_fetch" })).toBeChecked();
});

test("a session in a computer is no chat and has no Tools panel", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  await expect(header.getByText("Chat")).toBeHidden();
  const tabs = eika.page.getByRole("tablist", { name: "Context panels" });
  await expect(tabs.getByRole("tab", { name: "Tools" })).toBeHidden();
});

test("new chat opens a chat from the sidebar", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("New chat");
  await expect(eika.page).toHaveURL(/\/sessions\/chat-/);
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  await expect(chats.locator('[aria-current="page"]')).toHaveText("New chat");
  await expectAria(eika, "chat-sidebar", 'role=navigation[name="Chats"]');
});

test("a new chat is named after its first message once a fast model is chosen", async ({
  open,
}) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("General");
  const titles = eika.page.getByRole("combobox", { name: "Fast" });
  await expect(titles).toHaveText("Not set");
  await expect(
    eika.page.getByText("Session titles are off until a fast or main model"),
  ).toBeVisible();
  await eika.select("role=combobox[name='Fast']", "gpt-5-mini");
  await expect(titles).toHaveText("gpt-5-mini");
  await expect(eika.page.getByText("Session titles are made by gpt-5-mini.")).toBeVisible();
  await eika.page.keyboard.press("Escape");

  await eika.click("New chat");
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  await expect(chats.locator('[aria-current="page"]')).toHaveText("New chat");
  await eika.send("Why does the retry loop never back off?");
  await expect(chats.locator('[aria-current="page"]')).toHaveText("Why does the retry loop never");
});
