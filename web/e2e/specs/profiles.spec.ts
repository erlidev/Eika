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
  // Set: the instructions, with a reset. Unset: the base prompt, shown muted.
  await expect(eika.page.getByLabel("Extra instructions")).toHaveValue(
    "Review the change and report problems. Do not edit files.",
  );
  await expect(eika.page.getByRole("button", { name: "Reset extra instructions" })).toBeVisible();
  await expect(
    eika.page.getByRole("button", { name: "Override base prompt of a workspace session" }),
  ).toBeVisible();
  await expectShot(eika, "profile-editor", "role=dialog");

  await eika.click("role=tab[name=Sampling]");
  await expect(eika.page.getByLabel("Temperature")).toHaveValue("0.2");
  await expect(eika.page.getByLabel("Max output tokens")).toHaveAttribute(
    "placeholder",
    "128000 (the model)",
  );
  await eika.click("Reset temperature");
  await expect(eika.page.getByLabel("Temperature")).toHaveValue("");
  await eika.click("Save profile");
  await expect(eika.page.getByText("Saved. The next run uses it.")).toBeVisible();

  await eika.click("role=tab[name=Tools]");
  await expect(eika.page.getByRole("checkbox", { name: "bash" })).toBeChecked();
  await expect(eika.page.getByRole("checkbox", { name: "spawn_agent" })).not.toBeChecked();
});

test("choosing a model shows what that model sets before the profile is saved", async ({
  open,
}) => {
  const eika = await open({ scenario: "profiles" });
  await openProfiles(eika);
  await eika.click("role=button[name=/^Reviewer/]");
  await eika.select("Model", "gpt-5-mini");
  await eika.click("role=tab[name=Sampling]");
  await expect(eika.page.getByLabel("Max output tokens")).toHaveAttribute(
    "placeholder",
    "64000 (the model)",
  );

  // The same in a session's editor, for a profile chosen and not saved.
  await eika.page.keyboard.press("Escape");
  await eika.click("role=button[name=/^Profile: Reviewer/]");
  await eika.select("Profile", "Fast");
  await eika.click("role=tab[name=Sampling]");
  await expect(eika.page.getByLabel("Max output tokens")).toHaveAttribute(
    "placeholder",
    "4096 (the profile)",
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
  await expect(eika.page.getByLabel("Temperature")).toHaveValue("0.7");
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
