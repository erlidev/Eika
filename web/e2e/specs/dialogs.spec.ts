/** Dialogs and panels reached from the workbench. */

import { test } from "../fixtures.ts";

test("new project dialog", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("New project");
  await expectShot(eika, "new-project", "role=dialog");
});

test("command palette", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.press("Control+K");
  await expectShot(eika, "command-palette");
});

test("run panel", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Run");
  await expectShot(eika, "run-panel", 'role=complementary[name="Context panels"]');
});

test("project tree expanded", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("payments-api remote");
  await expectShot(eika, "project-tree", 'role=navigation[name="Projects"]');
});
