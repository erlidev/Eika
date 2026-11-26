import { expect, test } from "../fixtures.ts";

test("review and approve a routine", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "schedules" });
  await expect(eika.page.getByText("Approval needed: Review sources")).toBeVisible();
  await expectShot(eika, "schedules-desktop");
  await expectShot(
    eika,
    "schedules-approval-desktop",
    'css=section:has-text("Approval needed: Review sources")',
  );
  await eika.page.getByRole("button", { name: "Approve" }).click();
  await expect(eika.page.getByText("Approval needed: Review sources")).toHaveCount(0);
});

test.describe("on a phone", () => {
  test.use({ hasTouch: true, isMobile: true });
  test("schedules on a phone", async ({ open, expectShot }) => {
    const eika = await open({
      scenario: "schedules",
      viewport: { width: 390, height: 844 },
    });
    await expect(eika.page.getByText("Approval needed: Review sources")).toBeVisible();
    await expectShot(eika, "schedules-phone");
    await expectShot(
      eika,
      "schedules-approval-phone",
      'css=section:has-text("Approval needed: Review sources")',
    );
  });
});

test("preview an edited schedule", async ({ open }) => {
  const eika = await open({ scenario: "schedules" });
  await eika.page.getByRole("button", { name: "New routine" }).click();
  await eika.page.getByLabel("Cron expression (five fields)").fill("0 8 * * *");
  await expect(eika.page.getByRole("dialog").getByText("Every day at 08:00 UTC")).toBeVisible();
});
