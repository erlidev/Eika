/** The composer uses native editing and a rendered preview on touch. */
import { expect, test } from "../fixtures.ts";

test.use({ hasTouch: true, isMobile: true });

test("touch composer previews and sends a multiline message", async ({ open }) => {
  const eika = await open({ scenario: "chat", viewport: { width: 390, height: 844 } });
  expect(await eika.page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
  await eika.click("Settings");
  await eika.click("Appearance");
  await eika.click("Edit messages as markdown");
  await eika.press("Escape");
  const box = eika.page.getByLabel("Message", { exact: true });
  await box.fill("## Plan");
  await box.press("Enter");
  await box.pressSequentially("Use **backoff**");
  await expect(box).toHaveValue("## Plan\nUse **backoff**");
  const preview = eika.page.getByLabel("Message preview");
  await expect(preview.getByRole("heading", { name: "Plan" })).toBeVisible();
  await expect(preview.locator("strong")).toHaveText("backoff");
  const send = eika.page.getByRole("button", { name: "Send", exact: true });
  const size = await send.boundingBox();
  expect(size?.width).toBeGreaterThanOrEqual(44);
  expect(size?.height).toBeGreaterThanOrEqual(44);
  await send.tap();
  await expect(box).toHaveValue("");
  const message = eika.page.getByRole("article").filter({ hasText: "Use backoff" });
  await expect(message).toBeVisible();
  await expect(
    eika.page.getByRole("paragraph").filter({ hasText: "Decorrelated jitter grows" }),
  ).toBeVisible();
  await message
    .locator("..")
    .getByRole("button", { name: /Rewind the conversation/ })
    .tap();
  await expect(box).toHaveValue("## Plan\nUse **backoff**");
});

test("a long draft keeps Send above a keyboard", async ({ open }) => {
  const eika = await open({ scenario: "chat", viewport: { width: 390, height: 844 } });
  const box = eika.page.getByLabel("Message", { exact: true });
  await box.fill("A long draft\n".repeat(80));
  await eika.page.evaluate(() => {
    const viewport = window.visualViewport;
    if (!viewport) throw new Error("No visual viewport");
    Object.defineProperty(viewport, "height", { configurable: true, get: () => 400 });
    viewport.dispatchEvent(new Event("resize"));
  });
  const send = eika.page.getByRole("button", { name: "Send", exact: true });
  const rect = await send.boundingBox();
  expect(rect).not.toBeNull();
  expect((rect?.y ?? 400) + (rect?.height ?? 0)).toBeLessThanOrEqual(400);
  await send.tap();
  await expect(box).toHaveValue("");
});

test("input choice keeps the draft and renders untrusted Markdown safely", async ({ open }) => {
  const eika = await open({ scenario: "chat", viewport: { width: 390, height: 844 } });
  const box = eika.page.getByLabel("Message", { exact: true });
  const draft = "## Draft\n<script>window.bad = 1</script>\n[bad](javascript:alert(1))";
  await box.fill(draft);
  const preview = eika.page.getByLabel("Message preview");
  await expect(preview.locator("script")).toHaveCount(0);
  await expect(preview.locator('a[href^="javascript:"]')).toHaveCount(0);
  // The bottom bar gives way to a focused composer; leave it to reach Settings.
  await box.blur();
  await eika.click("Settings");
  await eika.click("Appearance");
  await eika.select("Message input", "Keyboard");
  await eika.press("Escape");
  await expect(box).toHaveValue(draft);
  await expect(preview).toHaveCount(0);
  await box.fill("keyboard message");
  await box.press("Enter");
  await expect(box).toHaveValue("");
  await box.blur();
  await eika.click("Settings");
  await eika.click("Appearance");
  await eika.select("Message input", "Touch");
  await eika.press("Escape");
  await box.fill("touch message");
  await box.press("Enter");
  await expect(box).toHaveValue("touch message\n");
});
