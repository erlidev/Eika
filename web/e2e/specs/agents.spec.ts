/**
 * The Agents and Instructions tabs of the Settings dialog: the class and
 * prompt of each built-in agent, each unset field showing what it runs as
 * built, and the user's About me and rules. The editors are compared pixel for
 * pixel on a desktop and on a phone; the rest is checked by what the page says
 * and offers. The UI no longer makes a profile request.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

const phone = { width: 390, height: 844 };

/** openAgent opens Settings on one agent's editor. */
async function openAgent(eika: EikaDriver, name: string): Promise<void> {
  await eika.click("Settings");
  await eika.click("role=tab[name=Agents]");
  await eika.click(`role=button[name=/^${name}/]`);
}

test("an agent's editor on a phone", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "home-empty", viewport: phone });
  await openAgent(eika, "researcher");
  await eika.click("Override prompt");
  await eika.fill("Prompt", "Read the web and report in one paragraph.");
  await eika.click("role=radio[name=Deep]");
  await expectShot(eika, "agent-editor-phone", "role=dialog");
});

test("class and prompt are saved, compared with the built-in, and reset", async ({ open }) => {
  const eika = await open({ scenario: "home-empty" });
  await openAgent(eika, "eika");
  await eika.click("role=radio[name=Deep]");
  await eika.click("Override prompt");
  await eika.fill("Prompt", "You are Eika. Answer in one line.");
  await expect(eika.page.getByText(/Differs from the built-in prompt/)).toBeVisible();
  await eika.click("Changes to the built-in prompt");
  await expect(
    eika.page.getByRole("group", { name: "Changes to the built-in prompt" }),
  ).toBeVisible();
  await eika.click("Save agent");
  await expect(eika.page.getByText("Saved. The next run uses it.")).toBeVisible();
  expect(eika.mock.world.agentOverrides.eika).toEqual({
    class: "deep",
    prompt: "You are Eika. Answer in one line.",
  });

  // The list says it is changed, and the editor starts from what was saved.
  await eika.click("All agents");
  await expect(eika.page.getByRole("button", { name: /^eika deep changed/ })).toBeVisible();
  await eika.click("role=button[name=/^eika/]");
  await expect(eika.page.getByRole("radio", { name: "Deep" })).toBeChecked();
  await expect(eika.page.getByLabel("Prompt", { exact: true })).toHaveValue(
    "You are Eika. Answer in one line.",
  );

  // Reset one field, then both.
  await eika.click("Reset prompt");
  await expect(eika.page.getByRole("button", { name: "Override prompt" })).toBeVisible();
  await eika.click("Save agent");
  await expect(eika.page.getByText("Saved. The next run uses it.")).toBeVisible();
  expect(eika.mock.world.agentOverrides.eika).toEqual({ class: "deep", prompt: null });
  await eika.click("Reset to built-in");
  await eika.click("role=button[name=Reset]");
  await expect(eika.page.getByRole("radio", { name: "Deep" })).not.toBeChecked();
  await expect(eika.page.getByRole("button", { name: "Save agent" })).toBeDisabled();
  expect(eika.mock.world.agentOverrides.eika).toBeUndefined();
});

test("instructions: About me and rules are saved together", async ({ open }) => {
  const eika = await open({ scenario: "home-empty" });
  await eika.click("Settings");
  await eika.click("role=tab[name=Instructions]");
  await expect(eika.page.getByRole("button", { name: "Save instructions" })).toBeDisabled();
  await eika.fill("About me", "I run a small bakery.\nI prefer metric units.");
  await eika.fill("Rules", "Ask before spending money.");
  await eika.click("Save instructions");
  await expect(eika.page.getByText("Saved. New conversations use it.")).toBeVisible();
  expect(eika.mock.world.instructions).toEqual({
    about_me: "I run a small bakery.\nI prefer metric units.",
    rules: "Ask before spending money.",
  });

  // What was saved is what a reload reads back, and a clear is a save.
  await eika.page.reload();
  await eika.settle();
  await eika.click("Settings");
  await eika.click("role=tab[name=Instructions]");
  await expect(eika.page.getByLabel("Rules", { exact: true })).toHaveValue(
    "Ask before spending money.",
  );
  await eika.fill("Rules", "");
  await eika.click("Save instructions");
  await expect(eika.page.getByText("Saved. New conversations use it.")).toBeVisible();
  expect(eika.mock.world.instructions.rules).toBe("");
});
