/**
 * Each screen as it first opens. The session is compared pixel for pixel in
 * both themes and on a phone, since it is where the UI spends its time; the
 * other screens once, in the light theme. What a screen says and offers is
 * checked by its accessibility tree, which a restyle does not change.
 */

import { expect, test } from "../fixtures.ts";

for (const theme of ["light", "dark"] as const) {
  test(`session transcript, ${theme} theme`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "workbench", theme });
    await expectShot(eika, `session-${theme}`);
  });
}

test("session on a phone", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench", viewport: { width: 390, height: 844 } });
  await expectShot(eika, "session-phone");
});

test("workbench, empty", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "empty" });
  await expectShot(eika, "workbench-empty-light");
});

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
