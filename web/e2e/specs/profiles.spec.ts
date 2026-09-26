/**
 * Agent profiles: the Settings tab that lists and edits them, where an unset
 * field shows what it falls through to and a set one has a reset, and the
 * status bar's profile, which opens the same editor over what the session
 * sets for itself. The editor is compared pixel for pixel once; the rest is
 * checked by what the page says and offers.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

/** openProfiles opens Settings on the Profiles tab. */
async function openProfiles(eika: EikaDriver): Promise<void> {
  await eika.click("Settings");
  await eika.click("role=tab[name=Profiles]");
}

test("the profiles and the default one", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await expect(eika.page.getByRole("combobox", { name: "Default profile" })).toHaveText("Default");
  await expectAria(eika, "profiles-list", "role=dialog");
});

test("a profile's editor shows what each unset field falls through to", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await eika.click("role=button[name=/^Reviewer/]");
  // The Model tab first: every value falls through, and says from where.
  await expect(eika.page.getByLabel("Max output tokens", { exact: true })).toHaveAttribute(
    "placeholder",
    "128000",
  );
  await expect(eika.page.getByRole("radiogroup", { name: "Reasoning effort" })).toBeVisible();
  await expectShot(eika, "profile-editor", "role=dialog");

  // Set: the instructions, with a reset. Unset: the base prompt, shown muted.
  await eika.click("role=tab[name=Prompt]");
  await expect(eika.page.getByLabel("Extra instructions", { exact: true })).toHaveValue(
    "Review the change and report problems. Do not edit files.",
  );
  await expect(eika.page.getByRole("button", { name: "Reset extra instructions" })).toBeVisible();
  await expect(
    eika.page.getByRole("button", { name: "Override base prompt of a workspace session" }),
  ).toBeVisible();

  await eika.click("role=tab[name=Sampling]");
  const temperature = eika.page.getByRole("textbox", { name: "Temperature" });
  await expect(temperature).toHaveValue("0.2");
  await expect(eika.page.getByRole("slider", { name: "Temperature slider" })).toHaveAttribute(
    "aria-valuenow",
    "0.2",
  );
  await eika.click("Reset temperature");
  await expect(temperature).toHaveValue("");
  await eika.click("Save profile");
  await expect(eika.page.getByText("Saved. The next run uses it.")).toBeVisible();

  await eika.click("role=tab[name=Tools]");
  await expect(eika.page.getByRole("switch", { name: "bash" })).toBeChecked();
  await expect(eika.page.getByRole("switch", { name: "spawn_agent" })).not.toBeChecked();
  await eika.fill("Search the tools", "agent");
  await expect(eika.page.getByRole("switch", { name: "bash" })).toBeHidden();
  await expect(eika.page.getByRole("switch", { name: "spawn_agent" })).toBeVisible();
});

test("the editor's controls set and unset each kind of value", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await eika.click("role=button[name=/^Reviewer/]");
  // A segmented choice: clicking the chosen value again unsets it.
  await eika.click("role=radio[name=high]");
  await expect(eika.page.getByRole("radio", { name: "high" })).toBeChecked();
  await eika.click("role=radio[name=high]");
  await expect(eika.page.getByRole("radio", { name: "high" })).not.toBeChecked();
  await eika.click("role=radio[name=Replay]");

  // Stop sequences are chips, added with Enter and removed with their button.
  await eika.click("role=tab[name=Sampling]");
  await eika.fill("Stop sequences", "END");
  await eika.page.keyboard.press("Enter");
  await expect(
    eika.page.getByRole("button", { name: "Remove the stop sequence END" }),
  ).toBeVisible();

  // The cost strip follows the tool choice.
  await eika.click("role=tab[name=Tools]");
  const strip = eika.page.getByRole("button", { name: /^Tools ~/ });
  const before = await strip.textContent();
  await eika.click("role=button[name='All built-in tools']");
  await expect(strip).not.toHaveText(before ?? "");

  await eika.click("Save profile");
  await expect(eika.page.getByText("Saved. The next run uses it.")).toBeVisible();
  await eika.click("role=tab[name=Model]");
  await expect(eika.page.getByRole("radio", { name: "Replay" })).toBeChecked();
});

test("choosing a model shows what that model sets before the profile is saved", async ({
  open,
}) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await eika.click("role=button[name=/^Reviewer/]");
  await eika.select("role=combobox[name=Model]", "gpt-5-mini");
  await expect(eika.page.getByLabel("Max output tokens", { exact: true })).toHaveAttribute(
    "placeholder",
    "64000",
  );

  // The same in a session's editor, for a profile chosen and not saved.
  await eika.page.keyboard.press("Escape");
  await eika.click("role=button[name=/^Profile: Reviewer/]");
  await eika.select("Profile", "Fast");
  await expect(eika.page.getByLabel("Max output tokens", { exact: true })).toHaveAttribute(
    "placeholder",
    "4096",
  );
});

test("a new profile is added and a bad parameter is caught before it is sent", async ({ open }) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await eika.click("New profile");
  await eika.fill("Name", "Hot");
  await eika.click("role=tab[name=Sampling]");
  await eika.fill("Temperature", "warm");
  await eika.click("Add profile");
  await expect(eika.page.getByText("Enter a number.")).toBeVisible();
  await eika.fill("Temperature", "3");
  await eika.click("Add profile");
  await expect(eika.page.getByRole("alert")).toContainText(
    "Could not add the profile: temperature must be from 0 to 2",
  );
  await eika.fill("Temperature", "1.5");
  await eika.click("Add profile");
  await eika.click("All profiles");
  await expect(eika.page.getByRole("button", { name: /^Hot/ })).toBeVisible();
});

test("the last profile cannot be deleted", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await openProfiles(eika);
  await eika.click("role=button[name=/^Default/]");
  await expect(eika.page.getByRole("button", { name: "Delete" })).toBeDisabled();
});

test("the status bar names the session's profile and edits its overrides", async ({
  open,
  expectAria,
}) => {
  const eika = await open({ scenario: "profiles" });
  const profile = eika.page.getByRole("button", { name: /^Profile: Reviewer/ });
  await expect(profile).toHaveAccessibleName("Profile: Reviewer, overridden by this session");
  await eika.click("role=button[name=/^Profile: Reviewer/]");
  await expectAria(eika, "session-configuration", "role=dialog");

  await eika.click("role=tab[name=Sampling]");
  await expect(eika.page.getByRole("textbox", { name: "Temperature" })).toHaveValue("0.7");
  await eika.click("Reset temperature");
  await eika.click("Save");
  await expect(profile).toHaveAccessibleName("Profile: Reviewer");

  // Another profile, chosen for this session alone.
  await eika.click("role=button[name=/^Profile: Reviewer/]");
  await eika.select("Profile", "Fast");
  await eika.click("Save");
  await expect(eika.page.getByRole("button", { name: /^Profile: Fast/ })).toBeVisible();
  await expect(eika.page.getByRole("combobox", { name: "Model" })).toHaveText("gpt-5-mini");
});
