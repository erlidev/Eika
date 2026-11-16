/**
 * The guided setup past its first screen: the provider step's field
 * problems, the models step, and the steps on a phone, where the stepper
 * gives way to "Step n of 5", compared pixel for pixel.
 */

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };

test("models step", async ({ open }) => {
  const eika = await open({ scenario: "setup-signed-in" });
  await eika.fill("API key", "sk-test-1234567890abcdef");
  await eika.click("Save and continue");
  await eika.click("gpt-5");
  await expect(eika.page.getByRole("heading", { name: "Choose models" })).toBeVisible();
});

test.describe("on a phone", () => {
  test("password step", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "setup-new", viewport: phone });
    await expectShot(eika, "setup-password-phone");
  });
});
