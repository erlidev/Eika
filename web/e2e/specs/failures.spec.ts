/**
 * What the UI shows when the harness fails: a list that did not load says
 * so beside where it would be, with Retry, instead of passing for an empty
 * one; a save that failed says what was not saved. Routes are made to fail
 * with the mock's `failing` option and `failRoute`; `healRoute` lets the
 * Retry that follows succeed.
 *
 * One load error is compared pixel for pixel in both themes, and the page
 * Eika shows when it cannot start; the others are checked by their
 * accessibility tree, which places the message and its Retry in the section
 * that failed.
 */

import { expect, test } from "../fixtures.ts";

test("models tab: the models did not load", async ({ open }) => {
  const eika = await open({
    scenario: "providers",
    failing: { "GET /api/models": { status: 500 } },
  });
  await eika.click("Settings");
  await expect(eika.page.getByText(/Could not load the models/)).toBeVisible();
  eika.mock.healRoute("GET /api/models");
  await eika.click("Retry");
  await expect(eika.page.getByText("gpt-5-mini")).toBeVisible();
});

test("setup: the providers did not load", async ({ open }) => {
  const eika = await open({
    scenario: "setup-signed-in",
    failing: { "GET /api/providers": "network" },
  });
  await expect(
    eika.page.getByText(/Could not load the providers already configured/),
  ).toBeVisible();
  eika.mock.healRoute("GET /api/providers");
  await eika.click("Retry");
  await expect(eika.page.getByRole("heading", { name: "Connect a model provider" })).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Retry" })).toBeHidden();
});

test("general: the models and the settings did not load", async ({ open }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "GET /api/models": { status: 500 } },
  });
  eika.mock.failRoute("GET /api/settings", { status: 502, bare: true });
  await eika.click("Settings");
  // Deleting a provider refetches the settings, which fails.
  await eika.click("Delete OpenAI");
  await eika.click("Delete provider");
  // A refetch that fails must not sign the browser out of the workbench.
  await expect(eika.page.getByRole("dialog", { name: "Settings" })).toBeVisible();
  await eika.click("General");
  await expect(eika.page.getByText(/Could not load the settings/)).toBeVisible();
  eika.mock.healRoute("GET /api/settings");
  await eika.click("Retry");
  await expect(eika.page.getByText(/Could not load the models/)).toBeVisible();
});

test("could not change a model class", async ({ open }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "PUT /api/settings": { status: 500 } },
  });
  await eika.click("Settings");
  await eika.click("General");
  await eika.select("Main", "gpt-5-mini");
  await expect(eika.page.getByText(/Could not save the model classes/)).toBeVisible();
  await expect(eika.page.getByRole("combobox", { name: "Main" })).toHaveText("gpt-5");
});

test("search: the harness refuses the order", async ({ open }) => {
  const eika = await open({
    scenario: "workbench",
    failing: {
      "PUT /api/settings": { status: 400, message: 'search.order: unknown provider "exa2"' },
    },
  });
  await eika.click("Settings");
  await eika.click("Search");
  await eika.click("Move exa up");
  await eika.click("Save order");
  // A refusal names the action and the harness's reason, never "could not be reached".
  await expect(
    eika.page.getByText(
      'Could not save the provider order: search.order: unknown provider "exa2".',
    ),
  ).toBeVisible();
  await expect(eika.page.getByText(/could not be reached/)).toHaveCount(0);
});

test("sign in: the harness is unreachable", async ({ open }) => {
  const eika = await open({
    scenario: "signed-out",
    failing: { "POST /api/auth/login": "network" },
  });
  await eika.fill("Password", "hunter2hunter");
  await eika.click("Sign in");
  await expect(eika.page.getByRole("alert")).toHaveText(
    /^Could not sign in: the harness could not be reached\./,
  );
  // The password stays, so trying again takes one click.
  await expect(eika.page.getByLabel("Password")).toHaveValue("hunter2hunter");
});

test("start: the settings did not load, and Retry recovers", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "GET /api/settings": { status: 502, bare: true } },
  });
  await expect(eika.page.getByRole("heading", { name: "Eika could not load" })).toBeVisible();
  await expect(
    eika.page.getByText(/Could not load the settings: the harness did not answer/),
  ).toBeVisible();
  // Still signed in: a password form would only mislead.
  await expect(eika.page.getByLabel("Password")).toHaveCount(0);
  await expect(eika.page.getByRole("button", { name: "Sign in" })).toHaveCount(0);
  await expectShot(eika, "start-settings-error-light");
  eika.mock.healRoute("GET /api/settings");
  await eika.click("Retry");
  await expect(eika.page.getByRole("button", { name: "Settings", exact: true })).toBeVisible();
});
