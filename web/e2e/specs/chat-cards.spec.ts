/** The home transcript's cards and notices, on desktop and phone. */
import type { Locator } from "@playwright/test";

import { expect, test } from "../fixtures.ts";

for (const [label, viewport] of [
  ["desktop", { width: 1280, height: 900 }],
  ["phone", { width: 390, height: 844 }],
] as const) {
  test(`${label}: home cards and notices`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "chat-cards", viewport });
    const view = eika.page.getByRole("region", { name: "Session" });
    // The transcript pages backward, so load the first cards too.
    const session = view;
    while ((await session.getByRole("button", { name: /^subagent_start/ }).count()) === 0) {
      const older = session.getByRole("button", { name: /load older/i });
      if ((await older.count()) === 0) break;
      await older.click();
    }
    const position = async (row: Locator) => {
      await eika.settle();
      await eika.page.mouse.move(0, 0);
      await eika.page.evaluate(() => {
        if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
      });
      await row.evaluate((element) => {
        const scroller = element.closest(".overflow-y-auto");
        if (!(scroller instanceof HTMLElement)) throw new Error("Transcript scroller not found");
        const top = element.closest("[data-transcript-row]") ?? element;
        scroller.scrollTop +=
          top.getBoundingClientRect().top - scroller.getBoundingClientRect().top - 8;
      });
      await view
        .getByRole("region", { name: /^(Compaction summary|Notice text)$/ })
        .evaluateAll((regions) => {
          for (const region of regions) region.scrollTop = 0;
        });
      await eika.settle();
    };
    const compaction = view.getByRole("button", { name: /Compacted about/ });
    await compaction.focus();
    await eika.page.keyboard.press("Enter");
    await expect(view.getByRole("heading", { name: "Subagents", exact: true })).toBeVisible();
    await expect(view.getByText(/entries="#1-#20"/)).toBeVisible();
    await expect(view.getByText(/Which council/)).toBeVisible();
    await expect(view.getByText(/outcome is unknown/).last()).toBeVisible();
    await position(compaction);
    await expectShot(eika, `chat-notices-${label}`);
    for (const name of [
      "subagent_start",
      "subagent_message",
      "subagent_status",
      "subagent_cancel",
      "report",
      "project_list",
      "project_open",
      "project_create",
      "project_update",
      "history_search",
      "history_read",
      "bash",
      "file_read",
      "file_write",
      "file_edit",
      "web_search",
      "web_fetch",
      "ask_user",
      "mcp__example__read",
    ]) {
      const card = view.getByRole("button", { name: new RegExp(`^${name}\\b`) }).first();
      await card.click();
      await expect(card).toHaveAttribute("aria-expanded", "true");
    }
    const body = (name: string) =>
      view
        .getByRole("button", { name: new RegExp(`^${name}\\b`) })
        .first()
        .locator("..");
    await expect(body("project_open").getByRole("group", { name: "result" })).toContainText(
      "Use <metric> measurements.",
    );
    await expect(body("history_search").getByRole("group", { name: "arguments" })).toContainText(
      '"query": "plumber"',
    );
    await expect(body("history_search").getByRole("group", { name: "result" })).toContainText(
      "Found a <plumber>.",
    );
    await expect(body("history_read").getByRole("group", { name: "result" })).toContainText(
      "The full earlier entry.",
    );
    await expect(body("report").getByRole("group", { name: "arguments" })).toContainText(
      "Choose a firm",
    );
    await expect(view.getByRole("button", { name: /^file_read/ })).toContainText("quotes.md");
    const malformed = view.getByRole("button", { name: /^project_update/ }).nth(1);
    await malformed.click();
    await expect(malformed.locator("..").getByRole("group", { name: "arguments" })).toHaveText(
      '{"project": <broken>',
    );

    const start = view.getByRole("button", { name: /^subagent_start/ });
    await position(start);
    await expect(view.getByRole("link", { name: /plumber · researcher · running/ })).toBeVisible();
    await expectShot(eika, `chat-cards-${label}`);
    if (label === "phone") {
      for (const region of [
        view.getByRole("region", { name: "Compaction summary" }),
        view.getByRole("region", { name: "Notice text" }).first(),
      ]) {
        await expect(region).toHaveAttribute("tabindex", "0");
        expect(await region.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
        await region.evaluate((el) => {
          el.scrollTop = 0;
        });
        await region.focus();
        await expect(region).toBeFocused();
        await eika.page.keyboard.press("End");
        await expect.poll(() => region.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
        await eika.page.keyboard.press("Home");
        await expect.poll(() => region.evaluate((el) => el.scrollTop)).toBe(0);
      }
    }

    expect(
      await eika.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth),
    ).toBe(0);
    await view.getByRole("link", { name: /plumber · researcher · running/ }).click();
    await expect(eika.page).toHaveURL(/\/activity\/subagent-plumber$/);
  });
}
