/** Dialogs and panels reached from the workbench. */

import { expect, test } from "../fixtures.ts";

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

for (const theme of ["light", "dark"] as const) {
  test(`session tree: a tree the keyboard walks (${theme})`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "workbench", theme });
    await eika.click("Tree");
    const tree = eika.page.getByRole("tree", { name: "Session tree" });
    const items = tree.getByRole("treeitem");
    await expect(items.first()).toBeVisible();
    // One tab stop, on the head.
    const head = tree.locator('[role="treeitem"][aria-current="true"]');
    await expect(head).toHaveAttribute("tabindex", "0");
    await head.focus();
    await eika.press("Home");
    await expect(items.first()).toBeFocused();
    await eika.press("ArrowDown");
    await expect(items.nth(1)).toBeFocused();
    await expectShot(eika, `session-tree-${theme}`, 'role=complementary[name="Context panels"]');
  });
}

test("run status bar: the live connection is labelled", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  const live = eika.page.getByRole("status", { name: "Live updates: Live" });
  await expect(live).toBeVisible();
  await expect(live).toHaveAttribute("title", "Live updates on");
  await expectShot(eika, "run-status-bar", 'role=status[name="Live updates: Live"] >> xpath=../..');
});

test("the reasoning effort cycles through the model's own list", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  // gpt-5 offers low, medium, high and is on medium.
  const effort = eika.page.getByRole("button", { name: /Reasoning effort/ });
  await expect(effort).toHaveText(/medium/);
  await effort.click();
  await expect(effort).toHaveText(/high/);
  await effort.click();
  await expect(effort).toHaveText(/low/);
});

test("the context meter states what the endpoint measured", async ({ open }) => {
  const eika = await open({ scenario: "agent-reasoning" });
  await eika.send("How should the jitter work?");
  const meter = eika.page.getByTitle(/^Context:/);
  await expect(meter).toContainText("/400k");
  // No decode rate: the endpoint reports its token counts when a response
  // ends, so a tok/s during a turn could only be a guess.
  await expect(eika.page.getByText(/tok\/s/)).toHaveCount(0);
});
