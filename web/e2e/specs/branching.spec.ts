/**
 * Branching a session: rewinding to a message the user sent, the entries a
 * run cannot continue from, and the forks and child agents the sidebar draws
 * under the session they came from.
 */

import { expect, test } from "../fixtures.ts";

test("a fork and a child agent sit under the session they came from", async ({
  open,
  expectAria,
}) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("payments-api remote");
  await eika.click("role=button[name=/running fix-retries/]");

  // Each is nested in a list of its own, not flattened into a sibling row.
  const parent = eika.page.getByRole("listitem").filter({
    hasText: "Add exponential backoff to retries",
  });
  await expect(parent.getByRole("button", { name: "add-backoff-tests (agent)" })).toBeVisible();
  const older = eika.page.getByRole("listitem").filter({ hasText: "Investigate flaky test," });
  await expect(
    older.getByRole("button", { name: "Investigate flaky test, pinned seed (fork)" }),
  ).toBeVisible();

  // The child agent's workspace is reached through its session, so it is not
  // listed beside its parent's as a container of its own.
  await expect(
    eika.page.getByRole("button", { name: /Workspace state.*add-backoff-tests/ }),
  ).toBeHidden();

  await expectAria(eika, "session-nesting", 'role=navigation[name="Projects"]');
});

test("rewinding a message takes the conversation back and puts it in the box", async ({ open }) => {
  const eika = await open({ scenario: "agent-tools" });
  // The transcript's own rows: the same words are also in the live region
  // that announces a finished turn, and in the session tree's previews.
  const answers = eika.page.getByRole("region", { name: "Session" }).getByRole("article");
  await eika.send("Run the tests");
  await expect(answers.filter({ hasText: "Tests pass." })).toBeVisible();

  // The second message the user sent: the first is the stored conversation's.
  await eika.page
    .getByRole("button", { name: "Rewind the conversation to this message and edit it" })
    .nth(1)
    .click();

  // The turns that followed are off the conversation and the message is back
  // in the composer, ready to be edited and asked again.
  await expect(answers.filter({ hasText: "Tests pass." })).toHaveCount(0);
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveValue("Run the tests");
  // Nothing was deleted: the branch is still in the tree.
  await eika.click("Tree");
  await expect(eika.page.getByRole("treeitem", { name: /Tests pass/ })).toBeVisible();
});

test("an entry in the middle of a turn offers no branch", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Tree");
  // The assistant turn that asked for `bash` has its result on a later
  // entry, so a run cannot continue from it and the harness would refuse it.
  const midTurn = eika.page.getByRole("treeitem", { name: /mid-turn, cannot branch here/ }).first();
  await expect(midTurn).toBeVisible();
  for (const name of [/^Set head to/, /^Fork from/]) {
    await expect(midTurn.getByRole("button", { name })).toBeDisabled();
  }
  // The entry that answers the last call closes the turn and is a place a
  // branch may start.
  const answered = eika.page.getByRole("treeitem", { name: /^tool_result/ }).first();
  await expect(answered.getByRole("button", { name: /^Fork from/ })).toBeEnabled();
});
