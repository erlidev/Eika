/**
 * Keeping the sidebar in order: renaming a session or a workspace in place,
 * pinning it to the top of its list, and archiving it under a folded
 * heading. Every row offers the same actions from its menu button and from a
 * right-click, and the session header renames the session that is open.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

/** openRetries opens the workbench with fix-retries' sessions showing. */
async function openRetries(eika: EikaDriver): Promise<void> {
  await eika.click("payments-api remote");
  await eika.click("role=button[name=/running fix-retries/]");
}

test("a session is renamed in place from its menu, and F2 does the same", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await openRetries(eika);
  const projects = eika.page.getByRole("navigation", { name: "Projects" });

  await eika.click("Actions for Add exponential backoff to retries");
  await eika.click("Rename");
  const field = projects.getByLabel("Rename Add exponential backoff to retries");
  await expect(field).toBeFocused();
  await field.fill("Backoff with jitter");
  await field.press("Enter");

  // The row and the open session's header both carry the new title, and
  // the focus is back on the row it started from.
  const row = projects.getByRole("button", { name: "Backoff with jitter", exact: true });
  await expect(row).toBeFocused();
  await expect(eika.page.getByRole("heading", { name: "Backoff with jitter" })).toBeVisible();

  // F2 on the focused row renames it too; Escape keeps the name it had.
  await row.press("F2");
  await projects.getByLabel("Rename Backoff with jitter").fill("Something else");
  await eika.press("Escape");
  await expect(row).toBeFocused();
  await expect(projects.getByText("Something else")).toBeHidden();
});

test("the session header renames the session that is open", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await openRetries(eika);
  await eika.click("role=heading[name='Add exponential backoff to retries'] >> role=button");
  await eika.fill("Session title", "Retry with backoff");
  await eika.press("Enter");
  await expect(eika.page.getByRole("heading", { name: "Retry with backoff" })).toBeVisible();
  await expect(
    eika.page
      .getByRole("navigation", { name: "Projects" })
      .getByRole("button", { name: "Retry with backoff", exact: true }),
  ).toBeVisible();
});

test("pinning puts a session first and archiving folds it away", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await openRetries(eika);
  const projects = eika.page.getByRole("navigation", { name: "Projects" });

  await eika.click("Actions for Investigate flaky test");
  await eika.click("Pin");
  const sessions = projects
    .getByRole("listitem")
    .filter({ has: eika.page.getByRole("button", { name: /running fix-retries/ }) })
    // The project's item holds the workspace's too; the workspace's is the last.
    .last()
    .getByRole("list")
    .first();
  await expect(sessions.getByRole("button").first()).toHaveAccessibleName(
    "Investigate flaky test (pinned)",
  );

  await eika.click("Actions for add-backoff-tests");
  await eika.click("Archive");
  await expect(projects.getByRole("button", { name: /^add-backoff-tests/ })).toBeHidden();
  await eika.click("Archived sessions (1)");
  await expect(projects.getByRole("button", { name: /^add-backoff-tests/ })).toBeVisible();
  await expectAria(eika, "organized-sessions", 'role=navigation[name="Projects"]');

  // Unarchiving puts it back under the session it came from.
  await eika.click("Actions for add-backoff-tests");
  await eika.click("Unarchive");
  await expect(projects.getByRole("button", { name: /Archived sessions/ })).toBeHidden();
  const parent = projects.getByRole("listitem").filter({ hasText: "Add exponential backoff" });
  await expect(parent.getByRole("button", { name: /^add-backoff-tests/ })).toBeVisible();
});

test("a workspace is renamed from a right-click, pinned, and archived", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("payments-api remote");
  const projects = eika.page.getByRole("navigation", { name: "Projects" });

  await projects.getByRole("button", { name: /running fix-retries/ }).click({ button: "right" });
  await expectShot(eika, "row-menu", 'role=menu[name="Actions for fix-retries"]');
  await eika.click("Rename");
  await eika.fill("Rename fix-retries", "retries");
  await eika.press("Enter");
  // The name changes; the branch, which names the work in git, does not.
  await expect(
    projects.getByRole("button", { name: /running retries eika\/fix-retries/ }),
  ).toBeVisible();

  await eika.click("Actions for retries");
  await eika.click("Pin");
  const workspaces = projects.getByRole("button", { name: /^Workspace state/ });
  await expect(workspaces.first()).toHaveAccessibleName(/retries \(pinned\)/);

  await eika.click("Actions for upgrade-go");
  await eika.click("Archive");
  await expect(projects.getByRole("button", { name: /upgrade-go/ })).toBeHidden();
  await eika.click("Archived workspaces (1)");
  await expect(projects.getByRole("button", { name: /creating upgrade-go/ })).toBeVisible();
});

test("an archived chat says so in its header, with the way back", async ({ open }) => {
  const eika = await open({ scenario: "chat" });
  const header = eika.page.getByRole("region", { name: "Session" }).locator("header");
  const chats = eika.page.getByRole("navigation", { name: "Chats" });
  const title = (await chats.locator('[aria-current="page"]').textContent()) ?? "";

  await eika.click(`Actions for ${title}`);
  await eika.click("Archive");
  await expect(header.getByText("Archived")).toBeVisible();
  await expect(chats.locator('[aria-current="page"]')).toBeHidden();

  await header.getByRole("button", { name: "Unarchive" }).click();
  await expect(header.getByText("Archived")).toBeHidden();
  await expect(chats.locator('[aria-current="page"]')).toBeVisible();
});

test("a change the harness refuses says why beside the row", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await openRetries(eika);
  eika.mock.failRoute("PATCH /api/sessions/{id}", { status: 500 });
  await eika.click("Actions for Investigate flaky test");
  await eika.click("Pin");
  await expect(
    eika.page.getByRole("navigation", { name: "Projects" }).getByRole("alert"),
  ).toHaveText(/^Could not change the session: /);
});

test("a workspace starts and stops from its row without the menu", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("web-dashboard local");
  const projects = eika.page.getByRole("navigation", { name: "Projects" });

  // The quick buttons show without a hover, so nothing is under the pointer.
  await eika.page.mouse.move(0, 0);
  const start = projects.getByRole("button", { name: "Start dark-mode" });
  // Playwright counts a transparent element as visible, so the opacity of
  // the buttons' group is what says they show.
  await expect(start.locator("..")).toHaveCSS("opacity", "1");
  await start.click();
  await expect(projects.getByRole("button", { name: /running dark-mode/ })).toBeVisible();

  await eika.page.mouse.move(0, 0);
  await projects.getByRole("button", { name: "Stop dark-mode" }).click();
  await expect(projects.getByRole("button", { name: /stopped dark-mode/ })).toBeVisible();
});
