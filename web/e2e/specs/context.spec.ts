/**
 * The Context panel and inspector: the next request, previewed live, and any
 * recorded one, split into the parts that fill the context window; the
 * inspector lays a request open whole, links each part of the next request
 * to the setting behind it, and copies it as JSON. The panel and the
 * inspector are compared pixel for pixel once each; the rest is checked by
 * what they say.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

/** inspect opens the Context panel, then the inspector on the whole request. */
async function inspect(eika: EikaDriver): Promise<void> {
  await eika.click("role=tab[name=Context]");
  await eika.click("Inspect the whole request");
}

test("the next request at a glance", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await expect(panel.getByRole("combobox", { name: "Request" })).toHaveText("Next request, live");
  // The next request is predicted by the endpoint's own tokenizer, and the
  // template's cost is the prediction less the parts, as a line of its own.
  await expect(panel).toContainText("tokens predicted, exact");
  await expect(panel.getByRole("list", { name: "Parts" })).toContainText("Template and formatting");
  await expect(panel.getByRole("meter", { name: "Context window" })).toHaveAttribute(
    "aria-valuemax",
    "400000",
  );
  await expectShot(eika, "context-light", "role=tabpanel[name=Context]");
});

test("a part opens the inspector on it", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "profiles", theme: "dark" });
  await eika.click("role=tab[name=Context]");
  await eika.click("role=button[name=/^Instructions, \\d+ tokens/]");
  const dialog = eika.page.getByRole("dialog", { name: "Context inspector" });
  const detail = dialog.getByRole("region", { name: "Part detail" });
  await expect(detail).toContainText("Review the change and report problems.");

  await eika.click("role=navigation[name=Parts] >> role=button[name=/^Tools/]");
  const bash = detail.getByRole("article", { name: "bash" });
  await expect(bash.getByRole("table")).toContainText("commandrequired");
  await expectShot(eika, "context-inspector-dark", "role=dialog");
  await eika.click("role=button[name='JSON schema of bash']");
  await expect(bash.getByRole("group", { name: "Schema of bash" })).toContainText('"command": {');
});

test("a recorded request and where its parameters came from", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await eika.select("Request", "#1 · gpt-5 · 533 in · 40m ago");
  await expect(panel.getByText("533", { exact: true }).first()).toBeVisible();
  await expect(panel).toContainText("tokens measured by the endpoint");
  // The remainder is what the endpoint measured less the parts, stated for
  // this call, with the cache and the counter's exact prediction beside it.
  await expect(panel.getByRole("list", { name: "Parts" })).toContainText("Template and formatting");
  await expect(panel).toContainText("Cached");
  await expect(panel).toContainText("533 exact");
  await eika.click("Inspect the whole request");
  await eika.click("role=navigation[name=Parts] >> role=button[name=Parameters]");
  const dialog = eika.page.getByRole("dialog", { name: "Context inspector" });
  // A record is what was sent then: nothing in it links to today's settings.
  await expect(dialog.getByRole("button", { name: /^Edit/ })).toHaveCount(0);
  await expectAria(eika, "context-recorded", "role=region[name='Part detail']");
});

test("a part of the next request opens the setting behind it", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await inspect(eika);
  // The instructions are the profile's, so its editor opens on them.
  await eika.click("role=button[name='Edit in Reviewer: instructions']");
  const settings = eika.page.getByRole("dialog", { name: "Settings" });
  await expect(settings.getByLabel("Name", { exact: true })).toHaveValue("Reviewer");
  await expect(settings.getByRole("tab", { name: "Prompt" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await eika.page.keyboard.press("Escape");

  // The temperature is the session's own, so the session's editor opens.
  await eika.click("Inspect the whole request");
  await eika.click("role=navigation[name=Parts] >> role=button[name=Parameters]");
  await eika.click("role=button[name='Edit for this session: temperature']");
  const session = eika.page.getByRole("dialog", { name: "This session's configuration" });
  await expect(session.getByRole("tab", { name: "Sampling" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(session.getByRole("textbox", { name: "Temperature" })).toHaveValue("0.7");
});

test("the request copies as JSON", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await inspect(eika);
  await eika.click("Copy as JSON");
  await expect(eika.page.getByRole("button", { name: "Copied" })).toBeVisible();
  const copied = JSON.parse(await eika.page.evaluate(() => navigator.clipboard.readText())) as {
    model: string;
    system: string;
    tools: { name: string }[];
    temperature: number;
  };
  expect(copied.model).toBe("gpt-5");
  expect(copied.system).toContain("You are Eika, an assistant in a chat");
  expect(copied.system).toContain("Review the change and report problems.");
  expect(copied.tools.map((t) => t.name)).toEqual(["bash", "web_fetch"]);
  expect(copied.temperature).toBe(0.7);
});

test("an endpoint that counts nothing: every part is estimated, and the remainder is n/a", async ({
  open,
}) => {
  const eika = await open({ scenario: "context-estimated" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await expect(panel).toContainText("tokens predicted, estimated");
  await expect(panel.getByText(/^~\d+$/).first()).toBeVisible();
  const parts = panel.getByRole("list", { name: "Parts" });
  // Nothing is scaled to make up a remainder there is nothing to state it against.
  await expect(parts.getByText("Template and formatting")).toBeVisible();
  await expect(parts).toContainText("n/a");
  await expect(panel).toContainText("A ~ marks a part the endpoint did not count itself");
  // The status bar says the same, with a tilde on the estimate.
  await expect(eika.page.getByText(/next ~\d+ estimated/)).toBeVisible();
});

test("a recorded call whose exact prediction drifted is shown as a bug", async ({ open }) => {
  const eika = await open({ scenario: "context-drift" });
  await eika.click("role=tab[name=Context]");
  await eika.select("Request", "#2 · gpt-5 · 754 in · 12m ago");
  const alert = eika.page.getByRole("alert");
  await expect(alert).toContainText("predicted 751 tokens for this call, exactly");
  await expect(alert).toContainText("the endpoint counted 754");
  await expect(alert).toContainText("a drift of -3");
  await expect(alert).toContainText("this is a bug in the counting");
  // A call that was predicted exactly says nothing of the kind.
  await eika.select("Request", "#1 · gpt-5 · 533 in · 40m ago");
  await expect(eika.page.getByRole("alert")).toHaveCount(0);
});

test("the status bar's meter: the cache, the next request and how it is known", async ({
  open,
}) => {
  const eika = await open({ scenario: "profiles" });
  // Nothing has been measured in this tab yet, so the fill and the cache are
  // n/a and the prediction stands alone, with its method.
  const bar = eika.page.getByText(/next \d+ exact/);
  await expect(bar).toBeVisible();
  await expect(eika.page.getByText("cached n/a")).toBeVisible();
  await eika.send("Now add jitter");
  // After a turn the endpoint's own figures arrive: the fill against the
  // effective window, and what it served from its cache.
  await expect(eika.page.getByText(/cached 1\.8k/)).toBeVisible();
  await expect(eika.page.getByText(/next \d+ exact/)).toBeVisible();
});

test("a turn that ends is recorded", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  await eika.send("Now add jitter");
  await eika.click("role=combobox[name=Request]");
  await expect(eika.page.getByRole("option", { name: /^#3 · gpt-5/ })).toBeVisible();
});
