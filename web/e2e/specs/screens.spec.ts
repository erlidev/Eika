/**
 * Setup and sign-in screens, and the models settings dialog.
 */

import { expect, test } from "../fixtures.ts";

test("sign in", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "signed-out" });
  await expectShot(eika, "sign-in-light");
});

test("sign in with the wrong password", async ({ open }) => {
  const eika = await open({ scenario: "signed-out" });
  await eika.fill("Password", "wrong");
  await eika.click("Sign in");
  await expect(eika.page.getByRole("alert")).toBeVisible();
});
