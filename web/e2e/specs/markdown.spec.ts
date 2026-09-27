/**
 * Markdown in a message the user writes: marked up in the composer as it is
 * typed, while the box still holds the source; edited as an editor would in
 * the markdown mode; rendered once it is sent; and handed back as source by a
 * rewind. The composer and a sent message are compared pixel for pixel once.
 */

import type { Locator } from "@playwright/test";

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

const source = [
  "## Plan",
  "Make **backoff** configurable,",
  "see [the docs](https://example.com/retries):",
  "",
  "- read `RETRY_CAP`",
  "- keep the jitter",
].join("\n");

/**
 * moved lists the runs of the composer's markup that do not sit exactly
 * where the same text sits unstyled. The textarea's caret and selection are
 * drawn from the unstyled text, so any run listed here is one the caret
 * would drift across.
 */
function moved(box: Locator): Promise<string[]> {
  return box.evaluate((el) => {
    const mirror = el.previousElementSibling as HTMLElement;
    const plain = mirror.cloneNode(true) as HTMLElement;
    for (const span of plain.querySelectorAll("span")) span.removeAttribute("class");
    mirror.after(plain);
    plain.scrollTop = mirror.scrollTop;
    const styled = [...mirror.querySelectorAll("span")];
    const unstyled = [...plain.querySelectorAll("span")];
    const out: string[] = [];
    styled.forEach((span, i) => {
      const a = [...span.getClientRects()];
      const b = [...(unstyled[i]?.getClientRects() ?? [])];
      const same =
        a.length === b.length &&
        a.every((r, j) => {
          const q = b[j];
          return (
            q !== undefined &&
            Math.abs(r.left - q.left) < 0.5 &&
            Math.abs(r.right - q.right) < 0.5 &&
            r.top === q.top
          );
        });
      if (!same) out.push(span.textContent);
    });
    plain.remove();
    return out;
  });
}

/** useMarkdownMode turns on editing messages as markdown, in Settings. */
async function useMarkdownMode(eika: EikaDriver) {
  await eika.click("Settings");
  await eika.click("Appearance");
  await eika.click("Edit messages as markdown");
  await eika.press("Escape");
}

test("the composer marks up markdown as it is typed", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "chat" });
  const box = eika.page.getByLabel("Message", { exact: true });
  await box.fill(
    [
      source,
      "a long line that wraps, with **bold** and *slanted* text in it ".repeat(3),
      "```go",
      "// backoff doubles each try, up to a cap",
      'func backoff(n int) time.Duration { return min(time.Second<<n, cap) } // "30s"',
      "```",
    ].join("\n"),
  );

  // The markup is drawn under the text; the box itself is still the source,
  // and the two lay out the same text into the same lines.
  const layers = await box.evaluate((el) => {
    const mirror = el.previousElementSibling;
    return {
      mirror: mirror?.textContent,
      value: (el as HTMLTextAreaElement).value,
      heights: [mirror?.scrollHeight, el.scrollHeight],
    };
  });
  expect(layers.mirror).toBe(`${layers.value} `);
  expect(layers.heights[0]).toBe(layers.heights[1]);
  expect(await moved(box)).toEqual([]);
  await expectShot(eika, "composer-markdown", 'role=textbox[name="Message"]');
});

test("a sent message is rendered, and a rewind hands back its source", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "chat" });
  const box = eika.page.getByLabel("Message", { exact: true });
  const sent = `${source}\n\n| attempt | delay |\n|---|---|\n| 1 | 1s |\n| 2 | 2s |\n| 3 | 4s |`;
  await box.fill(sent);
  await eika.press("Enter");

  const message = eika.page.getByRole("article").filter({ hasText: "configurable" });
  await expect(message.getByRole("heading", { name: "Plan" })).toBeVisible();
  await expect(message.getByRole("link", { name: "the docs" })).toHaveAttribute(
    "href",
    "https://example.com/retries",
  );
  await expect(message.getByRole("listitem")).toHaveText(["read RETRY_CAP", "keep the jitter"]);
  await expect(message.locator("strong")).toHaveText("backoff");
  // A newline typed in the box is a new line, not a space.
  await expect(message.locator("br")).toHaveCount(1);
  // A table's header and stripes stand apart from the bubble's tint.
  await expectShot(eika, "message-markdown", 'css=article:has-text("configurable")');

  // The reply ends the run; only an idle session offers a rewind.
  await expect(
    eika.page.getByRole("paragraph").filter({ hasText: "Decorrelated jitter grows" }),
  ).toBeVisible();
  await message.hover();
  await eika.click(
    "role=button[name='Rewind the conversation to this message and edit it'] >> nth=-1",
  );
  await expect(box).toHaveValue(sent);
});

test("the markdown mode edits as an editor does and sends on Ctrl+Enter", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  await useMarkdownMode(eika);
  const box = eika.page.getByLabel("Message", { exact: true });
  await expect(eika.page.getByText("Ctrl+Enter sends")).toBeVisible();
  await box.click();
  const keys = eika.page.keyboard;

  // A list goes on, nests under Tab, and ends on an empty item.
  await keys.type("- one");
  await keys.press("Enter");
  await expect(box).toHaveValue("- one\n- ");
  await keys.type("two");
  await keys.press("Tab");
  await expect(box).toHaveValue("- one\n  - two");
  await expect(box).toBeFocused();
  await keys.press("Enter");
  await keys.press("Shift+Tab");
  await keys.type("three");
  await keys.press("Enter");
  await keys.press("Enter");
  await expect(box).toHaveValue("- one\n  - two\n- three\n");

  // A continuation is one step to undo, as a typed newline would be.
  await keys.type("1. a");
  await keys.press("Enter");
  await expect(box).toHaveValue("- one\n  - two\n- three\n1. a\n2. ");
  await keys.press("ControlOrMeta+z");
  await expect(box).toHaveValue("- one\n  - two\n- three\n1. a");
  await keys.press("Enter");
  await keys.press("Enter");

  // A fence closes itself, and the caret stays for the language.
  await keys.type("```go");
  await expect(box).toHaveValue("- one\n  - two\n- three\n1. a\n```go\n```");
  await keys.press("Enter");
  await keys.type("if x {");
  await keys.press("Enter");
  // Tab in code indents, by a block's default step until it has its own.
  await keys.press("Tab");
  await keys.type("return");
  await keys.press("Enter");
  await expect(box).toHaveValue("- one\n  - two\n- three\n1. a\n```go\nif x {\n  return\n  \n```");
  await keys.press("Tab");
  await keys.type("y");
  await keys.press("Shift+Tab");
  await expect(box).toHaveValue("- one\n  - two\n- three\n1. a\n```go\nif x {\n  return\n  y\n```");
  await expect(box).toBeFocused();

  // Enter never sent any of that; Ctrl+Enter does.
  const messages = eika.page.getByRole("article").filter({ has: eika.page.getByText("You") });
  const before = await messages.count();
  await keys.press("ControlOrMeta+Enter");
  await expect(messages).toHaveCount(before + 1);
  await expect(box).toHaveValue("");
});
