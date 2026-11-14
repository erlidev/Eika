/**
 * Setup and sign-in screens, and the models settings dialog.
 */

import { expect, test } from "../fixtures.ts";

test("setup: choose a password", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "setup-new" });
  await expectShot(eika, "setup-password-light");
});

test("setup: docker unreachable", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "setup-docker-down" });
  await expectAria(eika, "setup-docker-down", "role=main");
});

test("sign in", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "signed-out" });
  await expectShot(eika, "sign-in-light");
});

test("sign in with the wrong password", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "signed-out" });
  await eika.fill("Password", "wrong");
  await eika.click("Sign in");
  await expect(eika.page.getByRole("alert")).toBeVisible();
  await expectAria(eika, "sign-in-refused", "role=main");
});

test("settings: the models tab", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await expectAria(eika, "settings-models", "role=dialog");
});
