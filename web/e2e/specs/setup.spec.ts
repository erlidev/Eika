/**
 * The guided setup past its first screen: the provider step's field
 * problems, the models step, and the steps on a phone, where the stepper
 * gives way to "Step n of 5", compared pixel for pixel.
 */

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };

test("provider step: a base URL the harness would refuse", async ({ open }) => {
  const eika = await open({ scenario: "setup-signed-in" });
  await eika.click("Other");
  await eika.fill("Name", "vLLM");
  await eika.fill("Base URL", "http://localhost:8000/v1#models");
  await expect(eika.page.getByLabel("Base URL")).toHaveAccessibleDescription(/query/);
  await expect(eika.page.getByRole("button", { name: "Save and continue" })).toBeDisabled();
});

test("models step", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "setup-signed-in" });
  await eika.fill("API key", "sk-test-1234567890abcdef");
  await eika.click("Save and continue");
  await eika.click("gpt-5");
  await expect(eika.page.getByRole("heading", { name: "Choose models" })).toBeVisible();
  await expectAria(eika, "setup-models", "role=main");
});

test.describe("on a phone", () => {
  test("password step", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "setup-new", viewport: phone });
    await expectShot(eika, "setup-password-phone");
  });

  test("provider step", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "setup-signed-in", viewport: phone, theme: "dark" });
    await expectShot(eika, "setup-provider-phone-dark");
  });

  test("sandbox step with Docker unreachable", async ({ open }) => {
    const eika = await open({ scenario: "setup-docker-down", viewport: phone });
    await expect(eika.page.getByText("Step 4 of 5")).toBeVisible();
    const overflow = await eika.page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow, "the page is wider than the phone").toBeLessThanOrEqual(0);
  });
});
