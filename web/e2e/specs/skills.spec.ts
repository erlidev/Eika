/**
 * The Skills page: the list with state and warning counts, and the install by
 * file picker and drop. The shots pin the desktop split and the phone's list,
 * on a coarse pointer. The mock names a skill from the upload's file name.
 */

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };
const zip = (name: string) => ({
  name,
  mimeType: "application/zip",
  buffer: Buffer.from("PK\u0003\u0004zip"),
});

test("the list shows state, warning and hit counts", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "skills" });
  const list = eika.page.getByRole("navigation", { name: "Skills" });
  await expect(list.getByRole("link")).toHaveCount(3);
  await expect(list.getByRole("link", { name: /invoice-totals/ })).toContainText("Enabled");
  const digest = list.getByRole("link", { name: /web-digest/ });
  await expect(digest).toContainText("Disabled");
  await expect(digest).toContainText("2 warnings");
  await expect(digest).toContainText("3 screen hits");
  await expect(list.getByRole("link", { name: /old-report/ })).toContainText("Broken");
  await expectShot(eika, "skills-desktop");
});

test("the detail shows the SKILL.md with its hits marked, the fields, files and warnings", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "skills", path: "/skills/web-digest" });
  const source = eika.page.getByRole("region", { name: "SKILL.md" });
  await expect(source.locator("mark")).toHaveText(["Ignore", "✓"]);
  await expect(eika.page.getByRole("region", { name: "Fields" }).locator("mark")).toHaveText(
    "following its links",
  );
  await expect(eika.page.getByText("Flagged in the description:")).toBeVisible();
  await expect(eika.page.getByText("scripts/run.py is not executable")).toBeVisible();
  await expect(eika.page.getByRole("region", { name: "Files" })).toContainText("scripts/run.py");
  await expectShot(eika, "skills-detail-desktop");
});

test("a broken skill that was not screened says both", async ({ open }) => {
  const eika = await open({ scenario: "skills", path: "/skills/old-report" });
  await expect(eika.page.getByRole("alert")).toContainText("SKILL.md is missing on disk");
  await expect(eika.page.getByText("The injection screen did not run")).toBeVisible();
});

test("a zip chosen with the picker installs disabled, and a poisoned one shows its hit", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "skills" });
  const picker = eika.page.getByLabel("Skill zip");
  await picker.setInputFiles(zip("tea-order.zip"));
  await expect(eika.page.getByText("Installed tea-order. It is disabled")).toBeVisible();
  const list = eika.page.getByRole("navigation", { name: "Skills" });
  await expect(list.getByRole("link", { name: /^tea-order/ })).toContainText("Disabled");
  await picker.setInputFiles(zip("poison-pill.zip"));
  await expect(eika.page.getByText("1 screen hit.")).toBeVisible();
  await expectShot(eika, "skills-install-result");
  await picker.setInputFiles(zip("poison-pill.zip"));
  await expect(eika.page.getByText("already installed and unchanged")).toBeVisible();
});

test("a zip dropped on the zone installs", async ({ open }) => {
  const eika = await open({ scenario: "skills" });
  const data = await eika.page.evaluateHandle(() => {
    const t = new DataTransfer();
    t.items.add(new File(["PKzip"], "dropped-one.zip", { type: "application/zip" }));
    return t;
  });
  await eika.page.getByTestId("skill-drop").dispatchEvent("drop", { dataTransfer: data });
  await expect(eika.page.getByText("Installed dropped-one.")).toBeVisible();
});

test("a refused zip says why, plainly", async ({ open }) => {
  const eika = await open({ scenario: "skills" });
  await eika.page.getByLabel("Skill zip").setInputFiles(zip("secret.zip"));
  await expect(eika.page.getByRole("alert")).toContainText("contains a credential");
  const failed = await open({
    scenario: "skills",
    failing: { "POST /api/skills": { status: 503 } },
  });
  await failed.page.getByLabel("Skill zip").setInputFiles(zip("tea-order.zip"));
  await expect(failed.page.getByRole("alert")).toContainText("Could not install the skill");
});

test("a skill changed elsewhere appears without a reload", async ({ open }) => {
  const eika = await open({ scenario: "skills" });
  const list = eika.page.getByRole("navigation", { name: "Skills" });
  await expect(list.getByRole("link")).toHaveCount(3);
  const stored = eika.mock.world.skills.find((s) => s.name === "web-digest");
  if (stored) stored.enabled = true;
  eika.mock.emit(
    eika.mock.event("skills.changed", "skills", { name: "web-digest", change: "enabled" }),
  );
  await expect(list.getByRole("link", { name: /web-digest/ })).toContainText("Enabled");
});

test("a failing list is said, not blank", async ({ open }) => {
  const eika = await open({
    scenario: "skills",
    failing: { "GET /api/skills": { status: 500 } },
  });
  await expect(eika.page.getByRole("alert")).toBeVisible();
});

test.describe("on a phone", () => {
  test.use({ hasTouch: true, isMobile: true });

  test("the list, the More sheet entry, then a skill with a way back", async ({
    open,
    expectShot,
  }) => {
    const eika = await open({ scenario: "skills", viewport: phone });
    expect(await eika.page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
    await expectShot(eika, "skills-phone-list");
    await eika.page.getByRole("link", { name: /web-digest/ }).tap();
    await expect(
      eika.page.getByRole("link", { name: "Skills", exact: true }).first(),
    ).toBeVisible();
    await expectShot(eika, "skills-phone-detail");
    await eika.page.getByRole("link", { name: "Skills", exact: true }).first().tap();
    await expect(eika.page).toHaveURL(/\/skills$/);
  });
});
