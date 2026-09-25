/**
 * The Context panel: the next request, previewed live, and any recorded
 * one, split into the parts that fill the context window, with the
 * parameters sent and the layer each came from. The panel is compared pixel
 * for pixel once, in each theme; the rest is checked by what it says.
 */

import { expect, test } from "../fixtures.ts";
import { scenario } from "../harness/scenarios.ts";

test("the next request by part", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await expect(panel.getByRole("combobox", { name: "Request" })).toHaveText("Next request, live");
  await expect(panel).toContainText("scaled to the last measured call");
  await expectShot(eika, "context-light", "role=tabpanel[name=Context]");
});

test("a segment opens its part", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await eika.click("role=button[name=/^Context files, about/]");
  await expect(panel.getByRole("group", { name: "AGENTS.md" })).toContainText("go test ./...");
  await eika.click("role=button[name=/^Built-in tools, about/]");
  const schema = panel.getByRole("group", { name: "Schema of bash" });
  await expect(schema).toContainText('"command": {');
  await eika.click("Raw schemas");
  await expect(schema).toContainText(
    '{"type":"object","properties":{"command":{"type":"string"}}}',
  );
});

test("a recorded request and where its parameters came from", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "profiles", theme: "dark" });
  await eika.click("role=tab[name=Context]");
  const panel = eika.page.getByRole("tabpanel", { name: "Context" });
  await eika.select("Request", "#1 · gpt-5 · 378 in · 40m ago");
  await expect(panel).toContainText("378 measured by the endpoint");
  await eika.click("Parameters");
  await expectAria(eika, "context-recorded", "role=tabpanel[name=Context]");
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
