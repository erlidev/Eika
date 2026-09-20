/**
 * One baseline per screen and state, in both themes. A change that moves
 * pixels on any of them fails here; `npm run visual:update` accepts it.
 */

import { expect, test } from "../fixtures.ts";

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme`, () => {
    test("setup: choose a password", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "setup-new", theme });
      await expectShot(eika, `setup-password-${theme}`);
    });

    test("setup: docker unreachable", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "setup-docker-down", theme });
      await expectShot(eika, `setup-docker-down-${theme}`);
    });

    test("sign in", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "signed-out", theme });
      await expectShot(eika, `sign-in-${theme}`);
    });

    test("workbench, empty", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "empty", theme });
      await expectShot(eika, `workbench-empty-${theme}`);
    });

    test("session transcript", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await expectShot(eika, `session-${theme}`);
    });

    test("settings: models", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await expectShot(eika, `settings-models-${theme}`, "role=dialog");
    });
  });
}

test("session on a phone", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench", viewport: { width: 390, height: 844 } });
  await expectShot(eika, "session-phone");
});

test("sign in with the wrong password", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "signed-out" });
  await eika.fill("Password", "wrong");
  await eika.click("Sign in");
  await expect(eika.page.getByRole("alert")).toBeVisible();
  await expectShot(eika, "sign-in-refused");
});
