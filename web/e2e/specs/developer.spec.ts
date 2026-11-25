/** Inspect a recorded request and its token accounting. */
import { expect, test } from "../fixtures.ts";

test("inspect a recorded request by part", async ({ open }) => {
  const eika = await open({ scenario: "recorded-calls" });
  await eika.click("Settings");
  await eika.click("Developer");
  const preview = eika.page.waitForRequest((request) =>
    request.url().includes("/api/sessions/ses-backoff/context"),
  );
  await eika.select("Session", "Add exponential backoff to retries");
  await preview;
  await eika.click("Request");
  await eika.click("role=option[name=/^#2/]");
  await expect(eika.page.getByText("Drift", { exact: true })).toBeVisible();
  await eika.click("Inspect request");
  const inspector = eika.page.getByRole("dialog", { name: "Context inspector" });
  await expect(inspector.getByRole("button", { name: "Copy as JSON" })).toBeVisible();
  await inspector
    .getByRole("navigation", { name: "Parts" })
    .getByRole("button", { name: /Messages/ })
    .click();
  await expect(inspector.getByRole("group", { name: "Show messages by" })).toBeVisible();
});

test("copy reports an unavailable clipboard", async ({ open }) => {
  const eika = await open({ scenario: "recorded-calls" });
  await eika.page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });
  await eika.click("Settings");
  await eika.click("Developer");
  await eika.select("Session", "Add exponential backoff to retries");
  await eika.click("Inspect request");
  await eika.click("Copy as JSON");
  await expect(eika.page.getByText("Could not copy: no clipboard")).toBeVisible();
});

test("an agent can use its assigned class without a main model", async ({ open }) => {
  const eika = await open({ scenario: "home-deep-only" });
  await expect(eika.page.getByRole("textbox", { name: "Message" })).toBeEnabled();
});
