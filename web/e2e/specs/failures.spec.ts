/**
 * What the UI shows when the harness fails: a list that did not load says
 * so beside where it would be, with Retry, instead of passing for an empty
 * one; a save that failed says what was not saved. Routes are made to fail
 * with the mock's `failing` option and `failRoute`; `healRoute` lets the
 * Retry that follows succeed.
 */

import { expect, test } from "../fixtures.ts";

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme`, () => {
    test("models tab: the models did not load", async ({ open, expectShot }) => {
      const eika = await open({
        scenario: "providers",
        theme,
        failing: { "GET /api/models": { status: 500 } },
      });
      await eika.click("Settings");
      await expect(eika.page.getByText(/Could not load the models/)).toBeVisible();
      await expectShot(eika, `models-load-error-${theme}`, "role=dialog");
      eika.mock.healRoute("GET /api/models");
      await eika.click("Retry");
      await expect(eika.page.getByText("gpt-5-mini")).toBeVisible();
    });

    test("setup: the providers did not load", async ({ open, expectShot }) => {
      const eika = await open({
        scenario: "setup-signed-in",
        theme,
        failing: { "GET /api/providers": "network" },
      });
      await expect(
        eika.page.getByText(/Could not load the providers already configured/),
      ).toBeVisible();
      await expectShot(eika, `setup-providers-load-error-${theme}`);
      eika.mock.healRoute("GET /api/providers");
      await eika.click("Retry");
      await expect(
        eika.page.getByRole("heading", { name: "Connect a model provider" }),
      ).toBeVisible();
      await expect(eika.page.getByRole("button", { name: "Retry" })).toBeHidden();
    });

    test("sandbox check: the harness did not answer", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await eika.click("General");
      eika.mock.failRoute("GET /api/system", { status: 500 });
      await eika.click("Check again");
      await expect(eika.page.getByText(/Could not ask the harness/)).toBeVisible();
      await expect(
        eika.page.getByText("Not checked, since the harness did not answer."),
      ).toBeVisible();
      await expectShot(eika, `system-check-error-${theme}`, "role=dialog");
    });
  });
}

test("setup: the models did not load", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "setup-signed-in",
    failing: { "GET /api/models": { status: 502, bare: true } },
  });
  await expect(eika.page.getByText(/Could not load the models already configured/)).toBeVisible();
  await expect(eika.page.getByText(/did not answer \(HTTP 502\)/)).toBeVisible();
  await expectShot(eika, "setup-models-load-error");
});

test("general: the models and the settings did not load", async ({ open, expectShot }) => {
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
  await expectShot(eika, "general-settings-load-error", "role=dialog");
  eika.mock.healRoute("GET /api/settings");
  await eika.click("Retry");
  await expect(eika.page.getByText(/Could not load the models/)).toBeVisible();
  await expect(eika.page.getByText("The models did not load; see below")).toBeVisible();
  await expectShot(eika, "general-models-load-error", "role=dialog");
});

test("search: the status did not load", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "GET /api/search/status": "network" },
  });
  await eika.click("Settings");
  await eika.click("Search");
  await expect(eika.page.getByText(/Could not load the search status/)).toBeVisible();
  await expectShot(eika, "search-load-error", "role=dialog");
  eika.mock.healRoute("GET /api/search/status");
  await eika.click("Retry");
  await expect(eika.page.getByText("Web providers")).toBeVisible();
});

test("add models: Add waits for the models already added", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "providers",
    failing: { "GET /api/models": { status: 500 } },
  });
  await eika.click("Settings");
  await eika.click('role=button[name="Models"] >> nth=1');
  await eika.click("gpt-5");
  await expect(eika.page.getByText(/cannot be checked for duplicates yet/)).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Add 1 model" })).toBeDisabled();
  await expectShot(eika, "model-picker-load-error", "role=dialog");
});

test("could not change the default model", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "PUT /api/settings": { status: 500 } },
  });
  await eika.click("Settings");
  await eika.click("Make default");
  await expect(eika.page.getByText(/Could not change the default model/)).toBeVisible();
  await expectShot(eika, "default-model-error-models", "role=dialog");
  await eika.click("General");
  await eika.select("Default model", "gpt-5-mini");
  await expect(eika.page.getByText(/Could not change the default model/)).toBeVisible();
  await expectShot(eika, "default-model-error-general", "role=dialog");
});

test("general: the setup could not be reopened", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "workbench",
    failing: { "PUT /api/settings": "network" },
  });
  await eika.click("Settings");
  await eika.click("General");
  await eika.scroll("Run the setup again");
  await eika.click("Run the setup again");
  await eika.scroll("text=/Could not reopen the setup/");
  await expect(
    eika.page.getByText(/^Could not reopen the setup: the harness could not be reached\. Check/),
  ).toBeVisible();
  await expectShot(eika, "setup-again-error", "role=dialog");
});

for (const theme of ["light", "dark"] as const) {
  test(`search: the harness refuses the order (${theme})`, async ({ open, expectShot }) => {
    const eika = await open({
      scenario: "workbench",
      theme,
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
    await expectShot(eika, `search-order-refused-${theme}`, "role=dialog");
  });
}

test("sign in: the harness is unreachable", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "signed-out",
    failing: { "POST /api/auth/login": "network" },
  });
  await eika.fill("Password", "hunter2hunter");
  await eika.click("Sign in");
  await expect(
    eika.page.getByText(/^Could not sign in: the harness could not be reached\./),
  ).toBeVisible();
  await expectShot(eika, "sign-in-unreachable");
});

for (const [label, look] of [
  ["light", { theme: "light" }],
  ["dark", { theme: "dark" }],
  ["phone", { viewport: { width: 390, height: 844 } }],
] as const) {
  test(`start: the settings did not load, and Retry recovers (${label})`, async ({
    open,
    expectShot,
  }) => {
    const eika = await open({
      scenario: "workbench",
      ...look,
      failing: { "GET /api/settings": { status: 502, bare: true } },
    });
    await expect(eika.page.getByRole("heading", { name: "Eika could not load" })).toBeVisible();
    await expect(
      eika.page.getByText(/Could not load the settings: the harness did not answer/),
    ).toBeVisible();
    // Still signed in: a password form would only mislead.
    await expect(eika.page.getByLabel("Password")).toHaveCount(0);
    await expect(eika.page.getByRole("button", { name: "Sign in" })).toHaveCount(0);
    await expectShot(eika, `start-settings-error-${label}`);
    eika.mock.healRoute("GET /api/settings");
    await eika.click("Retry");
    await expect(eika.page.getByRole("button", { name: "Settings", exact: true })).toBeVisible();
  });
}

test("start: the harness hit an internal error", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "signed-out",
    failing: { "GET /api/auth/status": { status: 500 } },
  });
  await expect(eika.page.getByText(/unexpected error/)).toBeVisible();
  await expectShot(eika, "start-auth-status-500");
});
