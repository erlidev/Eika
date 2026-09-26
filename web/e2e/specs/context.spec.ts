/**
 * The Context panel and inspector: the next request, previewed live, and any
 * recorded one, split into the parts that fill the context window; the
 * inspector lays a request open whole, links each part of the next request
 * to the setting behind it, and copies it as JSON. The panel and the
 * inspector are compared pixel for pixel once each; the rest is checked by
 * what they say.
 */

import { expect, test } from "../fixtures.ts";
import { scenario } from "../harness/scenarios.ts";
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
  await expect(panel).toContainText("scaled to the last measured call");
  await expect(panel.getByRole("meter", { name: "Context window" })).toHaveAttribute(
    "aria-valuemax",
    "400000",
  );
  await expectShot(eika, "context-light", "role=tabpanel[name=Context]");
});

test("a part opens the inspector on it", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "profiles", theme: "dark" });
  await eika.click("role=tab[name=Context]");
  await eika.click("role=button[name=/^Context files, about/]");
  const dialog = eika.page.getByRole("dialog", { name: "Context inspector" });
  const detail = dialog.getByRole("region", { name: "Part detail" });
  await expect(detail.getByRole("group", { name: "AGENTS.md" })).toContainText("go test ./...");

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
  await eika.select("Request", "#1 · gpt-5 · 532 in · 40m ago");
  await expect(panel).toContainText("532 measured by the endpoint");
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
  expect(copied.system).toContain("You are Eika, a coding agent");
  expect(copied.system).toContain("Review the change and report problems.");
  expect(copied.tools.map((t) => t.name)).toEqual(["bash", "web_fetch"]);
  expect(copied.temperature).toBe(0.7);
});

test("a turn that ends is recorded", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  await eika.send("Now add jitter");
  await eika.click("role=combobox[name=Request]");
  await expect(eika.page.getByRole("option", { name: /^#3 · gpt-5/ })).toBeVisible();
});

test("a stopped workspace is previewed without its context files", async ({ open }) => {
  const world = scenario("profiles").build();
  const workspace = world.workspaces.find((w) => w.id === "ws-retries");
  if (workspace) workspace.state = "stopped";
  const eika = await open({ scenario: world, path: "/sessions/ses-backoff" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await expect(panel).toContainText("The workspace is not running, so its context files");
  await expect(panel.getByRole("button", { name: /^Base prompt, about/ })).toBeVisible();
  await expect(panel.getByRole("button", { name: /^Context files, about/ })).toHaveCount(0);
});
